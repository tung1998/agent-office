package main

import (
	"context"
	"errors"
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

// flakyGetStore wraps a Store whose Settings().Get fails the first n times
// it is called, then behaves normally.
type flakyGetStore struct {
	storage.Store
	n *int
}

func (f flakyGetStore) Settings() storage.SettingRepo {
	return flakyGetSettings{f.Store.Settings(), f.n}
}

type flakyGetSettings struct {
	storage.SettingRepo
	n *int
}

func (f flakyGetSettings) Get(ctx context.Context, key string, dst any) (bool, error) {
	if *f.n > 0 {
		*f.n--
		return false, errors.New("transient read error")
	}
	return f.SettingRepo.Get(ctx, key, dst)
}

// A transient error reading the request list during the poll loop must not
// read as "the request was deleted": requestCLIUpdate should retry, not
// report errOfficeGone's "đã bị xóa" message.
func TestRequestCLIUpdateRetriesOnTransientGetError(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	base := requestStore(t)
	dir := t.TempDir()
	fakeCodex(t, dir, "0.166.0", "0.167.0")
	m := clitools.NewManagerWithPath(dir + ":/usr/bin:/bin")
	go serveCLIRequests(ctx, base, m, 10*time.Millisecond)

	n := 3
	st := flakyGetStore{base, &n}
	res, taken, err := requestCLIUpdate(ctx, st, []string{"codex"}, "script", false, 2*time.Second, 10*time.Millisecond, nil)
	if err != nil || !taken || len(res) != 1 || res[0].State != "done" {
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

// The office that took the request goes down mid-update: the command is not
// left waiting 14h for it, it says so; the next office ends the request it
// left, failed, so nothing waits on it (ADR-260).
func TestToolsUpdateOfficeDiedAfterClaim(t *testing.T) {
	oldEvery, oldLost := cliBeatEvery, cliBeatLost
	cliBeatEvery, cliBeatLost = 20*time.Millisecond, time.Second
	t.Cleanup(func() { cliBeatEvery, cliBeatLost = oldEvery, oldLost })
	st := requestStore(t)
	dir := t.TempDir()
	fakeCodex(t, dir, "0.166.0", "0.167.0")
	// a slow CLI (a loaded machine): reading its version takes longer than
	// cliBeatLost, and the office still says it is alive meanwhile
	slow := filepath.Join(dir, "node_modules", "@openai", "codex", "bin", "codex")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 2\necho codex-cli 0.166.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := clitools.NewManagerWithPath(dir + ":/usr/bin:/bin")
	officeCtx, officeDown := context.WithCancel(context.Background())
	go serveCLIRequests(officeCtx, st, m, 10*time.Millisecond)
	end, _ := cliuse.Begin(context.Background(), "codex") // the update waits for this run
	defer end()

	ctx := context.Background()
	type out struct {
		res []cliResult
		err error
	}
	done := make(chan out, 1)
	start := time.Now()
	go func() {
		res, _, err := requestCLIUpdate(ctx, st, []string{"codex"}, "script", false, 2*time.Second, 10*time.Millisecond, nil)
		done <- out{res, err}
	}()
	waitFor(t, func() bool { u, ok := m.Upgrade("codex"); return ok && u.State == "waiting" })
	time.Sleep(3 * time.Second) // beats while alive, its CLI slow: not dropped
	select {
	case o := <-done:
		t.Fatalf("ended while the office was alive: %+v %v", o, o.err)
	default:
	}
	officeDown()
	var o out
	select {
	case o = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the command still waits for an office that is gone")
	}
	if !errors.Is(o.err, errOfficeGone) || time.Since(start) > 15*time.Second {
		t.Fatalf("result = %+v", o)
	}

	// a request the gone office started, nobody waiting on it: the next ends it
	past := time.Now().UTC().Add(-time.Minute)
	if _, err := editRequests(ctx, st, func(l []cliRequest) ([]cliRequest, bool) {
		return append(l, cliRequest{ID: "clu_orphan", Tools: []string{"codex"}, At: past, Taken: &past, Beat: &past,
			Res: []cliResult{{Tool: "codex", State: "waiting", Started: past}}}), true
	}); err != nil {
		t.Fatal(err)
	}
	next := &cliRequestServer{st: st, m: clitools.NewManagerWithPath(t.TempDir()), started: map[string]bool{}, every: cliBeatEvery, lost: cliBeatLost}
	next.once(ctx)
	var left []cliRequest
	_, _ = st.Settings().Get(ctx, cliRequestKey, &left)
	if len(left) != 1 || !left[0].Done || left[0].Res[0].State != "failed" || !strings.Contains(left[0].Res[0].Text, "office đã dừng") {
		t.Fatalf("left: %+v", left)
	}
}
