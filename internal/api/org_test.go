package api_test

import (
	"context"
	"fmt"
	"github.com/tung1998/agent-office/internal/collections"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvidersAPI(t *testing.T) {
	e := setup(t)
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "x", "kind": "claude_cli"}, nil); resp.StatusCode != 403 {
		t.Fatalf("member create provider = %d", resp.StatusCode)
	}
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "Claude", "kind": "anthropic", "api_key": "sk-ant-secret-value-1234"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	p := body["provider"].(map[string]any)
	raw := strings.ToLower(toJSON(body))
	if strings.Contains(raw, "secret-value") || strings.Contains(raw, "api_key_enc") || p["api_key_hint"] != "…1234" || p["has_api_key"] != true || p["is_default"] != true {
		t.Fatalf("provider leaks key or wrong fields: %v", body)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "Bad", "kind": "anthropic"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("missing key = %d %v", resp.StatusCode, body)
	}
	resp, _ = do(t, member, "GET", e.srv.URL+"/api/providers", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("member list providers = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/provider-kinds", nil, nil)
	if resp.StatusCode != 200 || len(body["kinds"].([]any)) != 7 {
		t.Fatalf("kinds = %v", body)
	}
}

func TestReposAgentsAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	resp, body := do(t, admin, "GET", e.srv.URL+"/api/agent-library", nil, nil)
	if lib := body["agents"].([]any); resp.StatusCode != 200 || len(lib) < 100 || lib[0].(map[string]any)["instructions"] != nil {
		t.Fatalf("library = %d %v", resp.StatusCode, body)
	}
	if resp, body = do(t, admin, "GET", e.srv.URL+"/api/agent-library/agency/frontend-developer", nil, nil); resp.StatusCode != 200 || body["agent"].(map[string]any)["instructions"] == "" {
		t.Fatalf("library item = %d %v", resp.StatusCode, body)
	}
	if resp, _ = do(t, admin, "GET", e.srv.URL+"/api/agent-library/office/nope", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("missing library item = %d", resp.StatusCode)
	}

	dir := t.TempDir()
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir, "name": "shop"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create repo = %d %v", resp.StatusCode, body)
	}
	// a new project begins with the library's starter agent (ADR-166)
	repo := body["project"].(map[string]any)
	agents := repo["agents"].([]any)
	if len(agents) != 1 || agents[0].(map[string]any)["key"] != "assistant" || repo["default_agent_id"] != agents[0].(map[string]any)["id"] {
		t.Fatalf("repo agents = %v", repo)
	}
	pid := repo["id"].(string)
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "pack": "team"}, nil); resp.StatusCode != 400 {
		t.Fatalf("starter packs are gone, got %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir}, nil); resp.StatusCode != 409 {
		t.Fatalf("duplicate repo = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir + "/nope"}, nil); resp.StatusCode != 400 {
		t.Fatalf("missing path = %d", resp.StatusCode)
	}

	// edit an agent of the project
	engineer := agents[0].(map[string]any)
	engineer["name"] = "Kỹ sư backend"
	engineer["llm_model"] = "claude-sonnet-5"
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+engineer["id"].(string), stripID(engineer), nil)
	if resp.StatusCode != 200 || body["agent"].(map[string]any)["name"] != "Kỹ sư backend" {
		t.Fatalf("update agent = %d %v", resp.StatusCode, body)
	}
	// an invalid agent is rejected with problems
	engineer["version"] = body["agent"].(map[string]any)["version"] // edited from what was just saved
	engineer["key"] = "Bad Key"
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+engineer["id"].(string), stripID(engineer), nil)
	if resp.StatusCode != 400 || body["problems"] == nil {
		t.Fatalf("invalid agent = %d %v", resp.StatusCode, body)
	}
	// add an agent, make it the default
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/agents", map[string]any{"key": "reviewer", "name": "Reviewer", "model_tier": "fast"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create agent = %d %v", resp.StatusCode, body)
	}
	rid := body["agent"].(map[string]any)["id"].(string)
	if resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/default-agent", map[string]any{"agent_id": rid}, nil); resp.StatusCode != 204 {
		t.Fatalf("set default = %d", resp.StatusCode)
	}
	if _, body := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid, nil, nil); body["project"].(map[string]any)["default_agent_id"] != rid {
		t.Fatalf("default = %v", body["project"])
	}
	if resp, body := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/agents/export", nil, nil); resp.StatusCode != 200 || len(body["agents"].([]any)) != 2 {
		t.Fatalf("export = %d %v", resp.StatusCode, body)
	}
}

