package clitools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/cliuse"
)

// npmTool installs a fake npm package of tool in dir (bin/<name> links into
// node_modules, so the update method is npm) printing version.
func npmTool(t *testing.T, dir, name, pkg, version string) string {
	t.Helper()
	pdir := filepath.Join(dir, "node_modules", pkg, "bin")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(pdir, name)
	writeBin(t, pdir, name, "#!/bin/sh\necho "+name+"-cli "+version+"\n")
	_ = os.Remove(filepath.Join(dir, name))
	if err := os.Symlink(real, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	return real
}

func waitUpgrade(t *testing.T, m *Manager, id string, states ...string) UpgradeView {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if u, ok := m.Upgrade(id); ok {
			for _, s := range states {
				if u.State == s {
					return u
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	u, _ := m.Upgrade(id)
	t.Fatalf("upgrade of %s = %+v, want %v", id, u, states)
	return u
}

// The newest version comes from the npm registry for an npm install and is
// kept LatestTTL: the status says when it is newer.
func TestLatestNpm(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/@openai%2Fcodex/latest" && r.URL.Path != "/@openai/codex/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"name":"@openai/codex","version":"0.167.0"}`))
	}))
	defer srv.Close()
	m, dir := testManager(t)
	m.registry = srv.URL
	npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	v, err := m.LatestNow(context.Background(), "codex")
	if err != nil || v != "0.167.0" {
		t.Fatalf("latest = %q %v", v, err)
	}
	s, _ := m.Status(context.Background(), "codex")
	if s.Latest != "0.167.0" || !s.Outdated {
		t.Fatalf("status latest=%q outdated=%v", s.Latest, s.Outdated)
	}
	if calls.Load() != 1 {
		t.Fatalf("registry read %d times, want once (kept)", calls.Load())
	}
}

// A Homebrew cask reads its version from brew info.
func TestLatestBrew(t *testing.T) {
	m, dir := testManager(t)
	cask := filepath.Join(dir, "Caskroom", "codex", "0.1.0")
	if err := os.MkdirAll(cask, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBin(t, cask, "codex", "#!/bin/sh\necho codex-cli 0.1.0\n")
	if err := os.Symlink(filepath.Join(cask, "codex"), filepath.Join(dir, "codex")); err != nil {
		t.Fatal(err)
	}
	writeBin(t, dir, "brew", `#!/bin/sh
case "$*" in
  "info --json=v2 --cask codex") echo '{"formulae":[],"casks":[{"token":"codex","version":"0.2.0"}]}';;
  *) echo "unexpected $*" >&2; exit 1;;
esac
`)
	v, err := m.LatestNow(context.Background(), "codex")
	if err != nil || v != "0.2.0" {
		t.Fatalf("latest = %q %v", v, err)
	}
}

func TestOutdatedCompare(t *testing.T) {
	cases := []struct {
		installed, latest string
		want              bool
	}{
		{"codex-cli 0.166.0", "0.167.0", true},
		{"2.1.318 (Claude Code)", "2.1.318", false},
		{"0.10.0", "0.9.9", false},
		{"1.2.3", "", false},
		{"", "1.0.0", false},
		{"0.66.0", "1.0.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.installed, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.installed, c.latest, got)
		}
	}
}

// A newest version read is kept LatestTTL; a network that fails never holds
// up the status (it is read in the background).
func TestLatestCached(t *testing.T) {
	var calls atomic.Int32
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			<-block // a slow network
		}
		_, _ = w.Write([]byte(`{"version":"3.0.0"}`))
	}))
	defer srv.Close()
	defer close(block)
	m, dir := testManager(t)
	m.registry, m.noFetch = srv.URL, false
	now := time.Now()
	m.now = func() time.Time { return now }
	npmTool(t, dir, "gemini", "@google/gemini-cli", "2.0.0")
	if _, err := m.LatestNow(context.Background(), "gemini"); err != nil {
		t.Fatal(err)
	}
	if v := m.latestCached("gemini"); v != "3.0.0" || calls.Load() != 1 {
		t.Fatalf("cached = %q after %d reads", v, calls.Load())
	}
	now = now.Add(LatestTTL + time.Minute) // stale: read again, but not waited on
	start := time.Now()
	s, _ := m.Status(context.Background(), "gemini")
	if time.Since(start) > 3*time.Second || s.Latest != "3.0.0" || !s.Outdated || !s.LatestChecking {
		t.Fatalf("status took %v, latest %q", time.Since(start), s.Latest)
	}
}

// When idle: an update of Codex waits while a run has Codex running; one of
// Claude goes on at once.
func TestUpdateWaitsForRunsOnTool(t *testing.T) {
	m, dir := testManager(t)
	m.upPoll = 20 * time.Millisecond
	npmTool(t, dir, "codex", "@openai/codex", "1.0.0")
	npmTool(t, dir, "claude", "@anthropic-ai/claude-code", "1.0.0")
	writeBin(t, dir, "npm", "#!/bin/sh\necho \"npm $*\"\n")
	run, _ := cliuse.Begin(context.Background(), filepath.Join(dir, "codex"))
	u, err := m.UpdateWhenIdle("codex", "an")
	if err != nil || u.State != "waiting" || u.Waiting != 1 {
		t.Fatalf("start = %+v %v", u, err)
	}
	if _, err := m.UpdateWhenIdle("codex", "an"); err != ErrUpgrading {
		t.Fatalf("second update err = %v", err)
	}
	if _, err := m.UpdateWhenIdle("claude", "an"); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, m, "claude", "done") // another CLI: not held by Codex's run
	time.Sleep(100 * time.Millisecond)
	if u, _ := m.Upgrade("codex"); u.State != "waiting" || u.Waiting != 1 {
		t.Fatalf("codex updated under a run: %+v", u)
	}
	s, _ := m.Status(context.Background(), "codex")
	if s.InUse != 1 || s.Upgrade == nil || s.Upgrade.State != "waiting" {
		t.Fatalf("status = in use %d, upgrade %+v", s.InUse, s.Upgrade)
	}
	run()
	v := waitUpgrade(t, m, "codex", "done")
	if v.From != "1.0.0" || v.JobID == "" {
		t.Fatalf("done = %+v", v)
	}
	if j, ok := m.Job(v.JobID); !ok || !strings.Contains(j.Snapshot().Output, "@openai/codex@latest") {
		t.Fatalf("update job = %v", ok)
	}
}

// Other CLIs' runs do not hold an update; "now" does not wait either.
func TestUpdateIgnoresOtherTools(t *testing.T) {
	m, dir := testManager(t)
	m.upPoll = 20 * time.Millisecond
	npmTool(t, dir, "gemini", "@google/gemini-cli", "1.0.0")
	writeBin(t, dir, "npm", "#!/bin/sh\necho \"npm $*\"\n")
	other, _ := cliuse.Begin(context.Background(), "codex")
	defer other()
	if _, err := m.UpdateWhenIdle("gemini", "an"); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, m, "gemini", "done")
	run, _ := cliuse.Begin(context.Background(), "gemini")
	defer run()
	if u, err := m.UpdateNow("gemini", "an"); err != nil || u.When != "now" || u.State != "updating" {
		t.Fatalf("now = %+v %v", u, err)
	}
	waitUpgrade(t, m, "gemini", "done")
}

// New runs wait while the files change, and go once the update ends.
func TestHoldReleasedAfterUpdate(t *testing.T) {
	m, dir := testManager(t)
	npmTool(t, dir, "dsh", "@deepseek-ai/dsh", "1.0.0")
	gate := filepath.Join(dir, "gate")
	writeBin(t, dir, "npm", "#!/bin/sh\nwhile [ ! -f "+gate+" ] && [ -d "+dir+" ]; do sleep 0.05; done\necho ok\n")
	if _, err := m.UpdateNow("dsh", "an"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !cliuse.Held("dsh") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !cliuse.Held("dsh") {
		t.Fatal("new runs are not held while the CLI is replaced")
	}
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, m, "dsh", "done")
	if cliuse.Held("dsh") {
		t.Fatal("still held after the update")
	}
}

func TestUpdateGivesUpAfterMaxWait(t *testing.T) {
	m, dir := testManager(t)
	m.upPoll, m.upMaxWait = 10*time.Millisecond, 80*time.Millisecond
	npmTool(t, dir, "codex", "@openai/codex", "1.0.0")
	writeBin(t, dir, "npm", "#!/bin/sh\necho \"npm $*\"\n")
	run, _ := cliuse.Begin(context.Background(), "codex")
	defer run()
	var ended atomic.Value
	m.SetOnUpgradeEnd(func(u UpgradeView) { ended.Store(u.State) })
	if _, err := m.UpdateWhenIdle("codex", "an"); err != nil {
		t.Fatal(err)
	}
	v := waitUpgrade(t, m, "codex", "gave_up")
	if v.JobID != "" || ended.Load() != "gave_up" {
		t.Fatalf("gave up = %+v, told %v", v, ended.Load())
	}
	// cancelled while waiting
	m.upMaxWait = time.Hour
	if _, err := m.UpdateWhenIdle("codex", "an"); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelUpgrade("codex"); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, m, "codex", "cancelled")
	if err := m.CancelUpgrade("codex"); err == nil {
		t.Fatal("cancelled twice")
	}
}

// An update that leaves the CLI unable to start (npm cut short: the package's
// binary gone) puts the version it had back.
func TestRollbackOnBrokenUpdate(t *testing.T) {
	m, dir := testManager(t)
	real := npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	writeBin(t, dir, "npm", `#!/bin/sh
echo "npm $*" >> "`+dir+`/npm.log"
case "$3" in
  *@latest) rm -f "`+real+`";;
  *@0.166.0) printf '#!/bin/sh\necho codex-cli 0.166.0\n' > "`+real+`"; chmod +x "`+real+`";;
