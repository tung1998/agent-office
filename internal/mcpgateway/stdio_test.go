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
	if err != nil || checked.LastCheckStatus != "ok" || len(checked.LastTools) != 7 {
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

	// a request from the server (sampling) is refused, the call goes on
	if _, v := c.tool(8, "ask", nil); !strings.Contains(v, "chưa hỗ trợ") {
		t.Fatalf("server request answered %q", v)
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

	// office shutting down: nothing left running
	_, pid3 := c.tool(13, "pid", nil)
	var p3 int
	fmt.Sscan(pid3, &p3)
	gw.Close()
	if len(gw.Running()) > 0 || alive(p3) {
		t.Fatalf("after Close: %v alive=%v", gw.Running(), alive(p3))
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