// Only an admin may set an agent's FullAccess/ExtraDirs (ADR-074); a member's
// save keeps them as they were. ExtraDirs must be absolute, existing paths.
func TestAgentFullAccessPermissionAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	agents := body["project"].(map[string]any)["agents"].([]any)
	agentID := agents[0].(map[string]any)["id"].(string)

	// a relative or missing directory is rejected
	_, agentBody := do(t, admin, "GET", e.srv.URL+"/api/agents/"+agentID, nil, nil)
	ag := agentBody["agent"].(map[string]any)
	ag["permissions"] = map[string]any{"extra_dirs": []string{"relative/path"}}
	if resp, body := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+agentID, stripID(ag), nil); resp.StatusCode != 400 {
		t.Fatalf("relative extra dir = %d %v", resp.StatusCode, body)
	}
	dir := t.TempDir()
	ag["permissions"] = map[string]any{"extra_dirs": []string{dir + "/nope"}}
	if resp, body := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+agentID, stripID(ag), nil); resp.StatusCode != 400 {
		t.Fatalf("missing extra dir = %d %v", resp.StatusCode, body)
	}
	// a real absolute directory, and full access, are accepted and stamped
	ag["permissions"] = map[string]any{"extra_dirs": []string{dir}, "full_access": true}
	resp, body := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+agentID, stripID(ag), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("valid extra dir = %d %v", resp.StatusCode, body)
	}
	perms := body["agent"].(map[string]any)["permissions"].(map[string]any)
	if perms["full_access_by"] != "admin@x.io" || perms["extra_dirs"].([]any)[0] != dir {
		t.Fatalf("permissions not saved = %v", perms)
	}

	// a member cannot even reach the route (admin-only), full access stays
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	ag2 := map[string]any{}
	for k, v := range ag {
		ag2[k] = v
	}
	ag2["permissions"] = map[string]any{"full_access": false, "extra_dirs": []string{}}
	ag2["version"] = body["agent"].(map[string]any)["version"]
	if resp, _ := do(t, member, "PATCH", e.srv.URL+"/api/agents/"+agentID, stripID(ag2), nil); resp.StatusCode != 403 {
		t.Fatalf("member patch agent = %d", resp.StatusCode)
	}
	_, again := do(t, admin, "GET", e.srv.URL+"/api/agents/"+agentID, nil, nil)
	if p := again["agent"].(map[string]any)["permissions"].(map[string]any); p["full_access"] != true {
		t.Fatalf("full access changed by a rejected request: %v", p)
	}
}

// ADR-074 security fix: an agent's extra read dirs are checked against the
// dangerous folders (perm.CheckExtraDir) — a credentials folder like ~/.ssh
// is always rejected, even for an admin.
func TestAgentExtraDirRejectsSensitiveDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ssh := home + "/.ssh"
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	agents := body["project"].(map[string]any)["agents"].([]any)
	agentID := agents[0].(map[string]any)["id"].(string)
	_, agentBody := do(t, admin, "GET", e.srv.URL+"/api/agents/"+agentID, nil, nil)
	ag := agentBody["agent"].(map[string]any)
	ag["permissions"] = map[string]any{"extra_dirs": []string{ssh}}
	if resp, body := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+agentID, stripID(ag), nil); resp.StatusCode != 400 {
		t.Fatalf("extra dir = ~/.ssh: %d %v", resp.StatusCode, body)
	}
}

func stripID(a map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		if k != "id" && k != "project_id" {
			out[k] = v
		}
	}
	return out
}

