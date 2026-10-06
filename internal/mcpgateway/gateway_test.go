package mcpgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

const upstreamSecret = "Bearer upstream-secret-123456"

// fakeMCP is an MCP server answering as JSON or as an SSE stream; it wants
// the stored secret and records the Authorization it got.
func fakeMCP(t *testing.T, sse bool, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r.Header.Get("Authorization"))
		}
		if r.Header.Get("Authorization") != upstreamSecret {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "nope", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		if len(m.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch m.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake"}}
		case "tools/list":
			result = map[string]any{"tools": []any{
				map[string]any{"name": "resolve", "description": "Find a library\nmore", "annotations": map[string]any{"readOnlyHint": true}},
				map[string]any{"name": "write", "description": "Writes"},
			}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "hello " + m.Params.Name}}}
		default:
			result = map[string]any{}
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
		w.Header().Set("Mcp-Session-Id", "sess-1")
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, ": ping\n\nevent: message\ndata: %s\n\n", raw)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheck(t *testing.T) {
	for _, sse := range []bool{false, true} {
		srv := fakeMCP(t, sse, nil)
		tools, err := mcpgateway.Check(context.Background(), nil, srv.URL, map[string]string{"Authorization": upstreamSecret})
		if err != nil {
			t.Fatalf("sse=%v: %v", sse, err)
		}
		if len(tools) != 2 || tools[0].Name != "resolve" || tools[0].Description != "Find a library" || tools[0].ReadOnly == nil || !*tools[0].ReadOnly || tools[1].ReadOnly != nil {
			t.Fatalf("sse=%v tools = %+v", sse, tools)
		}
	}
}

func TestCheckErrors(t *testing.T) {
	srv := fakeMCP(t, false, nil)
	_, err := mcpgateway.Check(context.Background(), nil, srv.URL, map[string]string{"Authorization": "Bearer wrong"})
	if err == nil || !strings.Contains(err.Error(), "token sai") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("401 err = %v", err)
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL + "/mcp?key=sk-secret-in-url"
	dead.Close()
	_, err = mcpgateway.Check(context.Background(), nil, url, nil)
	if err == nil || !strings.Contains(err.Error(), "kiểm tra URL") || strings.Contains(err.Error(), "sk-secret") {
		t.Fatalf("unreachable err = %v", err)
	}
	if _, err := mcpgateway.Check(context.Background(), nil, "ftp://x", nil); err == nil {
		t.Fatal("ftp URL accepted")
	}
}

func TestSecrets(t *testing.T) {
	box, err := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := mcpgateway.SealMap(box, map[string]string{"Authorization": upstreamSecret})
	if err != nil || enc == "" || strings.Contains(enc, "upstream-secret") {
		t.Fatalf("sealed = %q, %v", enc, err)
	}
	m, err := mcpgateway.OpenMap(box, enc)
	if err != nil || m["Authorization"] != upstreamSecret {
		t.Fatalf("opened = %v, %v", m, err)
	}
	if e, _ := mcpgateway.SealMap(box, nil); e != "" {
		t.Fatalf("empty sealed = %q", e)
	}
	masked := mcpgateway.MaskMap(m)["Authorization"]
	if masked != "••••3456" || mcpgateway.Mask("short") != "••••" {
		t.Fatalf("masked = %q", masked)
	}
	got := mcpgateway.MergeHeaders(map[string]string{"Authorization": "old", "X-Gone": "x"}, map[string]string{"Authorization": "", "X-New": " v "})
	if len(got) != 2 || got["Authorization"] != "old" || got["X-New"] != "v" {
		t.Fatalf("merged = %v", got)
	}
	for name, ok := range map[string]bool{"context7": true, "my-mcp": true, "office": false, "Bad": false, "a_b": false, "-a": false, "": false} {
		if mcpgateway.ValidName(name) != ok {
			t.Fatalf("ValidName(%q) != %v", name, ok)
		}
	}
}

// writerToken: the run token of a person who may write.
func writerToken(r *http.Request) (mcpgateway.Caller, bool) {
	return mcpgateway.Caller{Kind: "person", CanWrite: true}, r.Header.Get("Authorization") == "Bearer run-token"
}

