package api_test

import (
	"testing"
)

// An agent's notes are seen and kept by people on its page (admins change
// them); compacted and restored; the project decides whether agents' own
// notes wait for approval.
func TestMemoryAPI(t *testing.T) {
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
	pid := body["project"].(map[string]any)["id"].(string)
	_, b := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/chat/agents", nil, nil)
	aid := b["agents"].([]any)[0].(map[string]any)["id"].(string)
	base := e.srv.URL + "/api/projects/" + pid + "/agents/" + aid + "/memories"

	if resp, _ := do(t, admin, "POST", base, map[string]any{"text": "Repo dùng pnpm"}, nil); resp.StatusCode != 201 {
		t.Fatalf("add = %d", resp.StatusCode)
	}
	do(t, admin, "POST", base, map[string]any{"text": "Chạy pnpm test trước khi báo xong"}, nil)
	_, b = do(t, admin, "GET", base, nil, nil)
	items := b["items"].([]any)
	if len(items) != 2 || b["auto"] != false {
		t.Fatalf("list = %v", b)
	}
	first := items[0].(map[string]any)["id"].(string)
	if resp, _ := do(t, admin, "PATCH", e.srv.URL+"/api/memories/"+first, map[string]any{"text": "Repo dùng pnpm 9"}, nil); resp.StatusCode != 200 {
		t.Fatalf("edit = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", base+"/compact", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("compact = %d", resp.StatusCode)
	}
	_, b = do(t, admin, "GET", base, nil, nil)
	if items = b["items"].([]any); len(items) != 1 || items[0].(map[string]any)["text"] != "gộp 2 ghi nhớ" {
		t.Fatalf("after compact = %v", b)
	}
	revs := b["revisions"].([]any)
	if len(revs) != 1 || revs[0].(map[string]any)["count"] != float64(2) {
		t.Fatalf("revisions = %v", revs)
	}
	rid := revs[0].(map[string]any)["id"].(string)
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/memory-revisions/"+rid+"/restore", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("restore = %d", resp.StatusCode)
	}
	_, b = do(t, admin, "GET", base, nil, nil)
	if items = b["items"].([]any); len(items) != 2 || items[0].(map[string]any)["text"] != "Repo dùng pnpm 9" {
		t.Fatalf("restored = %v", b)
	}
	id := items[1].(map[string]any)["id"].(string)
	if resp, _ := do(t, admin, "DELETE", e.srv.URL+"/api/memories/"+id, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/memory-settings", map[string]any{"auto": true}, nil); resp.StatusCode != 200 {
		t.Fatalf("settings = %d", resp.StatusCode)
	}
	_, b = do(t, admin, "GET", base, nil, nil)
	if len(b["items"].([]any)) != 1 || b["auto"] != true {
		t.Fatalf("after delete and auto = %v", b)
	}
	if resp, _ := do(t, admin, "POST", base, map[string]any{"text": "  "}, nil); resp.StatusCode != 400 {
		t.Fatalf("empty = %d", resp.StatusCode)
	}
}
