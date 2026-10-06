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

	"github.com/tung1998/agent-office/internal/officetools"
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
	st.Monitors().Create(ctx, storage.Monitor{ProjectID: p.ID, Name: "web", Type: "http", Target: "http://x", IntervalS: 60, Enabled: true, Status: "down", LastMessage: "HTTP 500"})
	st.Monitors().Create(ctx, storage.Monitor{ProjectID: other.ID, Name: "secret", Type: "http", Target: "http://y", IntervalS: 60, Enabled: true})

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
	if n := len(list["result"].(map[string]any)["tools"].([]any)); n != 9 { // + read_link, search_history
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
