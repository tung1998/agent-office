package chat

import (
	"context"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
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

// ReviewBeforeDone (tests only): reviewBeforeDone at the end of run jobID.
func (e *Engine) ReviewBeforeDone(ctx context.Context, conv storage.Conversation, agent storage.Agent, jobID, answer string) string {
	return e.reviewBeforeDone(ctx, &Turn{JobID: jobID, wake: make(chan struct{})}, conv, agent, "", answer)
}

// ToolFailed (tests only): a CLI's own tool failed in run jobID, as its
// runner tells it; shown is what reached the chat.
func (e *Engine) ToolFailed(ctx context.Context, conv storage.Conversation, agent storage.Agent, jobID string, ev Event) (shown []Event) {
	e.watchFails(ctx, &Turn{JobID: jobID, wake: make(chan struct{})}, conv, agent, "", func(ev Event) { shown = append(shown, ev) })(ev)
	return shown
}
