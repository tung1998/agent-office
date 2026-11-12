package api_test

import (
	"strings"
	"testing"
)

func TestBuiltinWorkflowTemplateIsImmutableAndProjectCopyIsIndependent(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	resp, body := do(t, admin, "GET", e.srv.URL+"/api/workflow-library/advisor", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get builtin = %d %v", resp.StatusCode, body)
	}
	template := body["workflow"].(map[string]any)
	if template["builtin"] != true {
		t.Fatalf("advisor is not marked built-in: %v", template)
	}
	source := template["source"].(string)
	changed := source + "\nEdited by an agent.\n"

	resp, body = do(t, admin, "PUT", e.srv.URL+"/api/workflow-library/advisor", map[string]any{"source": changed, "version": template["version"]}, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("edit builtin = %d %v, want 403", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "DELETE", e.srv.URL+"/api/workflow-library/advisor", nil, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("delete builtin = %d %v, want 403", resp.StatusCode, body)
	}

	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/workflows", map[string]any{"key": "advisor", "bindings": map[string]string{}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("add builtin to project = %d %v", resp.StatusCode, body)
	}
	projectWorkflow := body["workflow"].(map[string]any)
	if projectWorkflow["source"] != source {
		t.Fatal("project workflow was not copied from the code template")
	}

	projectCopy := strings.Replace(source, "Cố vấn", "Bản riêng của project", 1)
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/workflows/"+projectWorkflow["id"].(string), map[string]any{"source": projectCopy, "bindings": map[string]string{}, "version": projectWorkflow["version"]}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("edit project copy = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/workflow-library/advisor", nil, nil)
	if resp.StatusCode != 200 || body["workflow"].(map[string]any)["source"] != source {
		t.Fatalf("project edit changed builtin template: %d %v", resp.StatusCode, body)
	}
}

// ADR-072: a workflow's PATCH checks the version it was read with so two
// tabs editing the same project workflow don't silently overwrite each other.
func TestWorkflowUpdateConflict(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	src := "---\nkey: thu\nname: Thử\nroles:\n  - key: a\n---\nLàm đi.\n"
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/workflows", map[string]any{"source": src}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	wf := body["workflow"].(map[string]any)
	id := wf["id"].(string)
	v1 := wf["version"].(string)
	if v1 == "" {
		t.Fatalf("no version on create: %v", wf)
	}

	resp, body = do(t, admin, "GET", e.srv.URL+"/api/workflows/"+id, nil, nil)
	if resp.StatusCode != 200 || body["workflow"].(map[string]any)["version"].(string) != v1 {
		t.Fatalf("get = %d %v", resp.StatusCode, body)
	}

	// an empty version (agent/config-registry write) still saves
	src2 := "---\nkey: thu\nname: Thử 2\nroles:\n  - key: a\n---\nLàm đi.\n"
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/workflows/"+id, map[string]any{"source": src2}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("empty-version patch = %d %v", resp.StatusCode, body)
	}
	v2 := body["workflow"].(map[string]any)["version"].(string)
	if v2 == v1 {
		t.Fatalf("version did not change after source edit")
	}

	// toggling enabled must not change the version (reviewer note)
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/workflows/"+id, map[string]any{"enabled": false, "version": v2}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("toggle patch = %d %v", resp.StatusCode, body)
	}
	if body["workflow"].(map[string]any)["version"].(string) != v2 {
		t.Fatalf("version changed by toggling enabled")
	}

	// a stale version (second tab) is rejected with 409 and the row is unchanged
	src3 := "---\nkey: thu\nname: Thử 3\nroles:\n  - key: a\n---\nLàm đi.\n"
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/workflows/"+id, map[string]any{"source": src3, "version": v1}, nil)
	if resp.StatusCode != 409 || body["code"] != "conflict" {
		t.Fatalf("stale patch = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/workflows/"+id, nil, nil)
	if resp.StatusCode != 200 || body["workflow"].(map[string]any)["name"] != "Thử 2" {
		t.Fatalf("row changed by rejected patch: %v", body)
	}

	// the current version is accepted and returns a new one
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/workflows/"+id, map[string]any{"source": src3, "version": v2}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("current-version patch = %d %v", resp.StatusCode, body)
	}
	v3 := body["workflow"].(map[string]any)["version"].(string)
	if v3 == v2 {
		t.Fatalf("version did not change after accepted patch")
	}
}

// a workflow created with enabled:false is written disabled in one go (no
// Install/Create-then-Update), so an Update failure can't orphan it enabled.
func TestWorkflowCreateDisabled(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	src := "---\nkey: thu\nname: Thử\nroles:\n  - key: a\n---\nLàm đi.\n"
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/workflows", map[string]any{"source": src, "enabled": false}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	wf := body["workflow"].(map[string]any)
	if wf["enabled"].(bool) {
		t.Fatalf("workflow created enabled despite enabled:false: %v", wf)
	}
	id := wf["id"].(string)

	resp, body = do(t, admin, "GET", e.srv.URL+"/api/workflows/"+id, nil, nil)
	if resp.StatusCode != 200 || body["workflow"].(map[string]any)["enabled"].(bool) {
		t.Fatalf("get after create = %d %v", resp.StatusCode, body)
	}

	// no enabled field => defaults to enabled
	src2 := "---\nkey: thu2\nname: Thử 2\nroles:\n  - key: a\n---\nLàm đi.\n"
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/workflows", map[string]any{"source": src2}, nil)
	if resp.StatusCode != 201 || !body["workflow"].(map[string]any)["enabled"].(bool) {
		t.Fatalf("default create = %d %v", resp.StatusCode, body)
	}
}

// A create with both a key and a source (propose_change sends the key next
// to the file) takes the source, not the library workflow of that key.
func TestWorkflowCreateSourceWinsOverKey(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	src := "---\nkey: advisor\nname: Cố vấn riêng\nroles:\n  - key: a\n---\nLàm đi.\n"
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/workflows", map[string]any{"key": "advisor", "source": src}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	if wf := body["workflow"].(map[string]any); wf["name"] != "Cố vấn riêng" {
		t.Fatalf("library copy instead of the source: %v", wf["name"])
	}
}
