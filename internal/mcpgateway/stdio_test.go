package mcpgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func openStore(t *testing.T) (storage.Store, mcpgateway.Sealer) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return st, box
}

// fakeStdio builds testdata/fakestdio once per test.
func fakeStdio(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakestdio")
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/fakestdio").CombinedOutput(); err != nil {
		t.Fatalf("build fakestdio: %v\n%s", err, out)
	}
	return bin
}

// rpcClient posts JSON-RPC to office's gateway as one run would.
type rpcClient struct {
	t   *testing.T
	url string
}

func (c rpcClient) post(body string) (int, string) {
	c.t.Helper()
	req, _ := http.NewRequest("POST", c.url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer run-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// tool calls a tool and returns the answer's id and text (or error).
func (c rpcClient) tool(id int, name string, args map[string]any) (string, string) {
	c.t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	code, body := c.post(string(raw))
	if code != 200 {
		return "", fmt.Sprintf("HTTP %d %s", code, body)
	}
	var out struct {
		ID     json.RawMessage `json:"id"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		c.t.Fatalf("answer %q: %v", body, err)
	}
	if out.Error != nil {
		return string(out.ID), "error: " + out.Error.Message
	}
	if len(out.Result.Content) == 0 {
		return string(out.ID), ""
	}
	return string(out.ID), out.Result.Content[0].Text
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func TestStdio(t *testing.T) {
	bin := fakeStdio(t)
	st, box := openStore(t)
	ctx := context.Background()
	env, _ := mcpgateway.SealMap(box, map[string]string{"FAKE_TOKEN": "tok-secret-123456"})
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "local", Kind: "stdio", Command: bin, EnvEnc: env, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	gw := &mcpgateway.Gateway{Store: st, Box: box, StdioIdle: 400 * time.Millisecond,
		Identify: writerToken}
	t.Cleanup(gw.Close)
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)
	c := rpcClient{t, office.URL + "/mcp/s/local"}

	checked, err := gw.CheckServer(ctx, m)
	if err != nil || checked.LastCheckStatus != "ok" || len(checked.LastTools) != 9 {
		t.Fatalf("check = %+v %v", checked, err)
	}
	if names := gw.Names(ctx); len(names) != 1 || names[0] != "local" {
		t.Fatalf("Names = %v", names)
	}

	// each client's initialize comes from office's cache; the process saw one
	for i := 0; i < 2; i++ {
		code, body := c.post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"cli"}}}`)
		if code != 200 || !strings.Contains(body, `"fakestdio"`) || !strings.Contains(body, `"id":1`) {
			t.Fatalf("initialize = %d %s", code, body)
		}
		if code, _ := c.post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != 202 {
			t.Fatalf("notification = %d", code)
		}
	}
	if _, n := c.tool(2, "inits", nil); n != "1" {
		t.Fatalf("process got %s initialize", n)
	}
	if _, v := c.tool(3, "env", nil); v != "tok-secret-123456" {
		t.Fatalf("env = %q", v)
	}

	// two clients at once with the same id: each gets its own answer back
	var wg sync.WaitGroup
	got := make([]string, 2)
	ids := make([]string, 2)
	for i, args := range []map[string]any{{"text": "first", "ms": 300}, {"text": "second", "ms": 10}} {
		wg.Add(1)
		go func() { defer wg.Done(); ids[i], got[i] = c.tool(7, "slow", args) }()
	}
	wg.Wait()
	if got[0] != "first" || got[1] != "second" || ids[0] != "7" || ids[1] != "7" {
		t.Fatalf("parallel = %v ids %v", got, ids)
	}

	// a request from the server (sampling, elicitation) is refused when
	// office has nobody to ask (gw.Elicit unset), the call goes on
	if _, v := c.tool(8, "ask", nil); !strings.Contains(v, "chưa hỗ trợ") {
		t.Fatalf("server request answered %q", v)
	}
	if _, v := c.tool(9, "elicit", nil); !strings.Contains(v, "chưa hỗ trợ") {
		t.Fatalf("elicitation answered %q", v)
	}
	if code, _ := (func() (int, string) {
		req, _ := http.NewRequest("GET", c.url, nil)
		req.Header.Set("Authorization", "Bearer run-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode, ""
	})(); code != 405 {
		t.Fatalf("GET = %d", code)
	}

	// idle: office stops it, the next call starts a new one
	_, pid1 := c.tool(9, "pid", nil)
	var p1 int
	fmt.Sscan(pid1, &p1)
	deadline := time.Now().Add(5 * time.Second)
	for (len(gw.Running()) > 0 || alive(p1)) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if len(gw.Running()) > 0 || alive(p1) {
		t.Fatalf("still running after idle: %v", gw.Running())
	}
	if _, pid2 := c.tool(10, "pid", nil); pid2 == pid1 || pid2 == "" {
		t.Fatalf("restart after idle: %q then %q", pid1, pid2)
	}

	// it dies: the call says so with its stderr; a later call runs it again
	if _, v := c.tool(11, "crash", nil); !strings.Contains(v, "đã dừng") || !strings.Contains(v, "boom") {
		t.Fatalf("crash = %q", v)
	}
	var v string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, v = c.tool(12, "echo", map[string]any{"text": "back"}); v == "back" {
			break
		}
	}
	if v != "back" {
		t.Fatalf("after crash = %q", v)
	}

	// a stdio call that fails (the process's own isError result, or a
	// roundTrip error) is logged as "error", not "ok"
	if _, v := c.tool(14, "fail", nil); !strings.Contains(v, "tool failed on purpose") {
		t.Fatalf("fail = %q", v)
	}
	calls, err := st.MCPCalls().List(ctx, m.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]int{}
	for _, c := range calls {
		by[c.Status]++
	}
	if by["ok"] == 0 || by["error"] == 0 {
		t.Fatalf("mcp_calls log = %v (%+v)", by, calls)
	}
	for _, c := range calls {
		if c.Tool == "fail" && (c.Status != "error" || c.Error != "tool failed on purpose") {
			t.Fatalf("fail call log = %+v", c)
		}
	}

	// office shutting down: nothing left running
	_, pid3 := c.tool(13, "pid", nil)
	var p3 int
	fmt.Sscan(pid3, &p3)
	gw.Close()
	if len(gw.Running()) > 0 || alive(p3) {
		t.Fatalf("after Close: %v alive=%v", gw.Running(), alive(p3))
	}
}

// TestStdioElicit: an MCP server's elicitation/create, with Gateway.Elicit
// wired, is asked of whoever called the tool and answered on the server's
// own request id with what that asker gave back (ADR-264).
func TestStdioElicit(t *testing.T) {
	bin := fakeStdio(t)
	st, box := openStore(t)
	ctx := context.Background()
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "local", Kind: "stdio", Command: bin, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	var gotCaller mcpgateway.Caller
	var gotServer, gotMessage string
	var gotSchema json.RawMessage
	gw := &mcpgateway.Gateway{Store: st, Box: box, Identify: writerTokenInChat,
		Elicit: func(_ context.Context, c mcpgateway.Caller, server, message string, schema json.RawMessage) (string, json.RawMessage, error) {
			gotCaller, gotServer, gotMessage, gotSchema = c, server, message, schema
			return "accept", json.RawMessage(`{"ok":true}`), nil
		}}
	t.Cleanup(gw.Close)
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)
	c := rpcClient{t, office.URL + "/mcp/s/local"}

	if _, err := gw.CheckServer(ctx, m); err != nil {
		t.Fatal(err)
	}
	id, v := c.tool(1, "elicit", nil)
	if id != "1" {
		t.Fatalf("id = %q", id)
	}
	var reply struct {
		Action  string `json:"action"`
		Content struct {
			OK bool `json:"ok"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(v), &reply) != nil || reply.Action != "accept" || !reply.Content.OK {
		t.Fatalf("tool result = %q", v)
	}
	if gotServer != "local" || gotMessage != "cần xác nhận" || gotCaller.Kind != "person" || gotCaller.ConversationID != "conv-1" {
		t.Fatalf("elicit called with server=%q message=%q caller=%+v", gotServer, gotMessage, gotCaller)
	}
	if !strings.Contains(string(gotSchema), `"boolean"`) {
		t.Fatalf("schema = %s", gotSchema)
	}
}

// writerTokenInChat: like writerToken, but with a chat to ask in — the only
// kind of caller AskElicit's currentCaller() will pick (ADR-264).
func writerTokenInChat(r *http.Request) (mcpgateway.Caller, bool) {
	return mcpgateway.Caller{Kind: "person", CanWrite: true, ConversationID: "conv-1"}, r.Header.Get("Authorization") == "Bearer run-token"
}

// TestStdioElicitConcurrent: a slow answer to elicitation/create never
// blocks another, unrelated call on the same process from getting its own
// answer back (ADR-264 fixes a read-loop stall here).
func TestStdioElicitConcurrent(t *testing.T) {
	bin := fakeStdio(t)
	st, box := openStore(t)
	ctx := context.Background()
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "local", Kind: "stdio", Command: bin, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	reached := make(chan struct{})
	release := make(chan struct{})
	gw := &mcpgateway.Gateway{Store: st, Box: box, Identify: writerTokenInChat,
		Elicit: func(_ context.Context, _ mcpgateway.Caller, _, _ string, _ json.RawMessage) (string, json.RawMessage, error) {
			close(reached)
			<-release
			return "accept", json.RawMessage(`{"ok":true}`), nil
		}}
	t.Cleanup(gw.Close)
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)
	c := rpcClient{t, office.URL + "/mcp/s/local"}
	if _, err := gw.CheckServer(ctx, m); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() { defer close(done); c.tool(1, "elicit", nil) }()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("elicit never reached Gateway.Elicit")
	}
	if _, v := c.tool(2, "echo", map[string]any{"text": "back"}); v != "back" {
		t.Fatalf("echo while elicit waits = %q", v)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("elicit tool call never finished")
	}
}

// TestStdioOverlongLine: a stdio server writing one stdout line past the
// scanner's max token kills the process instead of leaving it running but
// deaf forever (the bug this guards: read()'s scanner error was never
// checked, so the pool kept reusing a process nobody read from again).
func TestStdioOverlongLine(t *testing.T) {
	small := 4 << 10
	restore := mcpgateway.SetStdioMaxLineForTest(small)
	t.Cleanup(restore)

	bin := fakeStdio(t)
	st, box := openStore(t)
	ctx := context.Background()
	if _, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "local", Kind: "stdio", Command: bin, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	gw := &mcpgateway.Gateway{Store: st, Box: box, Identify: writerToken}
	t.Cleanup(gw.Close)
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)
	c := rpcClient{t, office.URL + "/mcp/s/local"}

	_, pid1 := c.tool(1, "pid", nil)
	var p1 int
	fmt.Sscan(pid1, &p1)

	// the oversized line's own call never gets an answer (the process is
	// killed before it can reply); the call should fail fast rather than
	// hang until CallTimeout.
	// bigger than both the scanner's initial 64KB buffer and stdioMaxLine
	if _, v := c.tool(2, "bigline", map[string]any{"size": 80 << 10}); !strings.Contains(v, "đã dừng") {
		t.Fatalf("bigline call = %q, want a dead-process error", v)
	}

	deadline := time.Now().Add(5 * time.Second)
	for alive(p1) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(p1) {
		t.Fatalf("process %d still alive after overlong line", p1)
	}

	// a fresh call starts a new process (the earlier one was fully evicted,
	// not left registered-but-dead)
	if _, pid2 := c.tool(3, "pid", nil); pid2 == "" || pid2 == pid1 {
		t.Fatalf("restart after overlong line: %q then %q", pid1, pid2)
	}
}

func TestStdioMissingCommand(t *testing.T) {
	st, box := openStore(t)
	ctx := context.Background()
	gw := &mcpgateway.Gateway{Store: st, Box: box, Env: []string{"PATH=" + t.TempDir()}}
	t.Cleanup(gw.Close)
	for cmd, hint := range map[string]string{"uvx": "astral.sh/uv", "npx": "Node.js", "docker": "Docker", "no-such-tool": "PATH"} {
		m, _ := st.MCPServers().Create(ctx, storage.MCPServer{Name: "m-" + strings.ReplaceAll(cmd, "-", ""), Kind: "stdio", Command: cmd, Enabled: true})
		checked, err := gw.CheckServer(ctx, m)
		if err != nil || checked.LastCheckStatus != "error" || !strings.Contains(checked.LastCheckError, "máy chưa có lệnh "+cmd) || !strings.Contains(checked.LastCheckError, hint) {
			t.Fatalf("%s: %+v %v", cmd, checked.LastCheckError, err)
		}
	}
}
