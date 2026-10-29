package api_test

import "testing"

// A project turns the develop pack's features off: its payload says which are
// on (the dashboard hides the rest; agents and the API are not limited).
func TestProjectFeatures(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	base := e.srv.URL + "/api/projects/" + pid

	_, body = do(t, admin, "GET", base, nil, nil)
	if f := body["project"].(map[string]any)["features"].([]any); len(f) != 3 {
		t.Fatalf("unset: every feature on, got %v", f)
	}
	resp, body := do(t, admin, "PUT", base+"/features", map[string]any{"packs": map[string]any{"develop": true}, "off": []string{"ops"}}, nil)
	if resp.StatusCode != 200 || len(body["on"].([]any)) != 2 || body["off"].(map[string]any)["ops"] != "project" {
		t.Fatalf("save = %d %v", resp.StatusCode, body)
	}
	do(t, admin, "PUT", base+"/features", map[string]any{"packs": map[string]any{"develop": false}}, nil)
	_, body = do(t, admin, "GET", base, nil, nil)
	if f := body["project"].(map[string]any)["features"].([]any); len(f) != 0 {
		t.Fatalf("develop off: %v", f)
	}
}
