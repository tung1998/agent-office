package api_test

import (
	"context"
	"testing"
	"time"
)

// A save carries the version it was edited from: someone else's change in
// between answers 409 (nothing overwritten); a run of the automation (its
// last run, failures) is not someone else's change.
func TestSaveConflicts(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "GET", e.srv.URL+"/api/templates", nil, nil)
	solo := ""
	for _, x := range body["templates"].([]any) {
		if m := x.(map[string]any); m["key"] == "solo" {
			solo = m["id"].(string)
		}
	}
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop", "template_id": solo}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	// automation
	_, b := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{"name": "Hook", "source": "webhook", "action": "chat", "prompt": "x"}, nil)
	a := b["automation"].(map[string]any)
	aid, v1 := a["id"].(string), a["version"].(string)
	if v1 == "" {
		t.Fatal("no version")
	}
	cur, _ := e.st.Automations().Get(ctx, aid) // a run: not an edit
	now := time.Now().UTC()
	cur.LastRunAt, cur.Failures = &now, 1
	e.st.Automations().Update(ctx, cur)
	edit := func(name, version string) int {
		resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/automations/"+aid, map[string]any{"name": name, "source": "webhook", "action": "chat", "prompt": "x", "enabled": true, "version": version}, nil)
		return resp.StatusCode
	}
	if code := edit("A", v1); code != 200 {
		t.Fatalf("after a run = %d", code)
	}
	if code := edit("B", v1); code != 409 { // A was saved since v1
		t.Fatalf("stale save = %d", code)
	}
	if code := edit("C", ""); code != 200 { // no version (a tool, an agent): as before
		t.Fatalf("no version = %d", code)
	}

	// agent
	_, b = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/chat/agents", nil, nil)
	agid := b["agents"].([]any)[0].(map[string]any)["id"].(string)
	_, b = do(t, admin, "GET", e.srv.URL+"/api/agents/"+agid, nil, nil)
	ag := b["agent"].(map[string]any)
	av := ag["version"].(string)
	patch := map[string]any{"key": ag["key"], "name": ag["name"], "tier": ag["tier"], "permissions": ag["permissions"], "model_tier": ag["model_tier"], "instructions": "một", "version": av}
	if resp, b := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+agid, patch, nil); resp.StatusCode != 200 {
		t.Fatalf("agent save = %d %v", resp.StatusCode, b)
	}
	patch["instructions"] = "hai"
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+agid, patch, nil); resp.StatusCode != 409 {
		t.Fatalf("agent stale save = %d", resp.StatusCode)
	}

	// policy
	_, b = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/policy", nil, nil)
	pv := b["version"].(string)
	pol := b["policy"].(map[string]any)
	pol["deny_paths"] = []string{"secret/"}
	pol["version"] = pv
	if resp, b := do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/policy", pol, nil); resp.StatusCode != 200 {
		t.Fatalf("policy save = %d %v", resp.StatusCode, b)
	}
	pol["deny_paths"] = []string{"other/"}
	if resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/policy", pol, nil); resp.StatusCode != 409 {
		t.Fatalf("policy stale save = %d", resp.StatusCode)
	}
}
