package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/orgmodel"
)

const mcpSecret = "Bearer sk-upstream-very-secret-9876"

// upstreamMCP answers initialize / tools/list / tools/call when given mcpSecret.
func upstreamMCP(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != mcpSecret {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		if len(m.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := map[string]any{}
		switch m.Method {
		case "tools/list":
			result["tools"] = []any{map[string]any{"name": "resolve-library-id", "description": "Resolve", "annotations": map[string]any{"readOnlyHint": true}}}
		case "tools/call":
			result["content"] = []any{map[string]any{"type": "text", "text": "ok from upstream"}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ADR-091: the office's MCP servers are managed by admins; their secrets
// never come back; a run's (or a person's) token reaches them at /mcp/s/<name>.
func TestMCPServersAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	up := upstreamMCP(t)
	base := e.srv.URL + "/api/mcp/servers"

	if resp, _ := do(t, member, "GET", base, nil, nil); resp.StatusCode != 403 {
		t.Fatalf("member list = %d", resp.StatusCode)
	}
	for _, bad := range []map[string]any{
		{"name": "Bad Name", "url": up.URL},
		{"name": "office", "url": up.URL},
		{"name": "x", "url": "ftp://x"},
		{"name": "x", "url": up.URL, "kind": "stdio"},
		{"name": "x", "url": up.URL, "scope": "project:abc"},
	} {
		if resp, body := do(t, admin, "POST", base, bad, nil); resp.StatusCode != 400 {
			t.Fatalf("create %v = %d %v", bad, resp.StatusCode, body)
		}
	}
	resp, body := do(t, admin, "POST", base, map[string]any{"name": "context7", "url": up.URL, "headers": map[string]string{"Authorization": mcpSecret}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	srv := body["server"].(map[string]any)
	id := srv["id"].(string)
	if h := srv["headers"].(map[string]any)["Authorization"]; h != "••••9876" || srv["path"] != "/mcp/s/context7" {
		t.Fatalf("created = %v", srv)
	}
	if resp, _ := do(t, admin, "POST", base, map[string]any{"name": "context7", "url": up.URL}, nil); resp.StatusCode != 409 {
		t.Fatalf("duplicate = %d", resp.StatusCode)
	}

	resp, body = do(t, admin, "POST", base+"/"+id+"/check", nil, nil)
	srv, _ = body["server"].(map[string]any)
	if resp.StatusCode != 200 || srv["last_check_status"] != "ok" || len(srv["tools"].([]any)) != 1 {
		t.Fatalf("check = %d %v", resp.StatusCode, body)
	}
	// an empty value keeps the stored secret
	resp, body = do(t, admin, "PATCH", base+"/"+id, map[string]any{"headers": map[string]string{"Authorization": ""}, "enabled": true}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("patch = %d %v", resp.StatusCode, body)
	}
	if _, body = do(t, admin, "POST", base+"/"+id+"/check", nil, nil); body["server"].(map[string]any)["last_check_status"] != "ok" {
		t.Fatalf("check after keep = %v", body)
	}

	// no response nor change log ever holds the secret
	_, list := do(t, admin, "GET", base, nil, nil)
	_, logs := do(t, admin, "GET", e.srv.URL+"/api/audit?limit=50", nil, nil)
	for _, v := range []any{list, logs} {
		if strings.Contains(mustJSON(v), "very-secret") {
			t.Fatalf("a secret leaked: %s", mustJSON(v))
		}
	}
	if !strings.Contains(mustJSON(logs), "mcp_server.create") {
		t.Fatalf("no change log: %s", mustJSON(logs))
	}

	// the gateway: a wrong token is refused, a personal token (ADR-047) goes through
	callTool := func(token, tool string) (int, string) {
		req, _ := http.NewRequest("POST", e.srv.URL+"/mcp/s/context7", bytes.NewBufferString(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"`+tool+`","arguments":{}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	call := func(token string) (int, string) { return callTool(token, "resolve-library-id") }
	if code, _ := call("wrong"); code != 401 {
		t.Fatalf("wrong token = %d", code)
	}
	if _, err := assistant.Ensure(context.Background(), e.st, orgmodel.NewService(e.st), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	_, tb := do(t, admin, "POST", e.srv.URL+"/api/me/tokens", map[string]any{"name": "cli"}, nil)
	if code, out := call(tb["token"].(string)); code != 200 || !strings.Contains(out, "ok from upstream") {
		t.Fatalf("personal token = %d %s", code, out)
	}
	// a tool not known to only read: an admin's token calls it, a member's
	// reads but never writes with the server's credentials without an approval
	if code, out := callTool(tb["token"].(string), "write-thing"); code != 200 || !strings.Contains(out, "ok from upstream") {
		t.Fatalf("admin token writes = %d %s", code, out)
	}
	_, mt := do(t, member, "POST", e.srv.URL+"/api/me/tokens", map[string]any{"name": "cli"}, nil)
	if code, out := call(mt["token"].(string)); code != 200 || !strings.Contains(out, "ok from upstream") {
		t.Fatalf("member token reads = %d %s", code, out)
	}
	if code, out := callTool(mt["token"].(string), "write-thing"); code != 200 || strings.Contains(out, "ok from upstream") || !strings.Contains(out, "đề xuất") && !strings.Contains(out, "chỉ được đọc") {
		t.Fatalf("member token writes = %d %s", code, out)
	}
	batch := `[{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"write-thing","arguments":{}}}]`
	req, _ := http.NewRequest("POST", e.srv.URL+"/mcp/s/context7", bytes.NewBufferString(batch))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mt["token"].(string))
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 400 {
		t.Fatalf("member batch = %v %v", r, err)
	} else {
		r.Body.Close()
	}

	if resp, _ := do(t, admin, "DELETE", base+"/"+id, nil, nil); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if code, _ := call(tb["token"].(string)); code != 404 {
		t.Fatalf("deleted server = %d", code)
	}
}

// ADR-092: stdio servers (env sealed like headers) and an OAuth client typed
// on the form; the login's callback refuses a state it did not give out.
func TestMCPServersStdioOAuthAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	base := e.srv.URL + "/api/mcp/servers"

	resp, body := do(t, admin, "POST", base, map[string]any{"name": "local", "kind": "stdio", "command": "no-such-mcp-cmd",
		"args": []string{"-y", " pkg ", ""}, "env": map[string]string{"API_KEY": "env-very-secret-1234"}}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create stdio = %d %v", resp.StatusCode, body)
	}
	srv := body["server"].(map[string]any)
	if srv["command"] != "no-such-mcp-cmd" || mustJSON(srv["args"]) != `["-y","pkg"]` || srv["env"].(map[string]any)["API_KEY"] != "••••1234" {
		t.Fatalf("stdio = %v", srv)
	}
	_, body = do(t, admin, "POST", base+"/"+srv["id"].(string)+"/check", nil, nil)
	if s := body["server"].(map[string]any); s["last_check_status"] != "error" || !strings.Contains(s["last_check_error"].(string), "máy chưa có lệnh no-such-mcp-cmd") {
		t.Fatalf("check stdio = %v", body)
	}

	up := upstreamMCP(t)
	_, body = do(t, admin, "POST", base, map[string]any{"name": "remote", "url": up.URL, "oauth_client_id": "my-client", "oauth_client_secret": "cs-very-secret-5555"}, nil)
	srv = body["server"].(map[string]any)
	id := srv["id"].(string)
	oa, _ := srv["oauth"].(map[string]any)
	if oa == nil || oa["client_manual"] != true || oa["client_id"] != "my-client" || oa["has_secret"] != true || oa["logged_in"] != false {
		t.Fatalf("typed client = %v", srv)
	}
	// an empty secret keeps the stored one; an empty client id clears it
	_, body = do(t, admin, "PATCH", base+"/"+id, map[string]any{"oauth_client_id": "my-client", "oauth_client_secret": ""}, nil)
	if oa = body["server"].(map[string]any)["oauth"].(map[string]any); oa["has_secret"] != true {
		t.Fatalf("keep secret = %v", oa)
	}

	if resp, _ := do(t, admin, "POST", base+"/"+id+"/oauth/start", map[string]any{"origin": "http://evil.example"}, nil); resp.StatusCode != 400 {
		t.Fatalf("foreign origin = %d", resp.StatusCode)
	}
	cb := e.srv.URL + "/api/mcp/oauth/callback?state=made-up&code=c"
	if r, err := member.Get(cb); err != nil || r.StatusCode != 403 {
		t.Fatalf("member callback = %v %v", r, err)
	}
	r, err := admin.Get(cb)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 400 || !strings.Contains(string(page), "không hợp lệ") || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("bad state = %d %s", r.StatusCode, page)
	}
	// the pasted address of a localhost callback
	finish := e.srv.URL + "/api/mcp/oauth/finish"
	if resp, _ := do(t, member, "POST", finish, map[string]any{"url": "http://localhost:1/x?state=s&code=c"}, nil); resp.StatusCode != 403 {
		t.Fatalf("member paste = %d", resp.StatusCode)
	}
	if resp, body := do(t, admin, "POST", finish, map[string]any{"url": "http://localhost:1/x?code=c"}, nil); resp.StatusCode != 400 || !strings.Contains(mustJSON(body), "state=") {
		t.Fatalf("paste without state = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, admin, "POST", finish, map[string]any{"url": " " + cb + " "}, nil); resp.StatusCode != 400 || !strings.Contains(mustJSON(body), "không hợp lệ") {
		t.Fatalf("paste bad state = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "POST", base+"/"+id+"/oauth/logout", nil, nil)
	if resp.StatusCode != 200 || body["server"].(map[string]any)["last_check_status"] != "needs_login" {
		t.Fatalf("logout = %d %v", resp.StatusCode, body)
	}

	_, list := do(t, admin, "GET", base, nil, nil)
	_, logs := do(t, admin, "GET", e.srv.URL+"/api/audit?limit=50", nil, nil)
	for _, v := range []any{list, logs} {
		if s := mustJSON(v); strings.Contains(s, "very-secret") || strings.Contains(s, "oauth_enc") || strings.Contains(s, "access_token") {
			t.Fatalf("a secret leaked: %s", s)
		}
	}
}
