package mcpgateway_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/storage"
)

// syncBuf is a log sink safe for the gateway's goroutines.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) count(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Count(s.b.String(), sub)
}

// A run's real calls tell office a stdio server is broken: it is no longer
// given to runs, the break is kept once, and a check after installing its
// command finds it working at once (no old backoff, B3).
func TestHealthFromCalls(t *testing.T) {
	st, box := openStore(t)
	ctx := context.Background()
	bin := filepath.Join(t.TempDir(), "fakestdio") // not built yet: "not installed"
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "local", Kind: "stdio", Command: bin, Enabled: true, Scope: "machine"})
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuf{}
	var mu sync.Mutex
	var changes []string
	gw := &mcpgateway.Gateway{Store: st, Box: box, Identify: writerToken,
		Log: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		OnStatus: func(m storage.MCPServer) {
			mu.Lock()
			changes = append(changes, m.LastCheckStatus)
			mu.Unlock()
		}}
	t.Cleanup(gw.Close)
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)
	c := rpcClient{t: t, url: office.URL + "/mcp/s/local"}

	if got := gw.NamesFor(ctx, mcpgateway.Caller{Kind: "person"}); !slices.Contains(got, "local") {
		t.Fatalf("never checked: NamesFor = %v, want local", got)
	}
	for range 3 { // a run's CLI tries several times
		if code, _ := c.post(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != http.StatusBadGateway {
			t.Fatalf("missing command: HTTP %d", code)
		}
	}
	got, _ := st.MCPServers().Get(ctx, m.ID)
	if got.LastCheckStatus != "error" || !strings.Contains(got.LastCheckError, "máy chưa có lệnh") || strings.Contains(got.LastCheckError, "thử chạy lại") {
		t.Fatalf("after failed calls: %q %q", got.LastCheckStatus, got.LastCheckError)
	}
	since := mcpgateway.BrokenSince(ctx, st, m.ID)
	if since.IsZero() {
		t.Fatal("break not kept")
	}
	mu.Lock()
	if len(changes) != 1 || changes[0] != "error" {
		t.Fatalf("status changes told = %v, want one error", changes)
	}
	mu.Unlock()
	// B2: the same failure in its backoff is not warned of again
	if n := logs.count(`level=WARN msg="mcp gateway" server=local`); n != 1 {
		t.Fatalf("warnings = %d, want 1:\n%s", n, logs.b.String())
	}
	// B1: a run no longer gets it; its brief says why
	caller := mcpgateway.Caller{Kind: "person"}
	if got := gw.NamesFor(ctx, caller); slices.Contains(got, "local") {
		t.Fatalf("broken server still given to runs: %v", got)
	}
	if got := gw.BrokenFor(ctx, caller); len(got) != 1 || !strings.HasPrefix(got[0], "local (máy chưa có lệnh") {
		t.Fatalf("BrokenFor = %v", got)
	}

	// installed now: a check right away finds it (B3), the break is over
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/fakestdio").CombinedOutput(); err != nil {
		t.Fatalf("build fakestdio: %v\n%s", err, out)
	}
	checked, err := gw.CheckServer(ctx, got)
	if err != nil || checked.LastCheckStatus != "ok" {
		t.Fatalf("check after install: %q %q %v", checked.LastCheckStatus, checked.LastCheckError, err)
	}
	if !mcpgateway.BrokenSince(ctx, st, m.ID).IsZero() {
		t.Fatal("break still kept after it works")
	}
	if got := gw.NamesFor(ctx, caller); !slices.Contains(got, "local") {
		t.Fatalf("working server not given back: %v", got)
	}
}

