package clitools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/cliuse"
)

// A version whose update was put back stays bad after office restarts: the
// setting keeps it, so auto mode does not install the broken version again
// (ADR-259). A later update that lands on it by hand clears it.
func TestBadVersionSurvivesRestart(t *testing.T) {
	m, dir := testManager(t)
	m.registry = registry(t, map[string]string{"@openai/codex": "0.167.0"}).URL
	real := npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	writeBin(t, dir, "npm", `#!/bin/sh
echo "npm $*" >> "`+dir+`/npm.log"
case "$3" in
  *@latest) rm -f "`+real+`";;
  *@0.166.0) printf '#!/bin/sh\necho codex-cli 0.166.0\n' > "`+real+`"; chmod +x "`+real+`";;
esac
`)
	x := &memSetting{s: AutoSetting{Mode: AutoUpdate}}
	ctx := context.Background()
	m.WatchOnce(ctx, x.hooks())
	if v := waitUpgrade(t, m, "codex", "rolled_back", "failed", "done"); v.State != "rolled_back" {
		t.Fatalf("update = %+v", v)
	}
	m.forgetLatest("codex")
	m.WatchOnce(ctx, x.hooks()) // the next look keeps what was put back in the setting
	if x.s.Bad["codex"] != "0.167.0" {
		t.Fatalf("the bad version is not kept: %+v", x.s)
	}

	// office restarts: a new manager, the same setting
	m2, dir2 := testManager(t)
	m2.registry = m.registry
	npmTool(t, dir2, "codex", "@openai/codex", "0.166.0")
	writeBin(t, dir2, "npm", "#!/bin/sh\necho \"npm $*\" >> \""+dir2+"/npm.log\"\n")
	m2.WatchOnce(ctx, x.hooks())
	if u, ok := m2.Upgrade("codex"); ok {
		t.Fatalf("the broken version was installed again after a restart: %+v", u)
	}
	if b, _ := os.ReadFile(filepath.Join(dir2, "npm.log")); len(b) != 0 {
		t.Fatalf("npm ran: %s", b)
	}

	// updated by hand to that version, which now starts: no longer bad
	real2 := filepath.Join(dir2, "node_modules", "@openai/codex", "bin", "codex")
	writeBin(t, dir2, "npm", "#!/bin/sh\nprintf '#!/bin/sh\\necho codex-cli 0.167.0\\n' > \""+real2+"\"\n")
	if _, err := m2.UpdateNow("codex", "an"); err != nil {
		t.Fatal(err)
	}
	if v := waitUpgrade(t, m2, "codex", "done", "failed", "rolled_back"); v.State != "done" {
		t.Fatalf("update by hand = %+v", v)
	}
	m2.WatchOnce(ctx, x.hooks())
	if _, ok := x.s.Bad["codex"]; ok {
		t.Fatalf("a version that now starts stayed bad: %+v", x.s)
	}
}

