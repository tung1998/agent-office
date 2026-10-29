package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/storage"
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
	for _, p := range []string{"/api/peers", "/api/audit", "/api/mcp/servers"} {
		if resp, _ := do(t, pm, "GET", e.srv.URL+p, nil, nil); resp.StatusCode != 403 {
			t.Fatalf("%s = %d", p, resp.StatusCode)
		}
	}

	// what needs it on the overview, and the bots a notice goes by: its own projects only
	if resp, b := do(t, pm, "GET", e.srv.URL+"/api/bots", nil, nil); resp.StatusCode != 200 || len(b["bots"].([]any)) != 0 {
		t.Fatalf("bots = %d %v", resp.StatusCode, b)
	}
	if resp, _ := do(t, pm, "POST", e.srv.URL+"/api/incidents/dismiss", map[string]any{"key": "monitor:mon_x"}, nil); resp.StatusCode != 404 {
		t.Fatalf("dismiss another's = %d", resp.StatusCode)
	}
	if resp, _ := do(t, pm, "POST", e.srv.URL+"/api/incidents/retry", map[string]any{"kind": "automation", "id": "aut_x"}, nil); resp.StatusCode != 404 {
		t.Fatalf("retry another's = %d", resp.StatusCode)
	}
	if resp, _ := do(t, pm, "POST", e.srv.URL+"/api/proposals/skip-all", map[string]any{"project_id": otherID}, nil); resp.StatusCode != 404 {
		t.Fatalf("skip another's = %d", resp.StatusCode)
	}
	if resp, b := do(t, pm, "POST", e.srv.URL+"/api/proposals/skip-all", map[string]any{}, nil); resp.StatusCode != 200 {
		t.Fatalf("skip its own = %d %v", resp.StatusCode, b)
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

// ADR-239: a manager runs the member accounts, giving them only what it has:
// its projects, and the other offices (whole or some projects) an admin gave it.
func TestManagerRunsMembers(t *testing.T) {
	ctx := context.Background()
	a, b := setup(t), setup(t)
	shop, _ := b.st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	blog, _ := b.st.Repos().Create(ctx, storage.Repo{Name: "blog", Path: t.TempDir()})
	ca, cb := a.client(t), b.client(t)
	login(t, a, ca, "admin@x.io", "admin-password")
	login(t, b, cb, "admin@x.io", "admin-password")
	_, out := do(t, cb, "POST", b.srv.URL+"/api/peers/invite", map[string]any{"name": "office-b", "url": b.srv.URL, "shared": []string{shop.ID, blog.ID}}, nil)
	_, out = do(t, ca, "POST", a.srv.URL+"/api/peers/join", map[string]any{"code": out["code"], "name": "office-a", "url": a.srv.URL}, nil)
	pb := out["id"].(string)

	resp, out := do(t, ca, "POST", a.srv.URL+"/api/users", map[string]any{"email": "pm@x.io", "role": "manager", "password": "manager-password",
		"offices": map[string]any{pb: []string{shop.ID}}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create manager = %d %v", resp.StatusCode, out)
	}
	pm := a.client(t)
	login(t, a, pm, "pm@x.io", "manager-password")
	_, out = do(t, pm, "POST", a.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "mine"}, nil)
	mine := out["project"].(map[string]any)["id"].(string)
	// made after its login (the test login gives it every project there is)
	_, out = do(t, ca, "POST", a.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "admins"}, nil)
	adminsID := out["project"].(map[string]any)["id"].(string)

	// links: only the other office's projects it was given, only projects it sees
	shopRef, blogRef := peer.RefPrefix+pb+"/"+shop.ID, peer.RefPrefix+pb+"/"+blog.ID
	_, out = do(t, pm, "GET", a.srv.URL+"/api/projects/"+mine+"/policy", nil, nil)
	if got := toJSON(out["offices"]); !strings.Contains(got, shopRef) || strings.Contains(got, blogRef) {
		t.Fatalf("manager's offices = %s", got)
	}
	for ref, want := range map[string]int{blogRef: 400, adminsID: 400, shopRef: 200} {
		if resp, out := do(t, pm, "PUT", a.srv.URL+"/api/projects/"+mine+"/policy", map[string]any{"linked_projects": []string{ref}}, nil); resp.StatusCode != want {
			t.Fatalf("link %s = %d %v", ref, resp.StatusCode, out)
		}
	}

	// it makes a member: only a member, only with what it has; a whole office it has in part is cut down
	if resp, _ := do(t, pm, "POST", a.srv.URL+"/api/users", map[string]any{"email": "m2@x.io", "role": "manager", "password": "member-password"}, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("manager makes a manager = %d", resp.StatusCode)
	}
	if resp, _ := do(t, pm, "POST", a.srv.URL+"/api/users", map[string]any{"email": "m2@x.io", "role": "member", "password": "member-password", "projects": []string{adminsID}}, nil); resp.StatusCode != 400 {
		t.Fatalf("gives a project not its own = %d", resp.StatusCode)
	}
	if resp, _ := do(t, pm, "POST", a.srv.URL+"/api/users", map[string]any{"email": "m2@x.io", "role": "member", "password": "member-password", "offices": map[string]any{pb: []string{blog.ID}}}, nil); resp.StatusCode != 400 {
		t.Fatalf("gives an office project not its own = %d", resp.StatusCode)
	}
	resp, out = do(t, pm, "POST", a.srv.URL+"/api/users", map[string]any{"email": "m2@x.io", "role": "member", "password": "member-password",
		"projects": []string{mine}, "offices": map[string]any{pb: []string{"*"}}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("manager makes a member = %d %v", resp.StatusCode, out)
	}
	m2 := out["user"].(map[string]any)
	if got := toJSON(m2["offices"]); got != `{"`+pb+`":["`+shop.ID+`"]}` {
		t.Fatalf("member's offices = %s", got)
	}
	m2ID := m2["id"].(string)

	// the admin gives the member its project too: the manager's change keeps it
	do(t, ca, "PATCH", a.srv.URL+"/api/users/"+m2ID, map[string]any{"projects": []string{mine, adminsID}}, nil)
	if resp, out := do(t, pm, "PATCH", a.srv.URL+"/api/users/"+m2ID, map[string]any{"projects": []string{}}, nil); resp.StatusCode != 200 || toJSON(out["user"].(map[string]any)["projects"]) != `["`+adminsID+`"]` {
		t.Fatalf("manager takes its project back = %d %v", resp.StatusCode, out)
	}

	// it sees the members only (theirs cut to what it gives), and runs no one above
	_, out = do(t, pm, "GET", a.srv.URL+"/api/users", nil, nil)
	if list := out["users"].([]any); len(list) != 2 { // the setup's member and m2
		t.Fatalf("manager's users = %v", list)
	}
	_, me := do(t, ca, "GET", a.srv.URL+"/api/auth/me", nil, nil)
	adminID := me["user"].(map[string]any)["id"].(string)
	for _, c := range []struct {
		path string
		body map[string]any
		want int
	}{
		{"/api/users/" + adminID, map[string]any{"disabled": true}, 403},
		{"/api/users/" + m2ID, map[string]any{"role": "manager"}, 403},
		{"/api/users/" + m2ID, map[string]any{"disabled": true}, 200},
	} {
		if resp, _ := do(t, pm, "PATCH", a.srv.URL+c.path, c.body, nil); resp.StatusCode != c.want {
			t.Fatalf("PATCH %s %v = %d", c.path, c.body, resp.StatusCode)
		}
	}
	if resp, _ := do(t, pm, "POST", a.srv.URL+"/api/users/"+adminID+"/reset-password", map[string]any{"password": "another-password"}, nil); resp.StatusCode != 403 {
		t.Fatalf("reset an admin's password = %d", resp.StatusCode)
	}
	if resp, _ := do(t, pm, "POST", a.srv.URL+"/api/users/"+m2ID+"/reset-password", map[string]any{"password": "another-password"}, nil); resp.StatusCode != 204 {
		t.Fatalf("reset a member's password = %d", resp.StatusCode)
	}
}