func TestFolderBrowserAPI(t *testing.T) {
	e := setup(t)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "shop", ".git"), 0o755)
	os.MkdirAll(filepath.Join(root, "notes"), 0o755)

	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "GET", e.srv.URL+"/api/fs/dirs?path="+url.QueryEscape(root), nil, nil); resp.StatusCode != 403 {
		t.Fatalf("member browse = %d", resp.StatusCode)
	}
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": filepath.Join(root, "shop")}, nil)

	resp, body := do(t, admin, "GET", e.srv.URL+"/api/fs/dirs?path="+url.QueryEscape(root), nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("browse = %d %v", resp.StatusCode, body)
	}
	entries := body["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", entries)
	}
	shop := entries[1].(map[string]any)
	if shop["name"] != "shop" || shop["is_project"] != true || shop["registered"] != true {
		t.Fatalf("shop = %v", shop)
	}
	if entries[0].(map[string]any)["registered"] != false || len(body["shortcuts"].([]any)) == 0 {
		t.Fatalf("body = %v", body)
	}
	if resp, _ := do(t, admin, "GET", e.srv.URL+"/api/fs/dirs?path="+url.QueryEscape(filepath.Join(root, "missing")), nil, nil); resp.StatusCode != 404 {
		t.Fatalf("missing = %d", resp.StatusCode)
	}
}

func TestCloneProjectAPI(t *testing.T) {
	e := setup(t)
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/projects/clone", map[string]any{"url": "https://github.com/a/b.git"}, nil); resp.StatusCode != 403 {
		t.Fatalf("member clone = %d", resp.StatusCode)
	}
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	parent := t.TempDir()
	for _, u := range []string{"", "/tmp/repo", "file:///tmp/repo", "--upload-pack=x"} {
		if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/clone", map[string]any{"url": u, "parent": parent}, nil); resp.StatusCode != 400 {
			t.Fatalf("clone %q = %d", u, resp.StatusCode)
		}
	}
	os.MkdirAll(filepath.Join(parent, "b"), 0o755)
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/clone", map[string]any{"url": "https://github.com/a/b.git", "parent": parent}, nil); resp.StatusCode != 409 {
		t.Fatalf("existing dir = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/clone", map[string]any{"url": "https://github.com/a/b.git", "parent": filepath.Join(parent, "missing")}, nil); resp.StatusCode != 400 {
		t.Fatalf("missing parent = %d", resp.StatusCode)
	}
	if _, body := do(t, admin, "GET", e.srv.URL+"/api/system", nil, nil); body["clone_root"] == "" || body["clone_root"] == nil {
		t.Fatalf("system = %v", body)
	}
}

