package mcpgateway_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-254: a stdio server (playwright: its browser tab) a live run called is
// not stopped as idle under it; once the run ends, it is.
func TestStdioKeptWhileItsRunLives(t *testing.T) {
	bin := fakeStdio(t)
	st, box := openStore(t)
	ctx := context.Background()
	if _, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "local", Kind: "stdio", Command: bin, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	var live atomic.Bool
	live.Store(true)
	gw := &mcpgateway.Gateway{Store: st, Box: box, StdioIdle: 150 * time.Millisecond,
		Identify: func(r *http.Request) (mcpgateway.Caller, bool) {
			c, ok := writerToken(r)
			c.Run = "run-1"
			return c, ok
		},
		Alive: func(run string) bool { return run == "run-1" && live.Load() }}
	t.Cleanup(gw.Close)
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)
	c := rpcClient{t, office.URL + "/mcp/s/local"}

	_, pid1 := c.tool(1, "pid", nil)
	var p1 int
	fmt.Sscan(pid1, &p1)
	time.Sleep(800 * time.Millisecond) // well past StdioIdle
	if !alive(p1) || len(gw.Running()) != 1 {
		t.Fatal("stopped as idle while the run that called it lives")
	}
	if _, pid := c.tool(2, "pid", nil); pid != pid1 {
		t.Fatalf("a new process for the same run: %s then %s", pid1, pid)
	}

	live.Store(false) // the run ended
	deadline := time.Now().Add(5 * time.Second)
	for alive(p1) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if alive(p1) {
		t.Fatal("still running after its run ended and it went idle")
	}
}

// ADR-254: a refused token gets the reason office gives (Deny), not a bare 401.
func TestGatewaySaysWhyATokenIsRefused(t *testing.T) {
	st, box := openStore(t)
	gw := &mcpgateway.Gateway{Store: st, Box: box, Identify: writerToken,
		Deny: func(r *http.Request) string { return "token của lượt chạy đã bị thu hồi lúc 10:00" }}
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)
	req, _ := http.NewRequest("POST", office.URL+"/mcp/s/local", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer old")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 401 || !strings.Contains(string(body), "thu hồi") {
		t.Fatalf("401 = %d %q", resp.StatusCode, body)
	}
}
