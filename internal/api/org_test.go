package api_test

import (
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
	if resp.StatusCode != 200 || len(body["kinds"].([]any)) != 5 {
		t.Fatalf("kinds = %v", body)
	}
}

func TestTemplatesReposAgentsAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	resp, body := do(t, admin, "GET", e.srv.URL+"/api/templates", nil, nil)
	tpls := body["templates"].([]any)
	if resp.StatusCode != 200 || len(tpls) != 3 {
		t.Fatalf("templates = %d %v", resp.StatusCode, body)
	}
	ids := map[string]string{}
	for _, x := range tpls {
		m := x.(map[string]any)
		ids[m["key"].(string)] = m["id"].(string)
	}

	dir := t.TempDir()
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir, "name": "shop", "template_id": ids["team"]}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create repo = %d %v", resp.StatusCode, body)
	}
	repo := body["project"].(map[string]any)
	model := repo["model"].(map[string]any)
	if model["kind"] != "team" || model["is_template"] != false || len(model["agents"].([]any)) != 9 {
		t.Fatalf("repo model = %v", model)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir}, nil); resp.StatusCode != 409 {
		t.Fatalf("duplicate repo = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir + "/nope"}, nil); resp.StatusCode != 400 {
		t.Fatalf("missing path = %d", resp.StatusCode)
	}

	// edit an agent of the repo's model
	var engineer map[string]any
	for _, a := range model["agents"].([]any) {
		if a.(map[string]any)["key"] == "engineer" {
			engineer = a.(map[string]any)
		}
	}
	engineer["name"] = "Kỹ sư backend"
	engineer["llm_model"] = "claude-sonnet-5"
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+engineer["id"].(string), stripID(engineer), nil)
	if resp.StatusCode != 200 || body["agent"].(map[string]any)["name"] != "Kỹ sư backend" {
		t.Fatalf("update agent = %d %v", resp.StatusCode, body)
	}
	// invalid structure is rejected with problems
	engineer["version"] = body["agent"].(map[string]any)["version"] // edited from what was just saved
	engineer["reports_to"] = []string{"ghost"}
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+engineer["id"].(string), stripID(engineer), nil)
	if resp.StatusCode != 400 || body["problems"] == nil {
		t.Fatalf("invalid agent = %d %v", resp.StatusCode, body)
	}
	// template unchanged
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/org-models/"+ids["team"], nil, nil)
	for _, a := range body["model"].(map[string]any)["agents"].([]any) {
		if a.(map[string]any)["name"] == "Kỹ sư backend" {
			t.Fatal("template modified by repo edit")
		}
	}
	// replace model with council
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/"+repo["id"].(string)+"/model", map[string]any{"template_id": ids["council"]}, nil); resp.StatusCode != 409 {
		t.Fatalf("apply without replace = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+repo["id"].(string)+"/model", map[string]any{"template_id": ids["council"], "replace": true}, nil)
	if resp.StatusCode != 200 || body["project"].(map[string]any)["model"].(map[string]any)["kind"] != "council" {
		t.Fatalf("replace = %d %v", resp.StatusCode, body)
	}
	// clone a template, delete built-in refused
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/templates", map[string]any{"source_id": ids["solo"], "key": "solo-vi", "name": "Solo tiếng Việt"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("clone = %d %v", resp.StatusCode, body)
	}
	if resp, _ := do(t, admin, "DELETE", e.srv.URL+"/api/org-models/"+ids["solo"], nil, nil); resp.StatusCode != 400 {
		t.Fatalf("delete builtin = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "DELETE", e.srv.URL+"/api/org-models/"+body["model"].(map[string]any)["id"].(string), nil, nil); resp.StatusCode != 204 {
		t.Fatalf("delete clone = %d", resp.StatusCode)
	}
	if resp, body := do(t, admin, "GET", e.srv.URL+"/api/org-models/"+ids["team"]+"/export", nil, nil); resp.StatusCode != 200 || body["key"] != "team" {
		t.Fatalf("export = %d %v", resp.StatusCode, body)
	}
}

// Only an admin may set an agent's FullAccess/ExtraDirs (ADR-074); a member's
// save keeps them as they were. ExtraDirs must be absolute, existing paths.
func TestAgentFullAccessPermissionAPI(t *testing.T) {
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
	agents := body["project"].(map[string]any)["model"].(map[string]any)["agents"].([]any)
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
	_, body := do(t, admin, "GET", e.srv.URL+"/api/templates", nil, nil)
	solo := ""
	for _, x := range body["templates"].([]any) {
		if m := x.(map[string]any); m["key"] == "solo" {
			solo = m["id"].(string)
		}
	}
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop", "template_id": solo}, nil)
	agents := body["project"].(map[string]any)["model"].(map[string]any)["agents"].([]any)
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
		if k != "id" && k != "org_model_id" {
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
	_, body := do(t, admin, "GET", e.srv.URL+"/api/templates", nil, nil)
	var soloID string
	for _, x := range body["templates"].([]any) {
		if x.(map[string]any)["key"] == "solo" {
			soloID = x.(map[string]any)["id"].(string)
		}
	}
	do(t, admin, "PATCH", e.srv.URL+"/api/org-models/"+soloID, map[string]any{"name": "Solo sửa"}, nil)
	resp, body := do(t, admin, "GET", e.srv.URL+"/api/org-models/"+soloID+"/revisions", nil, nil)
	revs := body["revisions"].([]any)
	if resp.StatusCode != 200 || len(revs) != 1 {
		t.Fatalf("revisions = %d %v", resp.StatusCode, body)
	}
	rev := revs[0].(map[string]any)
	if rev["actor"] != "human:admin@x.io" || rev["action"] != "model.update" {
		t.Fatalf("rev = %v", rev)
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/revisions/"+rev["id"].(string), nil, nil)
	if resp.StatusCode != 200 || body["snapshot"].(map[string]any)["name"] != "Solo" {
		t.Fatalf("snapshot = %v", body)
	}
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/revisions/"+rev["id"].(string)+"/restore", map[string]any{}, nil); resp.StatusCode != 403 {
		t.Fatalf("member restore = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/revisions/"+rev["id"].(string)+"/restore", map[string]any{}, nil)
	if resp.StatusCode != 200 || body["model"].(map[string]any)["name"] != "Solo" {
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
