package api_test

import "testing"

// Monitors watch processes and containers only, one per process/service.
func TestMonitorTypesAndOnePerTarget(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	url := e.srv.URL + "/api/projects/" + pid + "/monitors"

	for _, typ := range []string{"http", "tcp", "heartbeat"} {
		if resp, _ := do(t, admin, "POST", url, map[string]any{"name": "x", "type": typ, "target": "localhost:1"}, nil); resp.StatusCode != 400 {
			t.Fatalf("%s monitor = %d, want 400", typ, resp.StatusCode)
		}
	}
	resp, body := do(t, admin, "POST", url, map[string]any{"type": "container", "target": "db"}, nil)
	if resp.StatusCode != 201 || body["name"] != "db" {
		t.Fatalf("container monitor = %d %v (named after its service)", resp.StatusCode, body)
	}
	if resp, _ := do(t, admin, "POST", url, map[string]any{"name": "db again", "type": "container", "target": "db"}, nil); resp.StatusCode != 400 {
		t.Fatalf("second monitor of a service = %d, want 400", resp.StatusCode)
	}
}
