package selfupdate

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// An update starts on its own once the source moved past the running build
// and stopped moving for the settle time; the same HEAD is tried once.
func TestAutoWatchDue(t *testing.T) {
	a := &autoWatch{settle: 5 * time.Minute, tried: map[string]bool{}}
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	if a.due("abc", "abc", t0) || a.due("abc", "abc", t0.Add(time.Hour)) {
		t.Fatal("up to date: no update")
	}
	if a.due("abc", "def", t0) || a.due("abc", "def", t0.Add(4*time.Minute)) {
		t.Fatal("updated before the commits settled")
	}
	if a.due("abc", "ghi", t0.Add(6*time.Minute)) { // another commit landed: the wait starts again
		t.Fatal("a new commit must restart the settle time")
	}
	if !a.due("abc", "ghi", t0.Add(11*time.Minute)) {
		t.Fatal("settled: due")
	}
	if a.due("abc", "ghi", t0.Add(12*time.Minute)) {
		t.Fatal("the same HEAD is tried once")
	}
	if a.due("", "ghi", t0.Add(time.Hour)) || a.due("abc", "", t0.Add(time.Hour)) {
		t.Fatal("unknown revisions: never")
	}
}

// Behind counts the commits the running build has not got.
func TestBehind(t *testing.T) {
	dir := newRepo(t)
	src := Source{Root: dir}
	ctx := context.Background()
	running := run(t, dir, "rev-parse", "HEAD")
	if n := src.Behind(ctx, running); n != 0 {
		t.Fatalf("behind = %d", n)
	}
	write(t, filepath.Join(dir, "b.go"), "package a\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "second")
	write(t, filepath.Join(dir, "c.go"), "package a\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "third")
	if n := src.Behind(ctx, running); n != 2 {
		t.Fatalf("behind = %d, want 2", n)
	}
	if src.Behind(ctx, "") != -1 || src.Behind(ctx, "0123456789abcdef0123456789abcdef01234567") != -1 {
		t.Fatal("an unknown revision cannot be told")
	}
}

// Watch starts a when-idle update when the source moved on and settled.
func TestWatchStartsAWhenIdleUpdate(t *testing.T) {
	dir := newRepo(t)
	running := run(t, dir, "rev-parse", "HEAD")
	write(t, filepath.Join(dir, "b.go"), "package a\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "second")
	u := New(Source{Root: dir}, t.TempDir(), nil, nil)
	u.SetBusy(func(context.Context) string { return "1 lượt chat" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	on := func(context.Context) bool { return true }
	go u.Watch(ctx, on, running, 5*time.Millisecond, 0)
	waitFor(t, "an update", func() bool { return u.State().By == AutoBy })
	st := u.State()
	if !st.WhenIdle {
		t.Fatalf("an update office starts on its own waits for idle: %+v", st)
	}
	_ = u.Cancel()
}