esac
`)
	var after atomic.Int32
	m.SetAfterUpdate(func(context.Context, string) string { after.Add(1); return "" })
	if _, err := m.UpdateNow("codex", "an"); err != nil {
		t.Fatal(err)
	}
	v := waitUpgrade(t, m, "codex", "rolled_back", "failed")
	if v.State != "rolled_back" || v.From != "0.166.0" || v.To != "0.166.0" || !strings.Contains(v.Note, "codex") {
		t.Fatalf("rollback = %+v", v)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "npm.log"))
	if !strings.Contains(string(log), "@openai/codex@0.166.0") {
		t.Fatalf("npm calls = %s", log)
	}
	if after.Load() != 1 {
		t.Fatalf("connections checked %d times after the rollback", after.Load())
	}
	if !strings.Contains(v.Summary("Codex"), "quay về bản 0.166.0") {
		t.Fatalf("summary = %q", v.Summary("Codex"))
	}
}

// A healthy update keeps the new version and checks the connections (their
// broken mark goes).
func TestNoRollbackWhenHealthy(t *testing.T) {
	m, dir := testManager(t)
	real := npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	writeBin(t, dir, "npm", `#!/bin/sh
echo "npm $*" >> "`+dir+`/npm.log"
printf '#!/bin/sh\necho codex-cli 0.167.0\n' > "`+real+`"
`)
	var after atomic.Int32
	m.SetAfterUpdate(func(_ context.Context, tool string) string {
		if tool == "codex" {
			after.Add(1)
		}
		return ""
	})
	if _, err := m.UpdateWhenIdle("codex", "an"); err != nil {
		t.Fatal(err)
	}
	v := waitUpgrade(t, m, "codex", "done", "failed", "rolled_back")
	if v.State != "done" || v.From != "0.166.0" || v.To != "0.167.0" || after.Load() != 1 {
		t.Fatalf("update = %+v, checked %d", v, after.Load())
	}
	log, _ := os.ReadFile(filepath.Join(dir, "npm.log"))
	if strings.Count(string(log), "npm ") != 1 {
		t.Fatalf("npm ran %q", log)
	}
}

// An update command that runs and exits 0 but leaves the CLI on the same
// version it had, with another install of the same command on PATH, is not
// "done": the one office actually runs may be the stray copy the update
// never touched, not whatever the update command believed it updated.
func TestUpdateFlagsVersionUnchangedWithDuplicate(t *testing.T) {
	m, dir1 := testManager(t)
	npmTool(t, dir1, "codex", "@openai/codex", "0.166.0")
	writeBin(t, dir1, "npm", "#!/bin/sh\n# runs, exits 0, changes nothing\nexit 0\n")
	dir2 := t.TempDir()
	for i, kv := range m.env {
		if after, ok := strings.CutPrefix(kv, "PATH="); ok {
			m.env[i] = "PATH=" + after + string(os.PathListSeparator) + dir2
		}
	}
	writeBin(t, dir2, "codex", "#!/bin/sh\necho codex-cli 0.165.0\n")
	if _, err := m.UpdateNow("codex", "an"); err != nil {
		t.Fatal(err)
	}
	v := waitUpgrade(t, m, "codex", "done", "failed", "rolled_back")
	if v.State != "failed" || v.From != v.To || !strings.Contains(v.Note, "bản khác") {
		t.Fatalf("update shadowed by a duplicate = %+v, want failed naming the other install", v)
	}
}

// Homebrew cannot install an older version: a broken update says how to fix it.
func TestBrokenBrewUpdateTellsTheFix(t *testing.T) {
	m, dir := testManager(t)
	cask := filepath.Join(dir, "Caskroom", "codex", "1.0.0")
	if err := os.MkdirAll(cask, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBin(t, cask, "codex", "#!/bin/sh\necho codex-cli 1.0.0\n")
	if err := os.Symlink(filepath.Join(cask, "codex"), filepath.Join(dir, "codex")); err != nil {
		t.Fatal(err)
	}
	writeBin(t, dir, "brew", "#!/bin/sh\nrm -f \""+filepath.Join(cask, "codex")+"\"\n")
	if _, err := m.UpdateNow("codex", "an"); err != nil {
		t.Fatal(err)
	}
	v := waitUpgrade(t, m, "codex", "failed", "rolled_back")
	if v.State != "failed" || !strings.Contains(v.Note, "brew install --cask codex") {
		t.Fatalf("broken brew update = %+v", v)
	}
}

// A warning printed before the version (Codex in a temporary home) is not
// taken for it.
func TestVersionLineSkipsWarnings(t *testing.T) {
	out := "WARNING: proceeding, even though we could not create PATH aliases\ncodex-cli 0.166.0\n"
	if v := versionLine(out); v != "codex-cli 0.166.0" {
		t.Fatalf("version line = %q", v)
	}
	if v := versionLine("weird\n"); v != "weird" {
		t.Fatalf("no version = %q", v)
	}
}

// When idle, the CLI is taken only while no run has it running: runs that
// keep starting and ending (one right after another) never overlap the update.
func TestUpdateNeverUnderARun(t *testing.T) {
	m, dir := testManager(t)
	m.upPoll, m.upMaxWait = time.Millisecond, 20*time.Second
	npmTool(t, dir, "gemini", "@google/gemini-cli", "1.0.0")
	writeBin(t, dir, "npm", "#!/bin/sh\nsleep 0.3\necho ok\n")
	stop := make(chan struct{})
	var overlap atomic.Bool
	go func() { // a sampler: while held, no run may be counted
		for {
			select {
			case <-stop:
				return
			default:
			}
			if cliuse.Held("gemini") && cliuse.InUse("gemini") > 0 {
				overlap.Store(true)
			}
			time.Sleep(50 * time.Microsecond)
		}
	}()
	go func() { // runs back to back
		for {
			select {
			case <-stop:
				return
			default:
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			done, err := cliuse.Begin(ctx, "gemini")
			cancel()
			if err == nil {
				time.Sleep(2 * time.Millisecond)
				done()
			}
			time.Sleep(time.Millisecond) // the next run starts soon after: idle only briefly
		}
	}()
	if _, err := m.UpdateWhenIdle("gemini", "an"); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, m, "gemini", "done", "failed", "gave_up")
	close(stop)
	if overlap.Load() {
		t.Fatal("a run had the CLI running while its update held it")
	}
}

// The connections' check runs after the hold is let go (it starts the CLI),
// and a check that finds the install broken puts the old version back; the
// check after the rollback runs unheld too.
func TestRollbackWhenConnectionCheckFails(t *testing.T) {
	m, dir := testManager(t)
	real := npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	writeBin(t, dir, "npm", `#!/bin/sh