// An install broken outside office is reinstalled once in auto mode; a
// reinstall that does not fix it is not tried again on every look (it was
// told), and it does not mark the newest version bad: the break was not that
// version's. Once it starts again, a new break is reinstalled again.
func TestAutoReinstallsBrokenInstallOnce(t *testing.T) {
	m, dir := testManager(t)
	m.registry = registry(t, map[string]string{"@openai/codex": "0.167.0"}).URL
	real := npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	writeBin(t, dir, "npm", "#!/bin/sh\necho \"npm $*\" >> \""+dir+"/npm.log\"\n") // does not fix it
	broken := "#!/bin/sh\necho 'Error: spawn /x/vendor/codex ENOENT' >&2\nexit 1\n"
	if err := os.WriteFile(real, []byte(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	x := &memSetting{s: AutoSetting{Mode: AutoUpdate}}
	ctx := context.Background()
	m.WatchOnce(ctx, x.hooks())
	if v := waitUpgrade(t, m, "codex", "failed", "rolled_back", "done"); v.State != "failed" {
		t.Fatalf("reinstall = %+v", v)
	}
	first, _ := m.Upgrade("codex")
	for range 3 {
		m.WatchOnce(ctx, x.hooks())
	}
	if u, _ := m.Upgrade("codex"); !u.Started.Equal(first.Started) {
		t.Fatalf("the broken install was reinstalled again: %+v", u)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "npm.log")); strings.Count(string(b), "npm ") != 1 {
		t.Fatalf("npm calls = %s", b)
	}
	if len(x.told) != 1 {
		t.Fatalf("told = %q", x.told)
	}
	m.mu.Lock()
	bad := m.bad["codex"]
	m.mu.Unlock()
	if bad != "" || x.s.Bad["codex"] != "" {
		t.Fatalf("a break from outside marked %q bad", bad)
	}

	// it starts again, then breaks again: reinstalled again
	npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	m.forgetLatest("codex")
	m.registry = registry(t, map[string]string{"@openai/codex": "0.166.0"}).URL
	m.WatchOnce(ctx, x.hooks())
	if _, ok := x.s.Repair["codex"]; ok {
		t.Fatalf("the reinstall mark stayed once it started: %+v", x.s)
	}
	_ = os.WriteFile(real, []byte(broken), 0o755)
	m.WatchOnce(ctx, x.hooks())
	if u := waitUpgrade(t, m, "codex", "failed", "done", "rolled_back"); u.Started.Equal(first.Started) {
		t.Fatalf("a new break was not reinstalled: %+v", u)
	}
}

// A CLI whose runs follow each other with no gap is never idle: after a
// while the update holds new runs back and waits only for those running, so
// it is not given up after UpgradeMaxWait. A drain that takes too long lets
// the runs go again and the update goes back to waiting.
func TestIdleWaitDrainsAfterGrace(t *testing.T) {
	m, dir := testManager(t)
	real := npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	writeBin(t, dir, "npm", "#!/bin/sh\nprintf '#!/bin/sh\\necho codex-cli 0.167.0\\n' > \""+real+"\"\n")
	m.upPoll, m.upMaxWait = 5*time.Millisecond, 3*time.Second
	m.upGrace, m.upDrainMax = 100*time.Millisecond, 2*time.Second
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	// runs of 20ms started every 10ms: two overlap, one is always running
	first, err := cliuse.Begin(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
		first()
		for ctx.Err() == nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				end, err := cliuse.Begin(ctx, "codex")
				if err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
				end()
			}()
			time.Sleep(10 * time.Millisecond)
		}
	}()
	time.Sleep(30 * time.Millisecond)
	if _, err := m.UpdateWhenIdle("codex", "an"); err != nil {
		t.Fatal(err)
	}
	v := waitUpgrade(t, m, "codex", "done", "gave_up", "failed", "rolled_back")
	stop()
	wg.Wait()
	if v.State != "done" || v.To != "0.167.0" {
		t.Fatalf("update of a CLI never idle = %+v", v)
	}
	if cliuse.Held("codex") {
		t.Fatal("runs still held after the update")
	}
}

// A drain whose runs do not end in upDrainMax lets new runs go again.
func TestIdleDrainLetsRunsGo(t *testing.T) {
	m, dir := testManager(t)
	npmTool(t, dir, "codex", "@openai/codex", "0.166.0")
	m.upPoll, m.upMaxWait = 5*time.Millisecond, 400*time.Millisecond
	m.upGrace, m.upDrainMax = 50*time.Millisecond, 100*time.Millisecond
	end, _ := cliuse.Begin(context.Background(), "codex") // a long run
	defer end()
	if _, err := m.UpdateWhenIdle("codex", "an"); err != nil {
		t.Fatal(err)
	}
	sawHeld, sawFree := false, false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		u, _ := m.Upgrade("codex")
		if !u.Active() {
			break
		}
		if cliuse.Held("codex") {
			sawHeld = true
		} else if sawHeld {
			sawFree = true
		}
		time.Sleep(2 * time.Millisecond)
	}
	if v, _ := m.Upgrade("codex"); v.State != "gave_up" || !sawHeld || !sawFree {
		t.Fatalf("drain: %+v held=%v freed=%v", v, sawHeld, sawFree)
	}
	if cliuse.Held("codex") {
		t.Fatal("runs held after it gave up")
	}
}