func TestNewProjectAPI(t *testing.T) {
	e := setup(t)
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/projects/new", map[string]any{"idea": "shop", "name": "shop"}, nil); resp.StatusCode != 403 {
		t.Fatalf("member new = %d", resp.StatusCode)
	}
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	parent := t.TempDir()
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/new", map[string]any{"idea": "shop", "parent": parent}, nil); resp.StatusCode != 400 {
		t.Fatalf("no dir/name = %d", resp.StatusCode)
	}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/new", map[string]any{"idea": "a POD shop", "stack": "go", "name": "shop", "parent": parent}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("new = %d %v", resp.StatusCode, body)
	}
	p := body["project"].(map[string]any)
	if p["description"] != "a POD shop\nStack: go" {
		t.Fatalf("project = %v", p)
	}
	if b, err := e.st.Burn().Session(context.Background(), p["id"].(string)); err != nil || b.Template != "builder" || b.State != "stopped" {
		t.Fatalf("a project from an idea gets a builder Burn, not started: %+v %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(parent, "shop", ".git")); err != nil {
		t.Fatalf("no git repo: %v", err)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/new", map[string]any{"idea": "dup", "name": "shop", "parent": parent}, nil); resp.StatusCode != 409 {
		t.Fatalf("existing dir = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/new", map[string]any{"idea": "x", "name": "shop", "parent": filepath.Join(parent, "missing")}, nil); resp.StatusCode != 400 {
		t.Fatalf("missing parent = %d", resp.StatusCode)
	}
}

// ADR-199: a new company from the template shipped with office: the repo
// holds the template, committed; its pack made the project (the CEO alone,
// ADR-204); the goal given is its first goal.
func TestNewCompanyAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	parent := t.TempDir()
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/new", map[string]any{"template": "nope", "name": "acme", "parent": parent}, nil); resp.StatusCode != 400 {
		t.Fatalf("unknown template = %d", resp.StatusCode)
	}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/new", map[string]any{"template": "company", "name": "Acme", "dir": "acme", "parent": parent,
		"goal": "Doanh thu 1.000 USD/tháng trong 90 ngày"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("new company = %d %v", resp.StatusCode, body)
	}
	ctx := context.Background()
	pid := body["project"].(map[string]any)["id"].(string)
	if _, err := os.Stat(filepath.Join(parent, "acme", ".claude", "skills", "operations", "SKILL.md")); err != nil {
		t.Fatal("the template is not in the repo")
	}
	agents, _ := e.st.Agents().List(ctx, pid)
	r, _ := e.st.Repos().Get(ctx, pid)
	if def, ok := storage.DefaultAgent(r, agents); !ok || def.Key != "ceo" || len(agents) != 1 { // ADR-204: it makes the rest
		t.Fatalf("agents = %d, default %+v", len(agents), def)
	}
	goals, n, err := collections.New(e.st).Query(ctx, pid, "goals", collections.Query{})
	if err != nil || n != 1 || goals[0].Data["request"] != "Doanh thu 1.000 USD/tháng trong 90 ngày" { // ADR-213: the words as given
		t.Fatalf("goals = %d %+v %v", n, goals, err)
	}
	// ADR-209: one message is all it needs: its CEO runs as administrator in
	// the creating admin's name, and got the goal in a chat at Vận hành
	ceoA := agents[0]
	if !ceoA.Permissions.FullAccess || ceoA.Permissions.FullAccessBy != "admin@x.io" {
		t.Fatalf("ceo permissions = %+v", ceoA.Permissions)
	}
	convs, _ := e.st.Chat().ListConversations(ctx, pid, 10)
	var sent bool
	for _, c := range convs {
		msgs, _ := e.st.Chat().ListMessages(ctx, c.ID)
		sent = sent || c.Mode == perm.Operate && len(msgs) > 0 && msgs[0].Content == "#intake Doanh thu 1.000 USD/tháng trong 90 ngày" // ADR-213: through its workflow
	}
	if !sent {
		t.Fatalf("the goal never reached the CEO: %+v", convs)
	}
	// a unit pack of the library, by name
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/new", map[string]any{"template": "@agentoffice/startup/support", "name": "CSKH", "dir": "cskh", "parent": parent}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("new unit = %d %v", resp.StatusCode, body)
	}
	agents, _ = e.st.Agents().List(ctx, body["project"].(map[string]any)["id"].(string))
	if len(agents) != 3 {
		t.Fatalf("unit agents = %d", len(agents))
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/project-packs", nil, nil)
	if list, _ := body["packs"].([]any); len(list) < 7 {
		t.Fatalf("library = %v", body)
	}
	// ADR-210: the overview's activity of every project
	_, body = do(t, admin, "GET", e.srv.URL+"/api/activity", nil, nil)
	if list, _ := body["projects"].([]any); len(list) < 2 {
		t.Fatalf("activity = %v", body)
	}
	// ADR-206: the project knows its template, and comes back to it
	ceo, _ := e.st.Agents().Get(ctx, r.DefaultAgentID)
	if ceo.Key != "ceo" {
		agentsHQ, _ := e.st.Agents().List(ctx, pid)
		ceo = agentsHQ[0]
	}
	ceo.Instructions = "cũ"
	e.st.Agents().Update(ctx, ceo)
	sync := e.srv.URL + "/api/projects/" + pid + "/pack-sync"
	_, body = do(t, admin, "GET", sync, nil, nil)
	if body["ref"] != "@agentoffice/startup" || !strings.Contains(fmt.Sprint(body["changes"]), "update") {
		t.Fatalf("preview = %v", body)
	}
	if resp, body = do(t, admin, "POST", sync, map[string]any{"mode": "apply"}, nil); resp.StatusCode != 200 {
		t.Fatalf("apply = %d %v", resp.StatusCode, body)
	}
	if a, _ := e.st.Agents().Get(ctx, ceo.ID); a.Instructions == "cũ" {
		t.Fatal("the CEO did not come back to the template")
	}
}

