package api_test

import (
	"testing"
)

// ADR-224: a manager sees and manages the projects it made or was given;
// the whole office's settings stay the admin's.
func TestProjectScope(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/users", map[string]any{"email": "pm@x.io", "role": "manager", "password": "manager-password"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create manager = %d %v", resp.StatusCode, body)
	}
	pmID := body["user"].(map[string]any)["id"].(string)
	pm := e.client(t)
	login(t, e, pm, "pm@x.io", "manager-password") // no project yet: none given

	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "other"}, nil)
	other := body["project"].(map[string]any)
	otherID := other["id"].(string)
	otherAgent := other["agents"].([]any)[0].(map[string]any)["id"].(string)

	resp, body = do(t, pm, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "mine"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("manager makes a project = %d %v", resp.StatusCode, body)
	}
	mine := body["project"].(map[string]any)
	mineAgent := mine["agents"].([]any)[0].(map[string]any)["id"].(string)

	_, body = do(t, pm, "GET", e.srv.URL+"/api/projects", nil, nil)
	if list := body["projects"].([]any); len(list) != 1 || list[0].(map[string]any)["name"] != "mine" {
		t.Fatalf("manager's projects = %v", list)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/projects", nil, nil)
	if list := body["projects"].([]any); len(list) != 2 {
		t.Fatalf("admin's projects = %d", len(list))
	}

	// another's project and its things are not found; its own are managed
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/projects/" + otherID},
		{"GET", "/api/agents/" + otherAgent},
		{"PATCH", "/api/agents/" + otherAgent + "/enabled"},
		{"GET", "/api/usage/runs?project=" + otherID},
	} {
		if resp, _ := do(t, pm, c.method, e.srv.URL+c.path, map[string]any{"enabled": false}, nil); resp.StatusCode != 404 {
			t.Fatalf("%s %s = %d", c.method, c.path, resp.StatusCode)
		}
	}
	if _, b := do(t, pm, "GET", e.srv.URL+"/api/jobs?project="+otherID, nil, nil); len(b["jobs"].([]any)) != 0 {
		t.Fatalf("other's jobs = %v", b["jobs"])
	}
	if resp, body := do(t, pm, "PATCH", e.srv.URL+"/api/agents/"+mineAgent+"/enabled", map[string]any{"enabled": false}, nil); resp.StatusCode != 200 {
		t.Fatalf("own agent = %d %v", resp.StatusCode, body)
	}
	// the office's own settings: the admin's
	for _, p := range []string{"/api/users", "/api/audit", "/api/mcp/servers"} {
		if resp, _ := do(t, pm, "GET", e.srv.URL+p, nil, nil); resp.StatusCode != 403 {
			t.Fatalf("%s = %d", p, resp.StatusCode)
		}
	}

	// the admin gives it the other project: now it sees and manages it
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/users/"+pmID, map[string]any{"projects": []string{mine["id"].(string), otherID}}, nil)
	if resp.StatusCode != 200 || len(body["user"].(map[string]any)["projects"].([]any)) != 2 {
		t.Fatalf("give project = %d %v", resp.StatusCode, body)
	}
	if resp, _ := do(t, pm, "PATCH", e.srv.URL+"/api/agents/"+otherAgent+"/enabled", map[string]any{"enabled": false}, nil); resp.StatusCode != 200 {
		t.Fatalf("given project's agent = %d", resp.StatusCode)
	}
	_, body = do(t, pm, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	if got := body["user"].(map[string]any)["projects"].([]any); len(got) != 2 {
		t.Fatalf("me projects = %v", got)
	}

	// an admin cannot demote itself; an unknown project is refused
	_, me := do(t, admin, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	adminID := me["user"].(map[string]any)["id"].(string)
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/users/"+adminID, map[string]any{"role": "manager"}, nil); resp.StatusCode != 400 {
		t.Fatalf("self demote = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/users/"+pmID, map[string]any{"projects": []string{"rep_nope"}}, nil); resp.StatusCode != 400 {
		t.Fatalf("unknown project = %d", resp.StatusCode)
	}
	// a member is a reader of its projects: no admin route, even there
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/users/"+pmID, map[string]any{"role": "member"}, nil); resp.StatusCode != 200 {
		t.Fatalf("to member = %d", resp.StatusCode)
	}
	login(t, e, pm, "pm@x.io", "manager-password") // its sessions ended with the role
	if resp, _ := do(t, pm, "PATCH", e.srv.URL+"/api/agents/"+mineAgent+"/enabled", map[string]any{"enabled": true}, nil); resp.StatusCode != 403 {
		t.Fatalf("member on its project = %d", resp.StatusCode)
	}
}
