package actions

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-194: an agent with self.manage makes its own schedules at once within
// bounds (chat or workflow, every 30 minutes at most, a daily cost cap of at
// most 5 USD); anything else, or no capability, waits for a person. The
// schedule's owner is the person the turn spoke for.
func TestSelfAutomation(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	conv, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "c"})
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", Status: "running", CreatedBy: "human:boss@x.io", ConversationID: conv.ID})
	st.Users().Create(ctx, storage.User{Email: "boss@x.io", Name: "Boss", Role: storage.RoleAdmin})
	svc := New(st, nil)
	self := Scope{ProjectID: p.ID, ConversationID: conv.ID, JobID: job.ID, Agent: "lead", Level: perm.Propose,
		Access: perm.Access{Level: perm.Propose, Caps: []string{perm.CapPropose, perm.CapSelf}}}
	plain := self
	plain.Access.Caps = []string{perm.CapPropose}
	propose := func(sc Scope, spec string) storage.Action {
		t.Helper()
		a, err := svc.Propose(ctx, sc, "create_automation", "", "r", storage.ActionArgs{Automation: []byte(spec)})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	ok := `{"name":"Điều phối %s","source":"schedule","every_minutes":30,"action":"chat","prompt":"rà việc","limits":{"daily_cost_usd":2}}`
	if a := propose(self, fmt.Sprintf(ok, "1")); a.Status != "done" {
		t.Fatalf("within bounds it waits: %s %s", a.Status, a.Detail)
	}
	list, _ := st.Automations().List(ctx, p.ID)
	if len(list) != 1 || list[0].CreatedBy != "boss@x.io" || list[0].Limits.DailyCostUSD != 2 {
		t.Fatalf("automation: %+v", list)
	}
	for name, spec := range map[string]string{
		"no capability":  fmt.Sprintf(ok, "2"),
		"every 10 min":   `{"name":"x","source":"schedule","every_minutes":10,"action":"chat","prompt":"p","limits":{"daily_cost_usd":2}}`,
		"cron too often": `{"name":"y","source":"schedule","cron":"*/15 8-18 * * 1-5","action":"chat","prompt":"p","limits":{"daily_cost_usd":2}}`,
		"no cost cap":    `{"name":"z","source":"schedule","every_minutes":60,"action":"chat","prompt":"p"}`,
		"cap too high":   `{"name":"w","source":"schedule","every_minutes":60,"action":"chat","prompt":"p","limits":{"daily_cost_usd":50}}`,
		"script":         `{"name":"s","source":"schedule","every_minutes":60,"action":"script","script":{"lang":"bash","body":"echo"},"limits":{"daily_cost_usd":1}}`,
	} {
		sc := self
		if name == "no capability" {
			sc = plain
		}
		if a := propose(sc, spec); a.Status != "pending" {
			t.Errorf("%s: %s", name, a.Status)
		}
	}
	// ADR-207, ADR-219: one that runs only when there is work may run every
	// 5 minutes, with no cost cap
	if a := propose(self, `{"name":"Điều phối dày","source":"schedule","every_minutes":5,"action":"chat","prompt":"p","only_if":{"board":"any"}}`); a.Status != "done" {
		t.Fatalf("every 5 minutes with only_if: %s %s", a.Status, a.Detail)
	}
	if a := propose(self, `{"name":"Quá dày","source":"schedule","every_minutes":2,"action":"chat","prompt":"p","only_if":{"board":"any"}}`); a.Status != "pending" {
		t.Fatalf("every 2 minutes: %s", a.Status)
	}
	if a := propose(self, `{"name":"Giờ hành chính","source":"schedule","cron":"0,30 8-17 * * 1-5","action":"chat","prompt":"p","limits":{"daily_cost_usd":3}}`); a.Status != "done" {
		t.Fatalf("every half hour in office hours: %s", a.Status)
	}
}

// ADR-204: with self.manage an agent makes an agent of its project on its
// own, never an administrator nor one reading folders outside, and only
// while the project has fewer than SelfMaxAgents.
func TestSelfChangeAgent(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	svc := New(st, nil)
	change := func(patch string) *storage.ConfigChange {
		return &storage.ConfigChange{Resource: "agent", Op: "create", Patch: []byte(patch)}
	}
	if !svc.selfChange(ctx, p.ID, change(`{"key":"thuc-thi","name":"Thực thi","permissions":{"level":"operate"}}`)) {
		t.Fatal("a plain agent waits")
	}
	for name, patch := range map[string]string{
		"administrator": `{"key":"a","name":"A","permissions":{"level":"operate","full_access":true}}`,
		"extra dirs":    `{"key":"b","name":"B","permissions":{"extra_dirs":["/etc"]}}`,
	} {
		if svc.selfChange(ctx, p.ID, change(patch)) {
			t.Errorf("%s: done on its own", name)
		}
	}
	// ADR-208: a workflow retired, a schedule it could have made deleted
	if !svc.selfChange(ctx, p.ID, &storage.ConfigChange{Resource: "workflow", Op: "delete", ID: "wf"}) {
		t.Error("deleting a workflow waits")
	}
	sched, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "s", Source: "schedule", Action: "chat", Enabled: true})
	script, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "x", Source: "schedule", Action: "script", Enabled: true})
	if !svc.selfChange(ctx, p.ID, &storage.ConfigChange{Resource: "automation", Op: "delete", ID: sched.ID}) {
		t.Error("deleting its schedule waits")
	}
	if svc.selfChange(ctx, p.ID, &storage.ConfigChange{Resource: "automation", Op: "delete", ID: script.ID}) {
		t.Error("deleted a script on its own")
	}
	if svc.selfChange(ctx, p.ID, &storage.ConfigChange{Resource: "agent", Op: "update", Patch: []byte(`{}`)}) {
		t.Error("changing an agent: done on its own")
	}
	for i := range SelfMaxAgents {
		if _, err := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: fmt.Sprintf("a%d", i), Name: fmt.Sprintf("A%d", i), ModelTier: storage.TierBalanced}); err != nil {
			t.Fatal(err)
		}
	}
	if svc.selfChange(ctx, p.ID, change(`{"key":"x","name":"X"}`)) {
		t.Error("over the cap: done on its own")
	}
}
