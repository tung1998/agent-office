package chat

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Only a turn out of the AI's quota waits for its reset: one stopped by the
// office's own daily budget (a cap the person set) is never waited out, nor
// a turn that failed for another reason (ADR-269).
func TestBudgetNotRetried(t *testing.T) {
	e := limitsEngine(t)
	ctx := context.Background()
	p, err := e.store.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := e.store.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a", Name: "A", ModelTier: "balanced"})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := e.store.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, AgentID: ag.ID})
	if err != nil {
		t.Fatal(err)
	}
	for code, want := range map[string]bool{"budget": false, "agent_error": false, OutOfTokensCode: true} {
		now := time.Now().UTC()
		j, err := e.store.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", AgentID: ag.ID, ConversationID: conv.ID,
			Status: "failed", ErrorCode: code, CreatedAt: now, FinishedAt: &now})
		if err != nil {
			t.Fatal(err)
		}
		e.planRetry(j, conv)
		j, _ = e.store.Jobs().Get(ctx, j.ID)
		if (j.RetryAt != nil) != want {
			t.Fatalf("%s: planned=%v, want %v", code, j.RetryAt, want)
		}
		if want && time.Until(*j.RetryAt) < RejectedCooldown-time.Second { // no reset named: the cooldown
			t.Fatalf("%s: planned at %v", code, j.RetryAt)
		}
		e.retries.drop(j.ID)
		_, _ = e.store.Jobs().ClearRetry(ctx, j.ID)
	}
}