echo "npm $*" >> "`+dir+`/npm.log"
case "$3" in
  *@latest) printf '#!/bin/sh\necho codex-cli 0.167.0\n' > "`+real+`";;
  *@0.166.0) printf '#!/bin/sh\necho codex-cli 0.166.0\n' > "`+real+`";;
esac
`)
	var checks []string
	m.SetAfterUpdate(func(ctx context.Context, tool string) string {
		// a person's run started while the connections are checked waits
		// for the update to end: a rollback never replaces files under it
		short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		if _, err := cliuse.Begin(short, "codex"); err == nil {
			checks = append(checks, "a run started during the check")
		}
		cancel()
		done, err := cliuse.Begin(ctx, "codex") // the check itself runs the CLI: exempt
		if err != nil {
			checks = append(checks, "held")
			return "check held"
		}
		done()
		v := m.version(tools[1])
		checks = append(checks, v)
		if v == "0.167.0" {
			return "codex: CLI không chạy được: spawn …/codex ENOENT"
		}
		return ""
	})
	if _, err := m.UpdateNow("codex", "an"); err != nil {
		t.Fatal(err)
	}
	v := waitUpgrade(t, m, "codex", "rolled_back", "failed", "done")
	if v.State != "rolled_back" || v.To != "0.166.0" || !strings.Contains(v.Note, "ENOENT") {
		t.Fatalf("update = %+v, checks %v", v, checks)
	}
	if strings.Join(checks, ",") != "0.167.0,0.166.0" {
		t.Fatalf("checks = %v", checks)
	}
	if cliuse.Held("codex") {
		t.Fatal("still held")
	}
}

// A cancel decided while the update waits is never followed by the update:
// either the cancel is refused (it already took the CLI) or nothing runs.
func TestCancelNeverUpdates(t *testing.T) {
	for i := 0; i < 30; i++ {
		m, dir := testManager(t)
		m.upPoll = time.Millisecond
		npmTool(t, dir, "dsh", "@deepseek-ai/dsh", "1.0.0")
		log := filepath.Join(dir, "npm.log")
		writeBin(t, dir, "npm", "#!/bin/sh\necho ran >> "+log+"\n")
		if _, err := m.UpdateWhenIdle("dsh", "an"); err != nil {
			t.Fatal(err)
		}
		cancelled := m.CancelUpgrade("dsh") == nil
		v := waitUpgrade(t, m, "dsh", "done", "cancelled", "failed")
		_, err := os.Stat(log)
		ran := err == nil
		if cancelled && (ran || v.State != "cancelled") {
			t.Fatalf("round %d: cancel accepted but the update ran (%s)", i, v.State)
		}
		if !cancelled && v.State == "cancelled" {
			t.Fatalf("round %d: cancel refused but the update says cancelled", i)
		}
	}
}

// A cancel before the update even looked: nothing runs.
func TestCancelBeforeItStarts(t *testing.T) {
	m, dir := testManager(t)
	m.upPoll = time.Millisecond
	npmTool(t, dir, "dsh", "@deepseek-ai/dsh", "1.0.0")
	log := filepath.Join(dir, "npm.log")
	writeBin(t, dir, "npm", "#!/bin/sh\necho ran >> "+log+"\n")
	run, _ := cliuse.Begin(context.Background(), "dsh")
	if _, err := m.UpdateWhenIdle("dsh", "an"); err != nil {
		t.Fatal(err)
	}
	if err := m.CancelUpgrade("dsh"); err != nil {
		t.Fatal(err)
	}
	run() // idle now: a cancelled update must not take it
	waitUpgrade(t, m, "dsh", "cancelled", "done")
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(log); err == nil {
		t.Fatal("a cancelled update ran")
	}
}

// An update has let the CLI go before it says it ended: while the end is
// told (a bot send of up to 20s), the next "update now" never shares its
// hold, so that one keeps runs back until its own files are in.
func TestHoldGoneWhenUpdateEnds(t *testing.T) {
	m, dir := testManager(t)
	npmTool(t, dir, "dsh", "@deepseek-ai/dsh", "1.0.0")
	writeBin(t, dir, "npm", "#!/bin/sh\necho ok\n")
	var heldAtEnd atomic.Bool
	m.SetOnUpgradeEnd(func(u UpgradeView) { heldAtEnd.Store(cliuse.Held("dsh")) })
	if _, err := m.UpdateNow("dsh", "an"); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, m, "dsh", "done")
	time.Sleep(50 * time.Millisecond)
	if heldAtEnd.Load() {
		t.Fatal("the CLI was still held while its end was told")
	}
}

// A read again that fails keeps the newest version known (the badge, the
// update's target) and is tried again soon, not LatestTTL later.
func TestLatestKeptWhenReadFails(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"version":"3.0.0"}`))
	}))
	defer srv.Close()
	m, dir := testManager(t)
	m.registry = srv.URL
	now := time.Now()
	m.now = func() time.Time { return now }
	npmTool(t, dir, "gemini", "@google/gemini-cli", "2.0.0")
	if _, err := m.LatestNow(context.Background(), "gemini"); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	now = now.Add(LatestTTL + time.Minute)
	if _, err := m.LatestNow(context.Background(), "gemini"); err == nil {
		t.Fatal("a failed read said nothing")
	}
	s, _ := m.Status(context.Background(), "gemini")
	if s.Latest != "3.0.0" || !s.Outdated || m.latestKnown("gemini") != "3.0.0" {
		t.Fatalf("a failed read lost the version known: latest %q outdated %v", s.Latest, s.Outdated)
	}
	// tried again after LatestRetry, not LatestTTL
	m.noFetch = false
	n := calls.Load()
	now = now.Add(LatestRetry + time.Minute)
	m.latestCached("gemini")
	deadline := time.Now().Add(3 * time.Second)
	for calls.Load() == n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() == n {
		t.Fatal("a failed read was not tried again soon")
	}
}

