package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/activity"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func TestMCP(t *testing.T) {
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	ctx := context.Background()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	other, _ := st.Repos().Create(ctx, storage.Repo{Name: "q", Path: t.TempDir()})
	st.Monitors().Create(ctx, storage.Monitor{ProjectID: p.ID, Name: "web", Type: "container", Target: "web", IntervalS: 60, Enabled: true, Status: "down", LastMessage: "HTTP 500"})
	st.Monitors().Create(ctx, storage.Monitor{ProjectID: other.ID, Name: "secret", Type: "container", Target: "secret", IntervalS: 60, Enabled: true})

	s := New(officetools.New(st, nil, nil), "test")
	srv := httptest.NewServer(s)
	defer srv.Close()
	tok, revoke := s.Grant(officetools.Scope{ProjectID: p.ID}, time.Minute)

	call := func(token, body string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, _ := call("", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	_, init := call(tok, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)
	if init["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatal(init)
	}
	if code, _ := call(tok, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
		t.Fatalf("notification: %d", code)
	}
	_, list := call(tok, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if n := len(list["result"].(map[string]any)["tools"].([]any)); n != 10 { // + read_link, search_history, staff_load
		t.Fatalf("tools: %d", n)
	}
	_, ov := call(tok, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ops_overview","arguments":{}}}`)
	text := ov["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "web") || !strings.Contains(text, "HTTP 500") || strings.Contains(text, "secret") {
		t.Fatalf("overview must show only this project: %s", text)
	}
	_, md := call(tok, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"monitor_detail","arguments":{"name":"secret"}}}`)
	if md["result"].(map[string]any)["isError"] != true {
		t.Fatal("other project's monitor must not be readable")
	}
	revoke()
	if code, _ := call(tok, `{"jsonrpc":"2.0","id":5,"method":"tools/list"}`); code != 401 {
		t.Fatalf("revoked: %d", code)
	}
}

// TestReadOnlyHint checks that readOnlyHint in the wire format mirrors each
// Tool's ReadOnly field. This toolbox has no actions/config/burn wired in, so
// tools/list here only returns the read-only subset (ops_overview and
// friends, plus the office tools since the scope is Office); the full set of
// tools, including the write ones, is covered by
// officetools.TestToolsReadOnly.

func TestReadOnlyHint(t *testing.T) {
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	ctx := context.Background()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})

	readOnly := map[string]bool{
		"projects": true, "jobs_query": true, "usage_summary": true, "handoff": true,
		"ops_overview": true, "process_logs": true, "container_logs": true, "monitor_detail": true,
		"git_status": true, "git_diff": true, "git_log": true,
		"describe": true, "list": true, "get": true,
		"search_history": true, "read_link": true, "burn_list": true, "staff_load": true,
	}

	tb := officetools.New(st, nil, nil)
	s := New(tb, "test")
	srv := httptest.NewServer(s)
	defer srv.Close()
	tok, revoke := s.Grant(officetools.Scope{ProjectID: p.ID, Level: perm.Propose, Office: true}, time.Minute)
	defer revoke()

	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	tools := out["result"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("no tools returned")
	}
	for _, raw := range tools {
		tl := raw.(map[string]any)
		name := tl["name"].(string)
		want := readOnly[name]
		got := tl["annotations"].(map[string]any)["readOnlyHint"].(bool)
		if got != want {
			t.Errorf("tool %q: readOnlyHint=%v, want %v", name, got, want)
		}
	}
}

// panicProvider is a Provider whose tool always panics, to check that a bad
// tool handler fails only that call instead of killing the connection.
type panicProvider struct{}

func (panicProvider) Tools(sc officetools.Scope) []officetools.Tool {
	return []officetools.Tool{{Name: "boom", Description: "panics"}}
}

func (panicProvider) Call(ctx context.Context, sc officetools.Scope, name string, raw json.RawMessage) (string, bool) {
	var m map[string]string
	m["x"] = "nil map write panics" // nolint:staticcheck
	return "", false
}

func TestToolPanicRecovered(t *testing.T) {
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	ctx := context.Background()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})

	tb := officetools.New(st, nil, nil)
	tb.AddProvider(panicProvider{})
	s := New(tb, "test")
	srv := httptest.NewServer(s)
	defer srv.Close()
	tok, revoke := s.Grant(officetools.Scope{ProjectID: p.ID}, time.Minute)
	defer revoke()

	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"boom","arguments":{}}}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connection died instead of returning an MCP error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("response body not valid JSON (connection likely reset): %v", err)
	}
	res, ok := out["result"].(map[string]any)
	if !ok || res["isError"] != true {
		t.Fatalf("want isError:true result, got %v", out)
	}
}

// A 2026-07-28 client opens with no initialize: version in each request's
// _meta, server/discover for what office speaks; wrong versions, mirrored
// headers that disagree and unknown methods answer with modern errors.
func TestMCPModern(t *testing.T) {
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	ctx := context.Background()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	s := New(officetools.New(st, nil, nil), "test")
	srv := httptest.NewServer(s)
	defer srv.Close()
	tok, revoke := s.Grant(officetools.Scope{ProjectID: p.ID}, time.Minute)
	defer revoke()

	call := func(method, params string, headers map[string]string) (int, map[string]any) {
		body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
		req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	meta := func(v string) string {
		return `{"_meta":{"io.modelcontextprotocol/protocolVersion":"` + v + `"}}`
	}
	h := func(method string) map[string]string {
		return map[string]string{"Mcp-Protocol-Version": "2026-07-28", "Mcp-Method": method}
	}

	code, d := call("server/discover", meta("2026-07-28"), h("server/discover"))
	res, _ := d["result"].(map[string]any)
	if code != 200 || res["resultType"] != "complete" || res["supportedVersions"].([]any)[0] != "2026-07-28" {
		t.Fatalf("discover: %d %v", code, d)
	}
	if res["_meta"].(map[string]any)["io.modelcontextprotocol/serverInfo"] == nil {
		t.Fatalf("discover without serverInfo: %v", d)
	}
	code, l := call("tools/list", meta("2026-07-28"), h("tools/list"))
	res, _ = l["result"].(map[string]any)
	if code != 200 || res["resultType"] != "complete" || res["cacheScope"] != "private" || len(res["tools"].([]any)) == 0 {
		t.Fatalf("tools/list: %d %v", code, l)
	}
	call1 := `{"name":"ops_overview","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`
	hc := h("tools/call")
	hc["Mcp-Name"] = "=?base64?b3BzX292ZXJ2aWV3?=" // ops_overview, encoded
	if code, c := call("tools/call", call1, hc); code != 200 || c["result"].(map[string]any)["resultType"] != "complete" {
		t.Fatalf("tools/call: %d %v", code, c)
	}

	code, u := call("tools/list", meta("2099-01-01"), nil)
	e, _ := u["error"].(map[string]any)
	if code != 400 || e["code"] != float64(-32022) || e["data"].(map[string]any)["requested"] != "2099-01-01" {
		t.Fatalf("unsupported version: %d %v", code, u)
	}
	hc["Mcp-Name"] = "monitor_detail"
	if code, m := call("tools/call", call1, hc); code != 400 || m["error"].(map[string]any)["code"] != float64(-32020) {
		t.Fatalf("header mismatch: %d %v", code, m)
	}
	if code, m := call("prompts/list", meta("2026-07-28"), h("prompts/list")); code != 404 || m["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("unknown method: %d %v", code, m)
	}
	code, rl := call("resources/list", meta("2026-07-28"), h("resources/list"))
	res, _ = rl["result"].(map[string]any)
	if code != 200 || res["resultType"] != "complete" || res["cacheScope"] != "private" {
		t.Fatalf("resources/list: %d %v", code, rl)
	}
	// legacy initialize asking for a version office does not speak gets its newest legacy one
	if _, i := call("initialize", `{"protocolVersion":"2026-07-28"}`, nil); i["result"].(map[string]any)["protocolVersion"] != "2025-11-25" {
		t.Fatalf("initialize: %v", i)
	}
}

// resourcePanicProvider stands in for project_report to check that a panic
// reached through resources/read fails only that call, like tools/call does.
type resourcePanicProvider struct{}

func (resourcePanicProvider) Tools(sc officetools.Scope) []officetools.Tool {
	return []officetools.Tool{{Name: "project_report", ReadOnly: true}}
}

func (resourcePanicProvider) Call(ctx context.Context, sc officetools.Scope, name string, raw json.RawMessage) (string, bool) {
	var m map[string]string
	m["x"] = "nil map write panics" // nolint:staticcheck
	return "", false
}

// resourceErrProvider stands in for burn_list returning isError=true, to
// check resources/read turns that into an rpcError instead of handing the
// error text to the client as if it were the resource's content.
type resourceErrProvider struct{}

func (resourceErrProvider) Tools(sc officetools.Scope) []officetools.Tool {
	return []officetools.Tool{{Name: "burn_list", ReadOnly: true}}
}

func (resourceErrProvider) Call(ctx context.Context, sc officetools.Scope, name string, raw json.RawMessage) (string, bool) {
	return "burn chưa bật", true
}

func TestResourceReadPanicAndError(t *testing.T) {
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	ctx := context.Background()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})

	tb := officetools.New(st, nil, nil)
	tb.AddProvider(resourcePanicProvider{})
	tb.AddProvider(resourceErrProvider{})
	s := New(tb, "test")
	srv := httptest.NewServer(s)
	defer srv.Close()
	tok, revoke := s.Grant(officetools.Scope{ProjectID: p.ID}, time.Minute)
	defer revoke()

	post := func(body string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("connection died instead of returning an MCP error: %v", err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("response body not valid JSON (connection likely reset): %v", err)
		}
		return resp.StatusCode, out
	}

	reportURI := "office://project/" + p.ID + "/report"
	code, out := post(`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"` + reportURI + `"}}`)
	if code != http.StatusOK || out["error"] == nil {
		t.Fatalf("panic in resource tool must become an rpcError: %d %v", code, out)
	}

	burnURI := "office://project/" + p.ID + "/burn"
	code, out = post(`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"` + burnURI + `"}}`)
	if code != http.StatusOK || out["error"] == nil || out["result"] != nil {
		t.Fatalf("a failed resource tool must become an rpcError, not resource content: %d %v", code, out)
	}

	// the server must still answer the next request on the same connection
	code, out = post(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if code != http.StatusOK || out["error"] != nil {
		t.Fatalf("server did not survive the panic: %d %v", code, out)
	}
}

// resources/list surfaces the project's report, and resources/read returns
// its text; a token for another project must not reach it either.
func TestResources(t *testing.T) {
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	ctx := context.Background()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	tb := officetools.New(st, nil, nil)
	tb.AddProvider(activity.NewTools(st))
	s := New(tb, "test")
	srv := httptest.NewServer(s)
	defer srv.Close()
	tok, revoke := s.Grant(officetools.Scope{ProjectID: p.ID}, time.Minute)
	defer revoke()

	call := func(tok, body string) map[string]any {
		req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}

	list := call(tok, `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	resources := list["result"].(map[string]any)["resources"].([]any)
	if len(resources) == 0 {
		t.Fatalf("resources/list: %v", list)
	}
	uri := resources[0].(map[string]any)["uri"].(string)
	if uri != "office://project/"+p.ID+"/report" {
		t.Fatalf("resource uri: %q", uri)
	}

	read := call(tok, `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"`+uri+`"}}`)
	contents := read["result"].(map[string]any)["contents"].([]any)
	text := contents[0].(map[string]any)["text"].(string)
	if text == "" {
		t.Fatalf("resources/read: %v", read)
	}

	other, _ := st.Repos().Create(ctx, storage.Repo{Name: "q", Path: t.TempDir()})
	otherTok, revokeOther := s.Grant(officetools.Scope{ProjectID: other.ID}, time.Minute)
	defer revokeOther()
	if out := call(otherTok, `{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"`+uri+`"}}`); out["error"] == nil {
		t.Fatalf("another project's token must not read this resource: %v", out)
	}
}

// The MCP other_office (ADR-169) is served next to /mcp with the same tokens:
// its own tools (none when the run reaches no other office), and office's
// tools are not callable there.
func TestOtherOfficePath(t *testing.T) {
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	ctx := context.Background()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	s := New(officetools.New(st, nil, nil), "test")
	mux := http.NewServeMux()
	mux.Handle("/mcp", s)
	mux.Handle("/mcp"+OtherPath, s)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	tok, revoke := s.Grant(officetools.Scope{ProjectID: p.ID}, time.Minute)
	defer revoke()
	call := func(path, body string) map[string]any {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	init := call("/mcp"+OtherPath, `{"jsonrpc":"2.0","id":9,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`)["result"].(map[string]any)
	if init["serverInfo"].(map[string]any)["name"] != "agent-office other_office" || !strings.Contains(init["instructions"].(string), "send") {
		t.Fatalf("other_office introduces itself as office: %v", init)
	}
	list := call("/mcp"+OtherPath, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if n := len(list["result"].(map[string]any)["tools"].([]any)); n != 0 {
		t.Fatalf("other_office tools with no paired office: %d", n)
	}
	if out := call("/mcp"+OtherPath, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ops_overview","arguments":{}}}`); out["error"] == nil {
		t.Fatalf("an office tool ran on other_office: %v", out)
	}
	if out := call("/mcp", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ops_overview","arguments":{}}}`); out["error"] != nil {
		t.Fatalf("/mcp: %v", out)
	}
}
