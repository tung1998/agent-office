package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// ADR-250: "Kiểm tất cả" checks every office MCP server; one that is broken
// shows in an admin's "Cần xử lý" once per break, can be let go and retried.
func TestMCPHealthAPI(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	base := e.srv.URL + "/api/mcp/servers"

	resp, body := do(t, admin, "POST", base, map[string]any{"name": "local", "kind": "stdio", "command": "no-such-mcp-cmd"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	id := body["server"].(map[string]any)["id"].(string)

	if resp, _ := do(t, member, "POST", base+"/check-all", nil, nil); resp.StatusCode != 403 {
		t.Fatalf("member check-all = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "POST", base+"/check-all", nil, nil)
	if resp.StatusCode != 200 || body["checked"] != float64(1) || body["broken"] != float64(1) {
		t.Fatalf("check-all = %d %v", resp.StatusCode, body)
	}
	var srv map[string]any
	for _, x := range body["servers"].([]any) {
		if m := x.(map[string]any); m["id"] == id {
			srv = m
		}
	}
	if srv == nil || srv["last_check_status"] != "error" || srv["broken_since"] == nil {
		t.Fatalf("server after check-all = %v", srv)
	}

	find := func(c *http.Client) map[string]any {
		_, body := do(t, c, "GET", e.srv.URL+"/api/incidents", nil, nil)
		for _, x := range body["incidents"].([]any) {
			if it := x.(map[string]any); it["kind"] == "mcp" {
				return it
			}
		}
		return nil
	}
	it := find(admin)
	if it == nil || it["title"] != "local" || it["id"] != id || it["link"] != "/mcp" || !strings.Contains(it["detail"].(string), "máy chưa có lệnh") {
		t.Fatalf("admin incident = %v", it)
	}
	if got := find(member); got != nil {
		t.Fatalf("a member sees the office's MCP: %v", got)
	}
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/incidents/retry", map[string]any{"kind": "mcp", "id": id}, nil); resp.StatusCode != 403 && resp.StatusCode != 404 {
		t.Fatalf("member retry = %d", resp.StatusCode)
	}
	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/incidents/retry", map[string]any{"kind": "mcp", "id": id}, nil); resp.StatusCode != 200 {
		t.Fatalf("admin retry = %d %v", resp.StatusCode, body)
	}
	if got := find(admin); got == nil || got["key"] != it["key"] {
		t.Fatalf("still broken, same break: %v (was %v)", got, it)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/incidents/dismiss", map[string]any{"key": it["key"]}, nil); resp.StatusCode != 204 {
		t.Fatalf("dismiss = %d", resp.StatusCode)
	}
	if got := find(admin); got != nil {
		t.Fatalf("let go but still shown: %v", got)
	}
}