// A server already broken before office kept when (fetch without uvx on
// the day ADR-250 shipped) gets its break kept at the next check, and later
// checks keep that same moment: the person's "Bỏ qua" holds until a new break.
func TestBrokenSinceForServerBrokenBefore(t *testing.T) {
	st, box := openStore(t)
	ctx := context.Background()
	m, _ := st.MCPServers().Create(ctx, storage.MCPServer{Name: "fetch", Kind: "stdio", Command: filepath.Join(t.TempDir(), "uvx"), Enabled: true, Scope: "machine"})
	if err := st.MCPServers().SetCheck(ctx, m.ID, "error", "máy chưa có lệnh uvx", nil, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	gw := &mcpgateway.Gateway{Store: st, Box: box}
	t.Cleanup(gw.Close)
	m, _ = st.MCPServers().Get(ctx, m.ID)
	if _, err := gw.CheckServer(ctx, m); err != nil {
		t.Fatal(err)
	}
	first := mcpgateway.BrokenSince(ctx, st, m.ID)
	if first.IsZero() {
		t.Fatal("break of a server broken before not kept")
	}
	time.Sleep(1100 * time.Millisecond) // a later check, a later LastCheckAt
	m, _ = st.MCPServers().Get(ctx, m.ID)
	if _, err := gw.CheckServer(ctx, m); err != nil {
		t.Fatal(err)
	}
	if again := mcpgateway.BrokenSince(ctx, st, m.ID); !again.Equal(first) {
		t.Fatalf("break moved: %v then %v", first, again)
	}
}

func TestInstallCommand(t *testing.T) {
	missing := func(cmd string) storage.MCPServer {
		return storage.MCPServer{Kind: "stdio", Command: cmd, LastCheckStatus: "error", LastCheckError: "máy chưa có lệnh " + cmd + ": …"}
	}
	if got := mcpgateway.InstallCommand(missing("uvx")); !strings.Contains(got, "astral.sh/uv/install.sh") {
		t.Fatalf("uvx: %q", got)
	}
	if got := mcpgateway.InstallCommand(missing("/opt/bin/bunx")); !strings.Contains(got, "bun.sh/install") {
		t.Fatalf("bunx by path: %q", got)
	}
	for _, m := range []storage.MCPServer{
		missing("npx"), // no one-line install: the hint links the site
		{Kind: "stdio", Command: "uvx", LastCheckStatus: "ok"},
		{Kind: "stdio", Command: "uvx", LastCheckStatus: "error", LastCheckError: "MCP không trả lời tools/list"},
		{Kind: "http", Command: "uvx", LastCheckStatus: "error", LastCheckError: "máy chưa có lệnh uvx"},
	} {
		if got := mcpgateway.InstallCommand(m); got != "" {
			t.Fatalf("%+v: %q, want none", m, got)
		}
	}
}

func TestDue(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	base := storage.MCPServer{Kind: "stdio", Enabled: true, Scope: "machine"}
	with := func(f func(*storage.MCPServer)) storage.MCPServer { m := base; f(&m); return m }
	cases := []struct {
		name string
		m    storage.MCPServer
		want bool
	}{
		{"never checked", base, true},
		{"broken, 16 min", with(func(m *storage.MCPServer) { m.LastCheckStatus, m.LastCheckAt = "error", at(16*time.Minute) }), true},
		{"broken, 5 min", with(func(m *storage.MCPServer) { m.LastCheckStatus, m.LastCheckAt = "error", at(5*time.Minute) }), false},
		{"ok, 1 h", with(func(m *storage.MCPServer) { m.LastCheckStatus, m.LastCheckAt = "ok", at(time.Hour) }), false},
		{"ok, 7 h", with(func(m *storage.MCPServer) { m.LastCheckStatus, m.LastCheckAt = "ok", at(7*time.Hour) }), true},
		{"off", with(func(m *storage.MCPServer) { m.Enabled = false }), false},
		{"project scope", with(func(m *storage.MCPServer) { m.Scope = "project" }), false},
	}
	for _, c := range cases {
		if got := mcpgateway.Due(c.m, now); got != c.want {
			t.Errorf("%s: Due = %v, want %v", c.name, got, c.want)
		}
	}
}

// The background sweep heals a broken server once what it needs is there.
func TestCheckDueHeals(t *testing.T) {
	st, box := openStore(t)
	ctx := context.Background()
	bin := filepath.Join(t.TempDir(), "fakestdio")
	m, _ := st.MCPServers().Create(ctx, storage.MCPServer{Name: "later", Kind: "stdio", Command: bin, Enabled: true, Scope: "machine"})
	gw := &mcpgateway.Gateway{Store: st, Box: box}
	t.Cleanup(gw.Close)
	if n, err := gw.CheckDue(ctx, time.Now(), false); err != nil || n != 1 {
		t.Fatalf("first sweep: %d %v", n, err)
	}
	if got, _ := st.MCPServers().Get(ctx, m.ID); got.LastCheckStatus != "error" {
		t.Fatalf("status %q, want error", got.LastCheckStatus)
	}
	if n, _ := gw.CheckDue(ctx, time.Now(), false); n != 0 {
		t.Fatalf("checked again before its interval: %d", n)
	}
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/fakestdio").CombinedOutput(); err != nil {
		t.Fatalf("build fakestdio: %v\n%s", err, out)
	}
	if n, err := gw.CheckDue(ctx, time.Now().Add(mcpgateway.RecheckBroken+time.Second), false); err != nil || n != 1 {
		t.Fatalf("later sweep: %d %v", n, err)
	}
	if got, _ := st.MCPServers().Get(ctx, m.ID); got.LastCheckStatus != "ok" {
		t.Fatalf("status %q %q, want ok", got.LastCheckStatus, got.LastCheckError)
	}
}
