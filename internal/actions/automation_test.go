package actions

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func TestProposeAutomation(t *testing.T) { // ADR-041
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	svc := New(st, nil)
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", Agent: "Lead", Level: perm.Operate, Access: perm.Access{Level: perm.Operate, Caps: perm.Preset(perm.Operate)}}
	spec := func(v map[string]any) storage.ActionArgs {
		raw, _ := json.Marshal(v)
		return storage.ActionArgs{Automation: raw}
	}
	good := map[string]any{"name": "Kiểm tra log", "source": "schedule", "cron": "0 8 * * 1-5", "timezone": "Asia/Ho_Chi_Minh", "action": "script",
		"script": map[string]any{"lang": "bash", "body": "grep -c ERROR app.log || true"}, "escalate": map[string]any{"when": "signal", "action": "chat"}}
	bad := map[string]any{"name": "x", "source": "schedule", "cron": "99 * * * *", "action": "script", "script": map[string]any{"lang": "bash", "body": "echo"}}
	if _, err := svc.Propose(ctx, sc, "create_automation", "x", "vì sao", spec(bad)); err == nil {
		t.Fatal("bad cron accepted")
	}
	if _, err := svc.Propose(ctx, sc, "create_automation", "x", "", spec(map[string]any{"name": "x", "source": "schedule", "every_minutes": 5, "action": "script",
		"script": map[string]any{"lang": "ruby", "body": "puts 1"}})); err == nil {
		t.Fatal("unknown language accepted")
	}
	a, err := svc.Propose(ctx, sc, "create_automation", "Kiểm tra log", "báo lỗi mỗi sáng", spec(good))
	if err != nil || a.Status != "pending" {
		t.Fatalf("propose = %+v %v (never automatic, even at operate)", a, err)
	}
	done, err := svc.Decide(ctx, a.ID, true, "human:a@b.c")
	if err != nil || done.Status != "done" {
		t.Fatalf("decide = %+v %v", done, err)
	}
	list, _ := st.Automations().List(ctx, proj.ID)
	if len(list) != 1 || !list[0].Enabled || list[0].NextRunAt == nil || list[0].Script.Lang != "bash" || list[0].Escalate.When != "" || list[0].CreatedBy != "human:a@b.c" {
		t.Fatalf("automations = %+v", list)
	}
	// update: the script changes, the rest stays
	good["automation_id"] = list[0].ID
	good["script"] = map[string]any{"lang": "bash", "body": "echo v2"}
	u, err := svc.Propose(ctx, sc, "update_automation", list[0].ID, "sửa", spec(good))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Decide(ctx, u.ID, true, "human:a@b.c"); err != nil {
		t.Fatal(err)
	}
	if b, _ := st.Automations().Get(ctx, list[0].ID); b.Script.Body != "echo v2" || b.ID != list[0].ID {
		t.Fatalf("updated = %+v", b)
	}
}

func TestProposeAutomationAgentsMustBelongToTheProject(t *testing.T) { // I4
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	other, _ := st.Repos().Create(ctx, storage.Repo{Name: "q", Path: t.TempDir()})
	m, _ := st.OrgModels().Create(ctx, storage.OrgModel{RepoID: other.ID, Key: "m", Name: "m", Kind: "solo"})
	foreign, _ := st.Agents().Create(ctx, storage.Agent{OrgModelID: m.ID, Key: "x", Name: "X", Tier: storage.TierLead, ModelTier: "fast"})
	svc := New(st, nil)
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", Agent: "Lead", Level: perm.Propose, Access: perm.Access{Level: perm.Propose, Caps: perm.Preset(perm.Propose)}}
	for _, spec := range []map[string]any{
		{"name": "a", "source": "schedule", "every_minutes": 5, "action": "chat", "agent_id": foreign.ID},
		{"name": "b", "source": "schedule", "every_minutes": 5, "action": "script", "script": map[string]any{"lang": "bash", "body": "exit 1"},
			"escalate": map[string]any{"when": "failure", "agent_id": foreign.ID}},
	} {
		raw, _ := json.Marshal(spec)
		if _, err := svc.Propose(ctx, sc, "create_automation", spec["name"].(string), "", storage.ActionArgs{Automation: raw}); err == nil {
			t.Fatalf("another project's agent accepted: %v", spec)
		}
	}
}

// ADR-043: an approved proposal is logged as the agent's change, with the
// approver, the chat/job it came from, and before/after.
func TestApprovedAutomationIsAudited(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	svc := New(st, nil)
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", JobID: "job_1", Agent: "Lead", Level: perm.Operate, Access: perm.Access{Level: perm.Operate, Caps: perm.Preset(perm.Operate)}}
	raw, _ := json.Marshal(map[string]any{"name": "Nightly", "source": "schedule", "every_minutes": 30, "action": "script",
		"script": map[string]any{"lang": "bash", "body": "echo 1"}})
	a, err := svc.Propose(ctx, sc, "create_automation", "Nightly", "", storage.ActionArgs{Automation: raw})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Actions().Get(ctx, a.ID); got.JobID != "job_1" {
		t.Fatalf("proposal job = %q", got.JobID)
	}
	who := audit.Who{Kind: "agent", Name: "Lead", ApprovedBy: "a@x.io", Via: "chat", JobID: "job_1", ActionID: a.ID}
	if _, err := svc.Decide(audit.With(ctx, who), a.ID, true, "a@x.io"); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.Audit().List(ctx, storage.AuditFilter{Resource: "automation"})
	if len(rows) != 1 || rows[0].Action != "automation.create" || rows[0].ActorName != "Lead" || rows[0].ApprovedBy != "a@x.io" ||
		rows[0].JobID != "job_1" || rows[0].ActionID != a.ID || rows[0].ProjectID != proj.ID || rows[0].After["Name"] != "Nightly" || rows[0].Before != nil {
		t.Fatalf("audit: %+v", rows)
	}
}
