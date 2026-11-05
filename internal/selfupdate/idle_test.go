package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// built makes a source whose update already built: bin/office and the new
// bin/office.new, as run leaves them before settle.
func built(t *testing.T) Source {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"office": "old", "office.new": "new"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Source{Root: root}
}

func binary(t *testing.T, src Source) string {
	t.Helper()
	raw, err := os.ReadFile(src.Binary())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// a busy func that says "1 quy trình (X)" while busy is true
type fakeBusy struct{ busy atomic.Bool }

func (f *fakeBusy) fn(context.Context) string {
	if f.busy.Load() {
		return "1 quy trình (Nâng cấp hằng ngày)"
	}
	return ""
}

func idleUpdater(t *testing.T, src Source, b *fakeBusy) (*Updater, *atomic.Int32) {
	var restarts atomic.Int32
	u := New(src, t.TempDir(), nil, func() { restarts.Add(1) })
	u.poll = 5 * time.Millisecond
	u.SetBusy(b.fn)
	return u, &restarts
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// While work runs, a when-idle update keeps the running build; once nothing
// runs it swaps the new build in and restarts (ADR-255).
func TestWhenIdleWaitsThenSwaps(t *testing.T) {
	src := built(t)
	b := &fakeBusy{}
	b.busy.Store(true)
	u, restarts := idleUpdater(t, src, b)
	done := make(chan struct{})
	go func() { u.settle(Options{WhenIdle: true, By: "admin@x.io"}); close(done) }()
	waitFor(t, "waiting", u.Waiting)
	st := u.State()
	if st.Busy == "" || st.WaitUntil == nil || st.WaitingSince == nil {
		t.Fatalf("waiting state = %+v", st)
	}
	time.Sleep(30 * time.Millisecond)
	if binary(t, src) != "old" || restarts.Load() != 0 {
		t.Fatal("swapped while work was running")
	}
	b.busy.Store(false)
	<-done
	if binary(t, src) != "new" || u.State().Status != "restarting" {
		t.Fatalf("binary %q, state %+v", binary(t, src), u.State())
	}
	waitFor(t, "restart", func() bool { return restarts.Load() == 1 })
	if why := TakeRestart(u.home); !strings.Contains(why, "khi office rảnh") || !strings.Contains(why, "admin@x.io") {
		t.Fatalf("restart reason = %q", why)
	}
	if TakeRestart(u.home) != Unexpected {
		t.Fatal("the reason is read once")
	}
}

// Past its MaxWait the update is dropped: the running build stays, nothing
// is cut off.
func TestWhenIdleGivesUpAfterMaxWait(t *testing.T) {
	src := built(t)
	b := &fakeBusy{}
	b.busy.Store(true)
	u, restarts := idleUpdater(t, src, b)
	var mu sync.Mutex
	clock := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	u.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	done := make(chan struct{})
	go func() { u.settle(Options{WhenIdle: true, MaxWait: time.Hour}); close(done) }()
	waitFor(t, "waiting", u.Waiting)
	mu.Lock()
	clock = clock.Add(61 * time.Minute)
	mu.Unlock()
	<-done
	st := u.State()
	if st.Status != "failed" || !st.TimedOut || !strings.Contains(st.Error, "giữ nguyên") {
		t.Fatalf("state = %+v", st)
	}
	if binary(t, src) != "old" || restarts.Load() != 0 {
		t.Fatal("swapped after giving up")
	}
	if _, err := os.Stat(filepath.Join(src.Root, "bin", "office.new")); !os.IsNotExist(err) {
		t.Fatal("the dropped build is left behind")
	}
	if r := LoadResult(u.home); r == nil || r.State != "failed" {
		t.Fatalf("result = %+v", r)
	}
}

// Cancel drops a waiting update; Hurry swaps it in now.
func TestWhenIdleCancelAndHurry(t *testing.T) {
	src := built(t)
	b := &fakeBusy{}
	b.busy.Store(true)
	u, restarts := idleUpdater(t, src, b)
	if err := u.Cancel(); err != ErrNothing {
		t.Fatalf("cancel with nothing waiting = %v", err)
	}
	u.state.Status = "running" // as Start leaves it
	done := make(chan struct{})
	go func() { u.settle(Options{WhenIdle: true}); close(done) }()
	waitFor(t, "waiting", u.Waiting)
	if err := u.Start(Options{}); err != ErrBusy {
		t.Fatalf("a second update while one waits = %v", err)
	}
	if err := u.Cancel(); err != nil {
		t.Fatal(err)
	}
	<-done
	if st := u.State(); st.Status != "cancelled" || binary(t, src) != "old" || restarts.Load() != 0 {
		t.Fatalf("after cancel: state %+v, binary %q", st, binary(t, src))
	}

	src = built(t)
	u, restarts = idleUpdater(t, src, b)
	u.state.Status = "running"
	done = make(chan struct{})
	go func() { u.settle(Options{WhenIdle: true}); close(done) }()
	waitFor(t, "waiting", u.Waiting)
	if err := u.Hurry(); err != nil {
		t.Fatal(err)
	}
	<-done
	if binary(t, src) != "new" {
		t.Fatal("hurry did not swap")
	}
	waitFor(t, "restart", func() bool { return restarts.Load() == 1 })
	if why := TakeRestart(u.home); !strings.Contains(why, "ngắt việc đang chạy") {
		t.Fatalf("a hurried restart says what it cut: %q", why)
	}
}

// A build that fails never waits: the update ends failed at once.
func TestWhenIdleBuildFailureDoesNotWait(t *testing.T) {
	b := &fakeBusy{}
	b.busy.Store(true)
	u, restarts := idleUpdater(t, Source{Root: t.TempDir()}, b) // no go.mod: go build fails
	if err := u.Start(Options{WhenIdle: true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the build to fail", func() bool { return u.State().Status == "failed" })
	if restarts.Load() != 0 || u.State().WaitingSince != nil || u.State().TimedOut {
		t.Fatalf("state = %+v", u.State())
	}
}

// Without a busy func (or not asked to), an update swaps at once as before.
func TestNotWhenIdleSwapsAtOnce(t *testing.T) {
	src := built(t)
	b := &fakeBusy{}
	b.busy.Store(true)
	u, _ := idleUpdater(t, src, b)
	u.settle(Options{})
	if binary(t, src) != "new" {
		t.Fatal("an update now must not wait")
	}
}
