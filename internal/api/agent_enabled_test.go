package api_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// The switch on the agent list: admin only, logged, and the agent's DTO says
// enabled; an open edit form (its version) stays valid across it.
func TestAgentEnabledSwitch(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "GET", e.srv.URL+"/api/templates", nil, nil)
	solo := ""
	for _, x := range body["templates"].([]any) {
		if m := x.(map[string]any); m["key"] == "solo" {
			solo = m["id"].(string)
		}
	}
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop", "template_id": solo}, nil)
	project := body["project"].(map[string]any)
	pid := project["id"].(string)
	agent := project["model"].(map[string]any)["agents"].([]any)[0].(map[string]any)
	id := agent["id"].(string)
	if agent["enabled"] != true {
		t.Fatalf("a new agent should be on: %v", agent["enabled"])
	}
	version := agent["version"]

	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "PATCH", e.srv.URL+"/api/agents/"+id+"/enabled", map[string]any{"enabled": false}, nil); resp.StatusCode != 403 {
		t.Fatalf("member = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+id+"/enabled", map[string]any{}, nil); resp.StatusCode != 400 {
		t.Fatalf("no enabled = %d", resp.StatusCode)
	}
	resp, body := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+id+"/enabled", map[string]any{"enabled": false}, nil)
	if resp.StatusCode != 200 || body["agent"].(map[string]any)["enabled"] != false {
		t.Fatalf("pause = %d %v", resp.StatusCode, body)
	}
	if body["agent"].(map[string]any)["version"] != version {
		t.Fatal("pausing changed the agent's version: an open edit form would be refused")
	}
	// the chat's list still has it (past messages keep their avatar), marked off
	_, body = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/chat/agents", nil, nil)
	if a := body["agents"].([]any)[0].(map[string]any); a["enabled"] != false {
		t.Fatalf("chat agents = %v", body)
	}
	// an edit that does not send enabled keeps it paused
	_, body = do(t, admin, "GET", e.srv.URL+"/api/agents/"+id, nil, nil)
	ag := body["agent"].(map[string]any)
	delete(ag, "enabled")
	ag["role"] = "khác"
	if resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+id, stripID(ag), nil); resp.StatusCode != 200 || b["agent"].(map[string]any)["enabled"] != false {
		t.Fatalf("edit = %d %v", resp.StatusCode, b)
	}
	rows, _ := e.st.Audit().List(context.Background(), storage.AuditFilter{Resource: "agent", ResourceID: id})
	found := false
	for _, r := range rows {
		if r.Action == "agent.enabled" && r.Before["enabled"] == true && r.After["enabled"] == false {
			found = true
		}
	}
	if !found {
		t.Fatalf("no agent.enabled audit row: %+v", rows)
	}
	if resp, body := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+id+"/enabled", map[string]any{"enabled": true}, nil); resp.StatusCode != 200 || body["agent"].(map[string]any)["enabled"] != true {
		t.Fatalf("resume = %d %v", resp.StatusCode, body)
	}
}
