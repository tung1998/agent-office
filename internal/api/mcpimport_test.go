package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// One row of the machine MCP list: added to office as a copy (the row stays
// and reads as already in office), then turned off and on through the API
// (admin only, audited), the project's files back as they were.
func TestMCPRowAddAndToggleAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := e.st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: dir}); err != nil {
		t.Fatal(err)
	}
	mcpJSON := `{"mcpServers":{"docs_src":{"type":"http","url":"https://docs.example/mcp"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	candidate := func() map[string]any {
		_, body := do(t, admin, "GET", e.srv.URL+"/api/mcp/import", nil, nil)
		for _, c := range body["candidates"].([]any) {
			if c := c.(map[string]any); c["name"] == "docs_src" {
				return c
			}
		}
		t.Fatal("docs_src not a candidate")
		return nil
	}
	c := candidate()
	if c["movable"] != true || c["taken"] == true || c["moved_as"] != nil {
		t.Fatalf("before = %v", c)
	}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/mcp/import", map[string]any{"items": []any{map[string]any{"ref": c["ref"], "name": c["suggested"]}}}, nil)
	if resp.StatusCode != 200 || body["results"].([]any)[0].(map[string]any)["ok"] != true {
		t.Fatalf("add = %d %v", resp.StatusCode, body)
	}
	if c = candidate(); c["taken"] != true || c["moved_as"] != "docs-src" {
		t.Fatalf("after add = %v", c)
	}
	if list, _ := e.st.MCPServers().List(ctx); len(list) != 1 || list[0].Name != "docs-src" {
		t.Fatalf("office servers = %v", list)
	}

	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ = do(t, member, "POST", e.srv.URL+"/api/automation/mcp/enabled", map[string]any{"ref": c["ref"], "enabled": false}, nil); resp.StatusCode != 403 {
		t.Fatalf("member toggle = %d", resp.StatusCode)
	}
	if resp, body = do(t, admin, "POST", e.srv.URL+"/api/automation/mcp/enabled", map[string]any{"ref": c["ref"], "enabled": false}, nil); resp.StatusCode != 200 {
		t.Fatalf("off = %d %v", resp.StatusCode, body)
	}
	if c = candidate(); c["disabled"] != true {
		t.Fatalf("not off: %v", c)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.local.json")); !strings.Contains(string(raw), "docs_src") {
		t.Fatalf("settings.local.json = %s", raw)
	}
	if resp, body = do(t, admin, "POST", e.srv.URL+"/api/automation/mcp/enabled", map[string]any{"ref": c["ref"], "enabled": true}, nil); resp.StatusCode != 200 {
		t.Fatalf("on = %d %v", resp.StatusCode, body)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, ".mcp.json")); string(raw) != mcpJSON {
		t.Fatalf(".mcp.json = %s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); !os.IsNotExist(err) {
		t.Fatal(".claude left behind")
	}
	rows, _ := e.st.Audit().List(ctx, storage.AuditFilter{Resource: "automation", ResourceID: "mcp:docs_src"})
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d", len(rows))
	}
	// a source without a switch
	ref := c["ref"].(map[string]any)
	ref["type"] = "cursor"
	if resp, _ = do(t, admin, "POST", e.srv.URL+"/api/automation/mcp/enabled", map[string]any{"ref": ref, "enabled": false}, nil); resp.StatusCode == 200 {
		t.Fatal("toggled a server that is not there")
	}
}

// "Đăng nhập qua office" on a machine MCP row: the server is copied into
// office (the row stays), then its login starts. An Atlassian-like server
// (no resource metadata, RFC 8414 at the origin, registration) gives a login
// URL; one without a login server, or without registration, gives the error
// the dashboard shows (needs_client: it opens the form).
func TestMCPRowLoginViaOfficeAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	var as *httptest.Server
	var noDCR atomic.Bool
	as = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			meta := map[string]any{"issuer": as.URL, "authorization_endpoint": as.URL + "/v1/authorize", "token_endpoint": as.URL + "/v1/token",
				"code_challenge_methods_supported": []string{"plain", "S256"}}
			if !noDCR.Load() {
				meta["registration_endpoint"] = as.URL + "/v1/register"
			}
			json.NewEncoder(w).Encode(meta)
		case "/v1/mcp":
			w.Header().Set("WWW-Authenticate", `Bearer realm="OAuth", error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
		case "/v1/register":
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"client_id": "atl-client", "token_endpoint_auth_method": "none"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer as.Close()
	bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer bare.Close()
	dir := t.TempDir()
	if _, err := e.st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: dir}); err != nil {
		t.Fatal(err)
	}
	mcpJSON := `{"mcpServers":{"atlassian":{"type":"http","url":"` + as.URL + `/v1/mcp"},"bare":{"type":"http","url":"` + bare.URL + `/mcp"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	_, body := do(t, admin, "GET", e.srv.URL+"/api/mcp/import", nil, nil)
	refs := map[string]any{}
	for _, c := range body["candidates"].([]any) {
		c := c.(map[string]any)
		refs[c["name"].(string)] = c["ref"]
	}
	copyIn := func(name string) string {
		t.Helper()
		resp, body := do(t, admin, "POST", e.srv.URL+"/api/mcp/import", map[string]any{"take_out": false,
			"items": []any{map[string]any{"ref": refs[name], "name": name}}}, nil)
		res := body["results"].([]any)[0].(map[string]any)
		if resp.StatusCode != 200 || res["ok"] != true || res["taken_out"] == true {
			t.Fatalf("copy %s = %d %v", name, resp.StatusCode, body)
		}
		return res["server"].(map[string]any)["id"].(string)
	}
	start := func(id string) (int, map[string]any) {
		t.Helper()
		resp, body := do(t, admin, "POST", e.srv.URL+"/api/mcp/servers/"+id+"/oauth/start", map[string]any{"origin": e.srv.URL}, nil)
		return resp.StatusCode, body
	}

	id := copyIn("atlassian")
	if raw, _ := os.ReadFile(filepath.Join(dir, ".mcp.json")); string(raw) != mcpJSON {
		t.Fatalf("the row left the file: %s", raw)
	}
	code, body := start(id)
	u, _ := body["url"].(string)
	if code != 200 || !strings.HasPrefix(u, as.URL+"/v1/authorize?") || !strings.Contains(u, "client_id=atl-client") ||
		!strings.Contains(u, url.QueryEscape(e.srv.URL+"/api/mcp/oauth/callback")) {
		t.Fatalf("start = %d %v", code, body)
	}

	if code, body = start(copyIn("bare")); code != 502 || !strings.Contains(body["error"].(string), "không tìm thấy máy chủ đăng nhập") || body["code"] != nil {
		t.Fatalf("no login server = %d %v", code, body)
	}

	noDCR.Store(true)
	m, _ := e.st.MCPServers().Get(ctx, id)
	_ = e.st.MCPServers().SetOAuth(ctx, m.ID, "") // discover again
	if code, body = start(id); code != 502 || body["code"] != "needs_client" || !strings.Contains(body["error"].(string), "client_id") {
		t.Fatalf("no registration = %d %v", code, body)
	}
}

// ADR-093: a project's .mcp.json server moves into office (its secret into
// the encrypted store, out of the file with a copy in trash), is given to
// some agents, logs its calls, and goes back on request.
func TestMCPImportAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	up := upstreamMCP(t)
	dir := t.TempDir()
	if _, err := e.st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: dir}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OFFICE_TEST_MCP_TOKEN", strings.TrimPrefix(mcpSecret, "Bearer "))
	mcpJSON := `{"mcpServers":{"Docs_Server":{"type":"http","url":"` + up.URL + `","headers":{"Authorization":"Bearer ${OFFICE_TEST_MCP_TOKEN}"}},` +
		`"old":{"type":"sse","url":"https://x.example/sse"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcpJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, admin, "GET", e.srv.URL+"/api/mcp/import", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("candidates = %d %v", resp.StatusCode, body)
	}
	var docs map[string]any
	for _, c := range body["candidates"].([]any) {
		c := c.(map[string]any)
		switch c["name"] {
		case "Docs_Server":
			docs = c
		case "old":
			if c["problem"] != "sse" {
				t.Fatalf("sse candidate = %v", c)
			}
		}
	}
	if docs == nil || docs["suggested"] != "docs-server" || docs["movable"] != true || docs["target"] != up.URL {
		t.Fatalf("docs candidate = %v", docs)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/mcp/import", map[string]any{"take_out": true,
		"items": []any{map[string]any{"ref": docs["ref"], "name": "docs"}}}, nil)
	res := body["results"].([]any)[0].(map[string]any)
	if resp.StatusCode != 200 || res["ok"] != true || res["taken_out"] != true {
		t.Fatalf("import = %d %v", resp.StatusCode, body)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "very-secret") {
		t.Fatalf("import answer carries the secret: %s", raw)
	}
	left, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if strings.Contains(string(left), "Docs_Server") || !strings.Contains(string(left), "old") {
		t.Fatalf(".mcp.json after = %s", left)
	}
	srv := res["server"].(map[string]any)
	id := srv["id"].(string)
	m, _ := e.st.MCPServers().Get(ctx, id)
	if m.Origin != "mcp.json" || m.URL != up.URL || !strings.Contains(m.OriginRef, "Docs_Server") || !strings.Contains(m.OriginRef, "trash") ||
		strings.Contains(m.OriginRef, "OFFICE_TEST_MCP_TOKEN") {
		t.Fatalf("stored = %+v", m)
	}
	if from := srv["moved_from"].(map[string]any); from["name"] != "Docs_Server" || from["backup"] != true {
		t.Fatalf("moved_from = %v", from)
	}
	// the ${VAR} was filled in: the stored secret works
	if resp, body = do(t, admin, "POST", e.srv.URL+"/api/mcp/servers/"+id+"/check", nil, nil); body["server"].(map[string]any)["last_check_status"] != "ok" {
		t.Fatalf("check after import = %v", body)
	}
	if resp, body = do(t, admin, "POST", e.srv.URL+"/api/mcp/import", map[string]any{"items": []any{map[string]any{"ref": docs["ref"], "name": "docs"}}}, nil); resp.StatusCode != 200 {
		t.Fatalf("import again = %d", resp.StatusCode)
	} else if r := body["results"].([]any)[0].(map[string]any); r["ok"] == true {
		t.Fatalf("a server no longer in the file imported again: %v", r)
	}

	// given to one agent, one tool trusted
	resp, body = do(t, e.srv.Client(), "GET", e.srv.URL+"/api/mcp/agents", nil, nil)
	if resp.StatusCode == 200 {
		t.Fatal("agents list without login")
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/mcp/agents", nil, nil)
	if resp.StatusCode != 200 || len(body["agents"].([]any)) == 0 {
		t.Fatalf("agents = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/mcp/servers/"+id, map[string]any{"agents": []string{"agt_x", " agt_x ", ""}, "trusted_tools": []string{"resolve-library-id"}}, nil)
	if s := body["server"].(map[string]any); resp.StatusCode != 200 || len(s["agents"].([]any)) != 1 || len(s["trusted_tools"].([]any)) != 1 {
		t.Fatalf("assign = %d %v", resp.StatusCode, body)
	}

	// the call log
	_ = e.st.MCPCalls().Add(ctx, storage.MCPCall{ServerID: id, ServerName: "docs", Tool: "resolve-library-id", Caller: "Dev", CallerKind: "claude", Status: "ok"})
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/mcp/servers/"+id+"/calls", nil, nil)
	if calls := body["calls"].([]any); resp.StatusCode != 200 || len(calls) != 1 || calls[0].(map[string]any)["caller"] != "Dev" {
		t.Fatalf("calls = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/mcp/servers", nil, nil)
	if st := body["stats"].(map[string]any)[id].(map[string]any); st["calls"] != float64(1) {
		t.Fatalf("stats = %v", body["stats"])
	}

	// back to .mcp.json, and out of office
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/mcp/servers/"+id+"/put-back", map[string]any{"delete": true}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("put back = %d %v", resp.StatusCode, body)
	}
	back, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	// as it was: the ${VAR}, never the secret office filled in
	if !strings.Contains(string(back), "Docs_Server") || !strings.Contains(string(back), "${OFFICE_TEST_MCP_TOKEN}") || strings.Contains(string(back), "very-secret") {
		t.Fatalf(".mcp.json after put back = %s", back)
	}
	if _, err := e.st.MCPServers().Get(ctx, id); err == nil {
		t.Fatal("still in office after put back with delete")
	}
}