// InstallFixFor reads how the binary itself was installed (updateMethod),
// not a static "recommended" flag: an npm-installed codex (as in the real
// codex-darwin-arm64 ENOENT seen in failure_digest — an npm package missing
// its platform binary) must get the npm fix, a Homebrew one the brew fix.
// Naming the wrong method would install an unused second copy and leave the
// real, broken one in place.
func TestInstallFixFor(t *testing.T) {
	m, _ := testManager(t)
	npm := "/Users/x/.nvm/versions/node/v25.9.0/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/codex/codex"
	if got := m.InstallFixFor(npm); !strings.Contains(got, "npm install -g @openai/codex") {
		t.Fatalf("npm-installed codex: got %q, want the npm command", got)
	}
	if strings.Contains(m.InstallFixFor(npm), "brew") {
		t.Fatalf("npm-installed codex must not get a brew fix")
	}
	brew := "/opt/homebrew/Caskroom/codex/0.166.0/bin/codex"
	if got := m.InstallFixFor(brew); !strings.Contains(got, "brew") || !strings.Contains(got, "codex") {
		t.Fatalf("Homebrew-installed codex: got %q, want the brew cask command", got)
	}
	if strings.Contains(m.InstallFixFor(brew), "npm") {
		t.Fatalf("Homebrew-installed codex must not get an npm fix")
	}
	// a path that says nothing about how it was installed: the Recommended
	// method (claude's is its native installer, not npm)
	if got := m.InstallFixFor("/x/claude"); !strings.Contains(got, "install.sh") {
		t.Fatalf("unresolvable claude path: got %q, want the native installer fallback", got)
	}
	if got := m.InstallFixFor("/x/no-such-tool"); got != "" {
		t.Fatalf("unknown tool: got %q, want \"\"", got)
	}
}

// Duplicates names every other install of the same command on PATH, not
// just the first lookPath returns: the common way a reinstall leaves the
// copy office actually runs untouched and still broken.
func TestDuplicates(t *testing.T) {
	m, dir1 := testManager(t)
	writeBin(t, dir1, "codex", "#!/bin/sh\necho codex-cli 0.166.0\n")
	dir2 := t.TempDir()
	for i, kv := range m.env {
		if after, ok := strings.CutPrefix(kv, "PATH="); ok {
			m.env[i] = "PATH=" + after + string(os.PathListSeparator) + dir2
		}
	}
	writeBin(t, dir2, "codex", "#!/bin/sh\necho codex-cli 0.165.0\n")
	active := filepath.Join(dir1, "codex")
	got := m.Duplicates(active)
	want := filepath.Join(dir2, "codex")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Duplicates(%q) = %v, want [%q]", active, got, want)
	}
	// asked about the other copy instead: it finds dir1's, excluding itself
	if got := m.Duplicates(want); len(got) != 1 || got[0] != active {
		t.Fatalf("Duplicates(%q) = %v, want [%q]", want, got, active)
	}
}
