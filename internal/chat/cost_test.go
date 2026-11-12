package chat

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// Claude Code reports a session's total so far: a resumed turn costs what it
// added, not the whole session again (ADR-125).
func TestTurnCostIsWhatTheTurnAdded(t *testing.T) {
	t.Parallel()
	e := limitsEngine(t)
	ctx := context.Background()
	cli := storage.ProviderClaudeCLI
	if got := e.turnCost(ctx, cli, "", "s1", 1.5); got != 1.5 { // a new session: all of it
		t.Fatalf("first turn = %v", got)
	}
	if got := e.turnCost(ctx, cli, "s1", "s1", 1.75); got != 0.25 {
		t.Fatalf("resumed turn = %v, want 0.25", got)
	}
	if got := e.turnCost(ctx, cli, "s1", "s1", 2.0); got != 0.25 {
		t.Fatalf("next turn = %v, want 0.25", got)
	}
	if got := e.turnCost(ctx, cli, "s1", "s2", 0.4); got != 0.4 { // the resume failed: a new session
		t.Fatalf("fresh after a failed resume = %v", got)
	}
	if got := e.turnCost(ctx, cli, "old", "old", 9); got != 0 { // nothing kept: priced from tokens
		t.Fatalf("unknown baseline = %v, want 0", got)
	}
	if got := e.turnCost(ctx, storage.ProviderAnthropic, "x", "x", 0.3); got != 0.3 { // per-call cost already
		t.Fatalf("api = %v", got)
	}
}
