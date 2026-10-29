package chat

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

// beginJob returns the job a chat answer runs as: the one ctx carries (an
// automation's), marked running, or a new one for the person who sent it.
func (e *Engine) beginJob(ctx context.Context, conv storage.Conversation, agentID, title string) (storage.Job, error) {
	now := time.Now().UTC()
	if id := usage.JobFrom(ctx); id != "" {
		return e.store.Jobs().StartIfRunning(ctx, id, conv.ID, agentID, title, now)
	}
	return e.store.Jobs().Create(ctx, storage.Job{ProjectID: conv.ProjectID, Kind: "chat_turn", Origin: "user", Trigger: "ui",
		CreatedBy: actor.From(ctx), ConversationID: conv.ID, AgentID: agentID, Title: title, Status: "running", StartedAt: &now})
}

// endJob finishes the job of an answer (cost and tokens come from its runs);
// ok = it was this call that finished it.
func (e *Engine) endJob(jobID, messageID string, runErr, ctxErr error) (j storage.Job, ok bool) {
	ctx := context.Background()
	if messageID != "" {
		_ = e.store.Jobs().SetMessageID(ctx, jobID, messageID)
	}
	status, code, msg := "done", "", ""
	var be *usage.BudgetError
	switch {
	case errors.Is(ctxErr, context.Canceled):
		status, code = "cancelled", "cancelled"
	case errors.Is(ctxErr, context.DeadlineExceeded):
		status, code = "cancelled", "timeout"
	case runErr != nil && errors.As(runErr, &be):
		status, code, msg = "failed", "budget", runErr.Error()
	case runErr != nil && OutOfTokens(runErr):
		status, code, msg = "failed", "out_of_tokens", runErr.Error()
	case runErr != nil:
		status, code, msg = "failed", "agent_error", runErr.Error()
	}
	// FinishIfRunning, not Finish: a cancel may have already moved the job to
	// "cancelled" (e.g. via FinishIfRunning in cancelJob) while this run was
	// wrapping up; don't let that get overwritten back to done/failed.
	j, err := e.store.Jobs().FinishIfRunning(ctx, jobID, status, code, msg, time.Now().UTC())
	return j, err == nil && j.Status == status
}

// Ended is something that ended in a project, for the automations that
// follow it (ADR-243): a person's turn (turn.*) or a workflow run (workflow.*).
type Ended struct {
	ProjectID, ID  string // ID: the job or run that ended
	Event          string
	From, FromName string // the agent, or the workflow's key and name
	ConversationID string
	Status, Error  string
	Text           string // the answer or the result
}

// SetOnEnded hears turns and workflow runs end.
func (e *Engine) SetOnEnded(fn func(ctx context.Context, x Ended)) { e.onEnded = fn }

// turnEnded tells that a person's turn ended: one an automation ran is its
// own run (automation.*), and a workflow's or an expert's turns are the
// workflow's, so a turn.* automation never answers itself.
func (e *Engine) turnEnded(j storage.Job, conv storage.Conversation, agent storage.Agent, text string) {
	if e.onEnded == nil || j.Origin != "user" || j.Kind != "chat_turn" || conv.Purpose == consultPurpose || e.runOf(conv.ID) != nil {
		return
	}
	ev := "turn.done"
	switch j.Status {
	case "done":
	case "failed":
		ev, text = "turn.failed", j.Error
	default:
		return
	}
	x := Ended{ProjectID: conv.ProjectID, ID: j.ID, Event: ev, From: agent.ID, FromName: agent.Name, ConversationID: conv.ID,
		Status: j.Status, Error: j.Error, Text: text}
	safeGo("onEnded", func() { e.onEnded(context.Background(), x) })
}

// outOfTokens are how the AI services say a run stopped because the account
// ran out of quota or credit (Claude Code's usage limit, an API's 429/quota).
var outOfTokens = []string{
	"usage limit", "limit reached", "hit your limit", "rate limit", "rate_limit", " 429", "quota",
	"credit balance", "insufficient credit", "insufficient_quota", "out of credits", "too many requests",
}

// OutOfTokens: err says the AI connection is out of tokens for now (not a
// bug in the run), so the chat can go on once there are tokens again.
func OutOfTokens(err error) bool {
	s := strings.ToLower(err.Error())
	for _, k := range outOfTokens {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// Stalled: a job's error code that leaves its chat waiting for tokens.
func Stalled(code string) bool { return code == "out_of_tokens" || code == "budget" }
