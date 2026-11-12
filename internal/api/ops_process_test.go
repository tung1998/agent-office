package api_test

import (
	"testing"
)

// updateProcess rejects a save made from a stale read (ADR-072 version
// check): if autostart changed underneath a form opened before that, saving
// the form answers 409 and does not revert the toggle.
func TestUpdateProcessVersionConflict(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create project = %d %v", resp.StatusCode, body)
	}
	pid := body["project"].(map[string]any)["id"].(string)

	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/processes", map[string]any{
		"name": "web", "command": "npm start", "cwd": ".",
	}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create process = %d %v", resp.StatusCode, body)
	}
	id := body["id"].(string)
	version := body["version"].(string)
	if version == "" {
		t.Fatalf("no version in create response: %v", body)
	}

	// toggleAutostart: a single-field PATCH, no version, must not be refused.
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/processes/"+id, map[string]any{"autostart": true}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("toggle autostart = %d %v", resp.StatusCode, body)
	}

	// saveForm: a full save built from the pre-toggle snapshot's version.
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/processes/"+id, map[string]any{
		"name": "web", "command": "npm run dev", "cwd": ".", "autostart": false, "version": version,
	}, nil)
	if resp.StatusCode != 409 || body["code"] != "conflict" {
		t.Fatalf("stale save = %d %v, want 409 conflict", resp.StatusCode, body)
	}

	resp, body = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/processes", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list = %d %v", resp.StatusCode, body)
	}
	list := body["processes"].([]any)
	p := list[0].(map[string]any)
	if p["autostart"] != true {
		t.Fatalf("autostart toggle was reverted by the stale save: %v", p)
	}
	if p["command"] != "npm start" {
		t.Fatalf("stale command landed despite the conflict: %v", p)
	}
}

// updateProcess without a version (a tool, an agent) is not checked and
// still runs; a rename to a name already used in the project is a plain
// conflict, kept distinct from the version/CAS conflict above.
func TestUpdateProcessNameConflict(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create project = %d %v", resp.StatusCode, body)
	}
	pid := body["project"].(map[string]any)["id"].(string)

	do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/processes", map[string]any{"name": "web", "command": "npm start", "cwd": "."}, nil)
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/processes", map[string]any{"name": "worker", "command": "npm run worker", "cwd": "."}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create process = %d %v", resp.StatusCode, body)
	}
	id := body["id"].(string)

	// no version: a bare rename still runs (a tool/agent call, not a form save).
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/processes/"+id, map[string]any{"name": "web"}, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("rename to a taken name = %d %v, want 409", resp.StatusCode, body)
	}
	if body["error"] == "Đã có thay đổi mới ở nơi khác kể từ lúc bạn mở (người khác hoặc agent). Tải lại để xem bản mới rồi sửa lại." {
		t.Fatalf("name conflict surfaced as a stale-save conflict: %v", body)
	}
}
