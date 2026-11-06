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
	release := Hold("gemini")
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

// TryHold takes the CLI only when no run has it running, in one step: a
// run that begins after holds waits; one already running makes it fail.
func TestTryHoldOnlyWhenIdle(t *testing.T) {
	run, _ := Begin(context.Background(), "dsh")
	if _, ok := TryHold("dsh"); ok {
		t.Fatal("held while a run had the CLI running")
	}
	if Held("dsh") {
		t.Fatal("a failed TryHold left a hold")
	}
	run()
	release, ok := TryHold("dsh")
	if !ok {
		t.Fatal("not held once idle")
	}
	if _, ok := TryHold("dsh"); ok {
		t.Fatal("held twice")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Begin(ctx, "dsh"); err == nil || InUse("dsh") != 0 {
		t.Fatal("a run started under the hold")
	}
	release()
	done, err := Begin(context.Background(), "dsh")
	if err != nil {
		t.Fatal(err)
	}
	done()
}

// A hold has no time limit of its own: it lasts as long as the update
// replacing the files (whose commands time out), never ending under it.
func TestHoldLastsUntilReleased(t *testing.T) {
	release := Hold("codex")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := Begin(ctx, "codex"); err == nil {
		t.Fatal("a run started before the hold was released")
	}
	release()
	if Held("codex") {
		t.Fatal("still held")
	}
}

// The update's own check goes through its hold; other runs still wait.
func TestExemptPassesTheHold(t *testing.T) {
	release := Hold("claude")
	defer release()
	done, err := Begin(Exempt(context.Background()), "claude")
	if err != nil || InUse("claude") != 0 {
		t.Fatalf("exempt check: %v, in use %d", err, InUse("claude"))
	}
	done()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Begin(ctx, "claude"); err == nil {
		t.Fatal("a run went through the hold")
	}
}
