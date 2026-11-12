package api_test

import (
	"net/url"
	"testing"
)

// ADR-172: a project defines a collection and keeps records in it; a record
// matching a unique field is patched; filters and history.
func TestCollectionsAPI(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	base := e.srv.URL + "/api/projects/" + pid + "/collections"

	resp, body := do(t, admin, "POST", base, map[string]any{"key": "customers", "name": "Khách hàng", "fields": []map[string]any{
		{"name": "name", "type": "text", "required": true}, {"name": "email", "type": "text", "unique": true},
		{"name": "tier", "type": "enum", "options": []string{"vip", "normal"}}}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("define = %d %v", resp.StatusCode, body)
	}
	cid := body["id"].(string)
	if resp, _ := do(t, admin, "POST", base, map[string]any{"key": "customers", "fields": []map[string]any{{"name": "a", "type": "text"}}}, nil); resp.StatusCode != 409 {
		t.Fatalf("a taken key = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", base, map[string]any{"key": "x", "fields": []map[string]any{{"name": "a", "type": "money"}}}, nil); resp.StatusCode != 400 {
		t.Fatalf("a bad type = %d", resp.StatusCode)
	}

	resp, body = do(t, admin, "POST", base+"/customers/records", map[string]any{"data": map[string]any{"name": "An", "email": "an@x.vn", "tier": "vip"}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("insert = %d %v", resp.StatusCode, body)
	}
	rid := body["id"].(string)
	resp, body = do(t, admin, "POST", base+"/customers/records", map[string]any{"data": map[string]any{"email": "an@x.vn", "tier": "normal"}}, nil)
	if resp.StatusCode != 200 || body["id"] != rid {
		t.Fatalf("upsert by email = %d %v", resp.StatusCode, body)
	}
	if resp, _ := do(t, admin, "POST", base+"/customers/records", map[string]any{"data": map[string]any{"name": "B", "tier": "gold"}}, nil); resp.StatusCode != 400 {
		t.Fatalf("a bad enum = %d", resp.StatusCode)
	}
	where := url.QueryEscape(`[{"field":"tier","op":"eq","value":"normal"}]`)
	_, body = do(t, admin, "GET", base+"/customers/records?where="+where, nil, nil)
	if body["total"].(float64) != 1 {
		t.Fatalf("filtered = %v", body)
	}
	_, body = do(t, admin, "GET", base+"/customers/records/"+rid+"/history", nil, nil)
	if len(body["history"].([]any)) != 2 {
		t.Fatalf("history = %v", body)
	}
	// a field added later
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/collections/"+cid, map[string]any{"fields": []map[string]any{
		{"name": "name", "type": "text", "required": true}, {"name": "email", "type": "text", "unique": true},
		{"name": "tier", "type": "enum", "options": []string{"vip", "normal"}}, {"name": "phone", "type": "text"}}}, nil)
	if resp.StatusCode != 200 || len(body["fields"].([]any)) != 4 || body["key"] != "customers" {
		t.Fatalf("update = %d %v", resp.StatusCode, body)
	}
	if resp, _ := do(t, admin, "DELETE", base+"/customers/records/"+rid, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	_, body = do(t, admin, "GET", base+"/customers/records", nil, nil)
	if body["total"].(float64) != 0 {
		t.Fatalf("after delete = %v", body)
	}
}
