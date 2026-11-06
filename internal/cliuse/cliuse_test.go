package cliuse

import (
	"context"
	"testing"
	"time"
)

func TestCountsRunsPerTool(t *testing.T) {
	ctx := context.Background()
	a, _ := Begin(ctx, "/usr/local/bin/codex")
	b, _ := Begin(ctx, "codex")
	c, _ := Begin(ctx, "/opt/x/claude")
	if InUse("codex") != 2 || InUse("claude") != 1 || InUse("gemini") != 0 {
		t.Fatalf("in use = %d %d %d", InUse("codex"), InUse("claude"), InUse("gemini"))
	}
	a()
	a() // twice: counted once
	b()
	c()
	if InUse("codex") != 0 || InUse("claude") != 0 {
		t.Fatalf("after release = %d %d", InUse("codex"), InUse("claude"))
	}
	if Tool("/bin/agy") != "antigravity" || Tool("git") != "" {
		t.Fatal("tool names")
	}
}

// A run of a held CLI waits for the update; other CLIs do not.
func TestHoldMakesNewRunsWait(t *testing.T) {
	ctx := context.Background()
	release := Hold("gemini", time.Minute)
	if !Held("gemini") || Held("claude") {
		t.Fatal("held")
	}
	other, err := Begin(ctx, "claude") // another CLI starts at once
	if err != nil {
		t.Fatal(err)
	}
	other()
	started := make(chan struct{})
	go func() {
		done, _ := Begin(ctx, "gemini")
		close(started)
		done()
	}()
	select {
	case <-started:
		t.Fatal("a run started under a hold")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the run did not start once the hold was released")
	}
	if Held("gemini") {
		t.Fatal("still held")
	}
}

// A hold that is never released ends at its limit; a run whose ctx ends stops waiting.
func TestHoldHasALimit(t *testing.T) {
	_ = Hold("dsh", 80*time.Millisecond)
	start := time.Now()
	done, err := Begin(context.Background(), "dsh")
	if err != nil || time.Since(start) < 50*time.Millisecond {
		t.Fatalf("err=%v waited=%v", err, time.Since(start))
	}
	done()
	release := Hold("dsh", time.Hour)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Begin(ctx, "dsh"); err == nil {
		t.Fatal("a cancelled run did not stop waiting")
	}
	if InUse("dsh") != 0 {
		t.Fatal("a run that never started is counted")
	}
}
