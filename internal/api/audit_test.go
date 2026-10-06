package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestAuditRecordsBeforeAfter(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "Hook", "source": "webhook", "action": "chat", "prompt": "x"}, nil)
	aid := body["automation"].(map[string]any)["id"].(string)
	resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/automations/"+aid, map[string]any{
		"name": "Hook 2", "source": "webhook", "action": "chat", "prompt": "x", "enabled": true}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("update = %d", resp.StatusCode)
	}
	rows, err := e.st.Audit().List(context.Background(), storage.AuditFilter{Resource: "automation", ResourceID: aid})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d %v", len(rows), err)
	}
	up := rows[0]
	if up.Action != "automation.update" || up.ActorKind != "human" || up.ActorName != "admin@x.io" || up.ActorID == "" || up.Via != "ui" || up.ProjectID != pid {
		t.Fatalf("update row: %+v", up)
	}
	if up.Before["name"] != "Hook" || up.After["name"] != "Hook 2" {
		t.Fatalf("before/after: %v → %v", up.Before, up.After)
	}
	if create := rows[1]; create.Before != nil || create.After["name"] != "Hook" || create.ProjectID != pid {
		t.Fatalf("create row: %+v", create)
	}
}

func TestAuditRedactsProviderKey(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "p", "kind": "anthropic", "api_key": "sk-secret-1"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create provider = %d %v", resp.StatusCode, body)
	}
	id := body["provider"].(map[string]any)["id"].(string)
	if resp, body := do(t, admin, "PATCH", e.srv.URL+"/api/providers/"+id, map[string]any{"name": "p2", "kind": "anthropic", "api_key": "sk-secret-2"}, nil); resp.StatusCode != 200 {
		t.Fatalf("update provider = %d %v", resp.StatusCode, body)
	}
	rows, _ := e.st.Audit().List(context.Background(), storage.AuditFilter{Resource: "provider", ResourceID: id})
	for _, r := range rows {
		for _, snap := range []map[string]any{r.Before, r.After, r.Detail} {
			for k, v := range snap {
				if s, ok := v.(string); ok && (s == "sk-secret-1" || s == "sk-secret-2") {
					t.Fatalf("%s leaked key in %q", r.Action, k)
				}
			}
		}
	}
	if len(rows) != 2 || rows[0].Before["name"] != "p" || rows[0].After["name"] != "p2" {
		t.Fatalf("provider rows: %+v", rows)
	}
}

// An agent's proposal that is approved but fails still leaves a row naming
// the agent, the approver and the job (ADR-043).
func TestAuditApprovedProposalThatFails(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	spec, _ := json.Marshal(map[string]any{"automation_id": "aut_gone", "name": "x", "source": "schedule", "every_minutes": 30, "action": "script",
		"script": map[string]any{"lang": "bash", "body": "echo"}})
	a, err := e.st.Actions().Create(ctx, storage.Action{ProjectID: pid, Kind: "update_automation", Target: "x", ProposedBy: "Lead", JobID: "job_9",
		Args: storage.ActionArgs{Automation: spec}})
	if err != nil {
		t.Fatal(err)
	}
	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/actions/"+a.ID+"/approve", map[string]any{}, nil); resp.StatusCode != 200 {
		t.Fatalf("approve = %d %v", resp.StatusCode, body)
	}
	rows, _ := e.st.Audit().List(ctx, storage.AuditFilter{JobID: "job_9"})
	var approve *storage.AuditEntry
	for i := range rows {
		if rows[i].Action == "action.approve" {
			approve = &rows[i]
		}
	}
	if approve == nil || approve.ActorKind != "agent" || approve.ActorName != "Lead" || approve.ApprovedBy != "admin@x.io" ||
		approve.ActionID != a.ID || approve.ProjectID != pid || approve.OK {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestAuditListAndStats(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	for _, n := range []string{"a", "b", "c"} {
		do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{"name": n, "source": "webhook", "action": "chat", "prompt": "x"}, nil)
	}
	resp, body := do(t, admin, "GET", e.srv.URL+"/api/audit?project="+pid+"&resource=automation&limit=2", nil, nil)
	entries := body["entries"].([]any)
	if resp.StatusCode != 200 || len(entries) != 2 || body["next_before"] == "" {
		t.Fatalf("page 1 = %d %v", resp.StatusCode, body)
	}
	first := entries[0].(map[string]any)
	if first["actor_kind"] != "human" || first["project_name"] != "shop" || first["after"] == nil {
		t.Fatalf("entry: %v", first)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/audit?project="+pid+"&resource=automation&limit=2&before="+body["next_before"].(string), nil, nil)
	if n := len(body["entries"].([]any)); n != 1 || body["next_before"] != "" {
		t.Fatalf("page 2 = %d next=%v", n, body["next_before"])
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/audit/stats?by=kind&project="+pid, nil, nil)
	rows := body["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["key"] != "human" || rows[0].(map[string]any)["count"].(float64) < 3 {
		t.Fatalf("stats = %v", body)
	}
	if resp, _ := do(t, admin, "GET", e.srv.URL+"/api/audit/stats?by=nope", nil, nil); resp.StatusCode != 400 {
		t.Fatalf("bad by = %d", resp.StatusCode)
	}

	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	for _, p := range []string{"/api/audit", "/api/audit/stats?by=day"} {
		if resp, _ := do(t, member, "GET", e.srv.URL+p, nil, nil); resp.StatusCode != 403 {
			t.Fatalf("member %s = %d", p, resp.StatusCode)
		}
	}
}

// Rows of a project's actions carry the project, so its Nhật ký tab shows them.
func TestAuditRowsCarryProject(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{"name": "Hook", "source": "webhook", "action": "chat", "prompt": "x"}, nil)
	aid := body["automation"].(map[string]any)["id"].(string)
	do(t, admin, "POST", e.srv.URL+"/api/automations/"+aid+"/run", map[string]any{}, nil)
	do(t, admin, "POST", e.srv.URL+"/api/automations/"+aid+"/rotate-secret", map[string]any{}, nil)
	do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations/test-script", map[string]any{"script": map[string]any{"lang": "bash", "body": "echo hi"}}, nil)
	rows, _ := e.st.Audit().List(context.Background(), storage.AuditFilter{Limit: 50})
	seen := map[string]bool{}
	for _, r := range rows {
		switch r.Action {
		case "automation.run", "automation.rotate", "automation.test_script":
			seen[r.Action] = true
			if r.ProjectID != pid {
				t.Errorf("%s project = %q, want %q", r.Action, r.ProjectID, pid)
			}
			if r.Action == "automation.test_script" && r.ResourceID == pid {
				t.Errorf("test_script resource_id is the project id")
			}
		}
	}
	if len(seen) != 3 {
		t.Fatalf("rows seen = %v", seen)
	}
}

// Review I7: in a group chat an approved diff is logged as the agent that
// wrote it, not the chat's default agent.
func TestAuditPatchAuthorInGroupChat(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, AgentName: "Lead"})
	m, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Content: "sửa xong", Author: "Dev"})
	p, err := e.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: c.ID, MessageID: m.ID, Diff: "not a diff", Files: []string{"a.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	do(t, admin, "POST", e.srv.URL+"/api/patches/"+p.ID+"/approve", map[string]any{}, nil)
	rows, _ := e.st.Audit().List(ctx, storage.AuditFilter{Resource: "patch"})
	if len(rows) != 1 || rows[0].ActorName != "Dev" || rows[0].ActorKind != "agent" {
		t.Fatalf("rows = %+v", rows)
	}
}
