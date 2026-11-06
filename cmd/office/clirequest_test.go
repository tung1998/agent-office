package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/cliuse"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func requestStore(t *testing.T) storage.Store {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// fakeCodex installs a fake npm codex at version in dir; npm writes
// version to into it and logs each call.
func fakeCodex(t *testing.T, dir, version, to string) {
	t.Helper()
	pdir := filepath.Join(dir, "node_modules", "@openai", "codex", "bin")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(pdir, "codex")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho codex-cli "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "codex")); err != nil {
		t.Fatal(err)
	}
	npm := "#!/bin/sh\necho \"npm $*\" >> \"" + dir + "/npm.log\"\nprintf '#!/bin/sh\\necho codex-cli " + to + "\\n' > \"" + real + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte(npm), 0o755); err != nil {
		t.Fatal(err)
	}
}

// `office tools update` while office runs goes through it: the update waits
// for the run using the CLI (it is not replaced under it), and the command
// waits for how it ended and says it (ADR-259).
func TestToolsUpdateGoesThroughRunningOffice(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	st := requestStore(t)
	dir := t.TempDir()
	fakeCodex(t, dir, "0.166.0", "0.167.0")
	m := clitools.NewManagerWithPath(dir + ":/usr/bin:/bin")
	go serveCLIRequests(ctx, st, m, 10*time.Millisecond)

	end, _ := cliuse.Begin(ctx, "codex") // a run has codex running
	type out struct {
		res   []cliResult
		taken bool
		err   error
	}
	done := make(chan out, 1)
	go func() {
		res, taken, err := requestCLIUpdate(ctx, st, []string{"codex"}, "script", false, 2*time.Second, 10*time.Millisecond, nil)
		done <- out{res, taken, err}
	}()
	waitFor(t, func() bool { u, ok := m.Upgrade("codex"); return ok && u.State == "waiting" })
	time.Sleep(100 * time.Millisecond)
	if b, _ := os.ReadFile(filepath.Join(dir, "npm.log")); len(b) != 0 {
		t.Fatalf("the CLI was replaced under a run: %s", b)
	}
	select {
	case o := <-done:
		t.Fatalf("the command ended before the update: %+v", o)
	default:
	}
	end()
	var o out
	select {
	case o = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the command did not end")
	}
	if o.err != nil || !o.taken || len(o.res) != 1 || o.res[0].State != "done" || !strings.Contains(o.res[0].Text, "0.166.0 → 0.167.0") {
		t.Fatalf("result = %+v", o)
	}
	if u, _ := m.Upgrade("codex"); u.By != "script" || u.When != "idle" {
		t.Fatalf("update = %+v", u)
	}
	var left []cliRequest
	_, _ = st.Settings().Get(ctx, cliRequestKey, &left)
	if len(left) != 0 {
		t.Fatalf("the request stayed: %+v", left)
	}
}

// With no office running nobody takes the request: it is taken back and the
// command updates on its own, as before.
func TestToolsUpdateWithoutOffice(t *testing.T) {
	ctx := context.Background()
	st := requestStore(t)
	res, taken, err := requestCLIUpdate(ctx, st, []string{"codex"}, "script", false, 50*time.Millisecond, 10*time.Millisecond, nil)
	if err != nil || taken || res != nil {
		t.Fatalf("res=%+v taken=%v err=%v", res, taken, err)
	}
	var left []cliRequest
	_, _ = st.Settings().Get(ctx, cliRequestKey, &left)
	if len(left) != 0 {
		t.Fatalf("an untaken request stayed: %+v", left)
	}
}

// A tool office does not know, or one not installed, ends at once with why.
func TestToolsUpdateUnknownTool(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	st := requestStore(t)
	m := clitools.NewManagerWithPath(t.TempDir())
	go serveCLIRequests(ctx, st, m, 10*time.Millisecond)
	res, taken, err := requestCLIUpdate(ctx, st, []string{"nope", "codex"}, "an", false, 2*time.Second, 10*time.Millisecond, nil)
	if err != nil || !taken || len(res) != 2 || res[0].State != "failed" || res[1].State != "failed" || !strings.Contains(res[1].Text, "chưa cài") {
		t.Fatalf("res=%+v taken=%v err=%v", res, taken, err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
