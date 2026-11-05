package chat

import (
	"context"
	"time"
)

// SetTestStepPanic (tests only): runSteps' per-step goroutine panics
// instead of running the step with this id — forces that path
// deterministically to check a step panic doesn't hang the run.
func (e *Engine) SetTestStepPanic(stepID string) { e.testStepPanic = stepID }

// SetBeforePatchSave (tests only): fn runs once a turn's new diff is made,
// before it is saved; the returned func puts the hook back.
func SetBeforePatchSave(fn func(conversationID string)) func() {
	old := beforePatchSave
	beforePatchSave = fn
	return func() { beforePatchSave = old }
}

// TestTurn (tests only): a turn in progress, as an agent's tool call names
// it (Scope.RunRef), for the person actor.
func (e *Engine) TestTurn(id, conversationID, actor, agentID, agentName string) {
	e.mu.Lock()
	e.turns[id] = &Turn{ID: id, ConversationID: conversationID, actor: actor, agentID: agentID, agentName: agentName, wake: make(chan struct{})}
	e.mu.Unlock()
}

// SweepOrphans (tests only): sweepOrphans with its age.
func (e *Engine) SweepOrphans(ctx context.Context, age time.Duration) int {
	return e.sweepOrphans(ctx, age)
}