// With only Auth office does not know who calls: tools that read run, a
// tool that writes is refused (fail closed, ADR-094).
func TestGatewayAuthOnlyReadsOnly(t *testing.T) {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	var seen []string
	srv := fakeMCP(t, false, &seen)
	ctx := context.Background()
	ro := true
	enc, _ := mcpgateway.SealMap(box, map[string]string{"Authorization": upstreamSecret})
	m, _ := st.MCPServers().Create(ctx, storage.MCPServer{Name: "plain", URL: srv.URL, HeadersEnc: enc, Enabled: true})
	if err := st.MCPServers().SetCheck(ctx, m.ID, "ok", "", []storage.MCPTool{{Name: "read", ReadOnly: &ro}, {Name: "write"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	gw := &mcpgateway.Gateway{Store: st, Box: box, Auth: func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer run-token" }}
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)
	call := func(body string) string {
		req, _ := http.NewRequest("POST", office.URL+"/mcp/s/plain", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer run-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return string(raw)
	}
	if body := call(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"write","arguments":{}}}`); !strings.Contains(body, "chỉ được đọc") || len(seen) != 0 {
		t.Fatalf("write with only Auth = %s (seen %v)", body, seen)
	}
	if body := call(`[{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"write"}}]`); !strings.Contains(body, "batch") || len(seen) != 0 {
		t.Fatalf("batch with only Auth = %s (seen %v)", body, seen)
	}
	if body := call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read","arguments":{}}}`); !strings.Contains(body, "hello read") {
		t.Fatalf("read with only Auth = %s", body)
	}
}

func TestGateway(t *testing.T) {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	var seen []string
	jsonSrv, sseSrv := fakeMCP(t, false, &seen), fakeMCP(t, true, &seen)
	enc, _ := mcpgateway.SealMap(box, map[string]string{"Authorization": upstreamSecret})
	ctx := context.Background()
	st.MCPServers().Create(ctx, storage.MCPServer{Name: "plain", URL: jsonSrv.URL, HeadersEnc: enc, Enabled: true})
	st.MCPServers().Create(ctx, storage.MCPServer{Name: "streamy", URL: sseSrv.URL, HeadersEnc: enc, Enabled: true})
	st.MCPServers().Create(ctx, storage.MCPServer{Name: "off", URL: jsonSrv.URL, HeadersEnc: enc})

	gw := &mcpgateway.Gateway{Store: st, Box: box, Identify: writerToken}
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)

	call := func(name, token string) (*http.Response, string) {
		body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"resolve","arguments":{}}}`
		req, _ := http.NewRequest("POST", office.URL+"/mcp/s/"+name, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp, string(raw)
	}
	if resp, _ := call("plain", "wrong"); resp.StatusCode != 401 {
		t.Fatalf("wrong token = %d", resp.StatusCode)
	}
	if len(seen) != 0 {
		t.Fatalf("a refused request reached the server: %v", seen)
	}
	resp, body := call("plain", "run-token")
	if resp.StatusCode != 200 || !strings.Contains(body, "hello resolve") || resp.Header.Get("Mcp-Session-Id") != "sess-1" {
		t.Fatalf("json = %d %s", resp.StatusCode, body)
	}
	resp, body = call("streamy", "run-token")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") || !strings.Contains(body, "data: ") || !strings.Contains(body, "hello resolve") {
		t.Fatalf("sse = %d %s", resp.StatusCode, body)
	}
	for _, a := range seen {
		if a != upstreamSecret {
			t.Fatalf("the server got Authorization %q (the run's token must not leak)", a)
		}
	}
	for _, name := range []string{"off", "nope"} {
		if resp, _ := call(name, "run-token"); resp.StatusCode != 404 {
			t.Fatalf("%s = %d", name, resp.StatusCode)
		}
	}
	if names := gw.Names(ctx); strings.Join(names, ",") != "plain,streamy" {
		t.Fatalf("Names = %v", names)
	}

	// a stored secret the server refuses: 502, without WWW-Authenticate
	bad, _ := mcpgateway.SealMap(box, map[string]string{"Authorization": "Bearer stale"})
	m, _ := st.MCPServers().GetByName(ctx, "plain")
	m.HeadersEnc = bad
	st.MCPServers().Update(ctx, m)
	if resp, _ := call("plain", "run-token"); resp.StatusCode != 502 || resp.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("refused upstream = %d %v", resp.StatusCode, resp.Header)
	}
	checked, err := gw.CheckServer(ctx, m)
	if err != nil || checked.LastCheckStatus != "error" || !strings.Contains(checked.LastCheckError, "token") {
		t.Fatalf("check bad = %+v %v", checked, err)
	}
	m.HeadersEnc = enc
	checked, _ = gw.CheckServer(ctx, m)
	if checked.LastCheckStatus != "ok" || len(checked.LastTools) != 2 {
		t.Fatalf("check ok = %+v", checked)
	}
}
