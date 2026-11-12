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
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
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
	patch := map[string]any{"key": ag["key"], "name": ag["name"], "permissions": ag["permissions"], "model_tier": ag["model_tier"], "instructions": "một", "version": av}
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

	// monitor
	mresp, b := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/monitors", map[string]any{"name": "Shop", "type": "container", "target": "shop"}, nil)
	if mresp.StatusCode != 201 {
		t.Fatalf("monitor create = %d %v", mresp.StatusCode, b)
	}
	mid, mv := b["id"].(string), b["version"].(string)
	if mv == "" {
		t.Fatal("no monitor version")
	}
	editMon := func(name, version string) int {
		resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/monitors/"+mid, map[string]any{"name": name, "type": "container", "target": "shop", "version": version}, nil)
		return resp.StatusCode
	}
	if code := editMon("Shop2", mv); code != 200 { // first form save with the version it opened with
		t.Fatalf("monitor save = %d", code)
	}
	if code := editMon("Shop3", mv); code != 409 { // second form, same stale version
		t.Fatalf("monitor stale save = %d", code)
	}
	if code := editMon("Shop4", ""); code != 200 { // no version (a tool, an agent): as before
		t.Fatalf("monitor no version = %d", code)
	}
	// a form opened before the pause keeps a version read before it
	_, b = do(t, admin, "GET", e.srv.URL+"/api/monitors?project="+pid, nil, nil)
	var mv2 string
	for _, x := range b["monitors"].([]any) {
		mm := x.(map[string]any)
		if mm["id"].(string) == mid {
			mv2 = mm["version"].(string)
		}
	}
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/monitors/"+mid, map[string]any{"enabled": false}, nil); resp.StatusCode != 200 {
		t.Fatalf("monitor pause = %d", resp.StatusCode)
	}
	if code := editMon("Shop5", mv2); code != 200 { // pausing (its own switch) must not stale the open form's version
		t.Fatalf("monitor save after pause = %d", code)
	}
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/monitors/"+mid, map[string]any{"ai_enabled": true}, nil); resp.StatusCode != 200 {
		t.Fatalf("monitor ai_enabled = %d", resp.StatusCode)
	}
	if code := editMon("Shop6", mv2); code != 409 { // ai_enabled is part of the hash: must stale an open form
		t.Fatalf("monitor save after ai_enabled change = %d", code)
	}
}