func TestMachineWideProject(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": ""}, nil); resp.StatusCode != 400 {
		t.Fatalf("no path, no name = %d", resp.StatusCode)
	}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"name": "Trợ lý máy"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("helper = %d %v", resp.StatusCode, body)
	}
	p := body["project"].(map[string]any)
	if p["scope"] != "machine" || p["path"] != "" || p["exists"] != true {
		t.Fatalf("helper project = %v", p)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"name": "Trợ lý 2"}, nil); resp.StatusCode != 201 {
		t.Fatalf("second helper = %d", resp.StatusCode)
	}
}

func TestRevisionsAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	project := body["project"].(map[string]any)
	pid := project["id"].(string)
	agent := project["agents"].([]any)[0].(map[string]any)
	agent["name"] = "Trợ lý sửa"
	if resp, body := do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+agent["id"].(string), stripID(agent), nil); resp.StatusCode != 200 {
		t.Fatalf("update = %d %v", resp.StatusCode, body)
	}
	resp, body := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/revisions", nil, nil)
	revs := body["revisions"].([]any)
	if resp.StatusCode != 200 || len(revs) != 1 {
		t.Fatalf("revisions = %d %v", resp.StatusCode, body)
	}
	rev := revs[0].(map[string]any)
	if rev["actor"] != "human:admin@x.io" || rev["action"] != "agent.update:assistant" {
		t.Fatalf("rev = %v", rev)
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/revisions/"+rev["id"].(string), nil, nil)
	if resp.StatusCode != 200 || body["snapshot"].(map[string]any)["agents"].([]any)[0].(map[string]any)["name"] != "Trợ lý" {
		t.Fatalf("snapshot = %v", body)
	}
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/revisions/"+rev["id"].(string)+"/restore", map[string]any{}, nil); resp.StatusCode != 403 {
		t.Fatalf("member restore = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/revisions/"+rev["id"].(string)+"/restore", map[string]any{}, nil)
	if resp.StatusCode != 200 || body["project"].(map[string]any)["agents"].([]any)[0].(map[string]any)["name"] != "Trợ lý" {
		t.Fatalf("restore = %d %v", resp.StatusCode, body)
	}
}

func TestTransferAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "Claude", "kind": "anthropic", "api_key": "sk-ant-very-secret-1"}, nil)
	do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"name": "Trợ lý"}, nil)

	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "GET", e.srv.URL+"/api/transfer/export", nil, nil); resp.StatusCode != 403 {
		t.Fatalf("member export = %d", resp.StatusCode)
	}
	resp, bundle := do(t, admin, "GET", e.srv.URL+"/api/transfer/export", nil, nil)
	if resp.StatusCode != 200 || strings.Contains(toJSON(bundle), "very-secret") || len(bundle["projects"].([]any)) != 1 {
		t.Fatalf("export = %d %v", resp.StatusCode, bundle)
	}
	bundle["projects"].([]any)[0].(map[string]any)["description"] = "đổi mô tả"
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/transfer/import", map[string]any{"bundle": bundle, "dry_run": true}, nil)
	if resp.StatusCode != 200 || body["dry_run"] != true {
		t.Fatalf("dry run = %d %v", resp.StatusCode, body)
	}
	var projectOp string
	for _, c := range body["changes"].([]any) {
		if c.(map[string]any)["kind"] == "project" {
			projectOp = c.(map[string]any)["op"].(string)
		}
	}
	if projectOp != "update" {
		t.Fatalf("project op = %q in %v", projectOp, body)
	}
	_, projects := do(t, admin, "GET", e.srv.URL+"/api/projects", nil, nil)
	if projects["projects"].([]any)[0].(map[string]any)["description"] == "đổi mô tả" {
		t.Fatal("dry run wrote data")
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/transfer/import", map[string]any{"bundle": bundle}, nil); resp.StatusCode != 200 {
		t.Fatalf("import = %d", resp.StatusCode)
	}
	_, projects = do(t, admin, "GET", e.srv.URL+"/api/projects", nil, nil)
	if projects["projects"].([]any)[0].(map[string]any)["description"] != "đổi mô tả" {
		t.Fatal("import did not apply")
	}
}
