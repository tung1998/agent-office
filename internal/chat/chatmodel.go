package chat

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/tung1998/agent-office/internal/scoring"
	"github.com/tung1998/agent-office/internal/storage"
)

// ErrUnknownModel: the model is not one the agent's connection offers.
var ErrUnknownModel = errors.New("model không có trong kết nối của agent")

// offers tells whether a connection offers model (its fetched list or one
// of its tier models).
func offers(p storage.Provider, model string) bool {
	if slices.Contains(p.Models, model) {
		return true
	}
	for _, m := range p.TierModels {
		if m == model {
			return true
		}
	}
	return false
}

// SetMemberModel sets the chat's own model for one of its agents ("" = the
// agent's own, ADR-267), from the next answer on. The model must be one the
// agent's own connection offers; an agent not in the chat yet joins with it.
func (e *Engine) SetMemberModel(ctx context.Context, conversationID, agentID, model string) error {
	conv, err := e.store.Chat().GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	agent, err := e.store.Agents().Get(ctx, agentID)
	if err != nil {
		return err
	}
	if agent.ProjectID != conv.ProjectID {
		return storage.ErrNotFound
	}
	if model != "" {
		p, _, err := e.providers.ResolveModel(ctx, agent)
		if err != nil {
			return err
		}
		if !offers(p, model) {
			return ErrUnknownModel
		}
	}
	err = e.store.Chat().SetMemberModel(ctx, conv.ID, agent.ID, model)
	if errors.Is(err, storage.ErrNotFound) {
		err = e.store.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: conv.ID, AgentID: agent.ID, AgentName: agent.Name, LLMModel: model})
	}
	return err
}

// chatAgent is agent as this turn of the chat runs it: on the chat's own
// model for it when there is one its connection still offers. Work handed
// over, a workflow's seat or step and an automation's chat run as the agent
// (or the automation) set them.
func (e *Engine) chatAgent(ctx context.Context, conv storage.Conversation, turn *Turn, agent storage.Agent) storage.Agent {
	a := withTier(ctx, agent)
	if turn.delegator != "" || turn.wfRole != "" || turn.wfStep != "" || conv.Purpose == "automation" {
		return a
	}
	m := e.member(ctx, conv, agent).LLMModel
	if m == "" {
		return a
	}
	if p, _, err := e.providers.ResolveModel(ctx, a); err != nil || !offers(p, m) {
		return a
	}
	a.LLMModel = m
	return a
}

func (e *Engine) scores() scoring.Recorder { return scoring.Recorder{St: e.store} }

// scoreEnded scores a finished job for the model/effort statistics (ADR-267):
// a person's turn, a Burn piece's work or a workflow's; never an expert's
// answer to another office.
func (e *Engine) scoreEnded(j storage.Job, conv storage.Conversation, turn *Turn, request string, files int) {
	if j.Kind != "chat_turn" || conv.Purpose == consultPurpose {
		return
	}
	src := scoring.Source{Purpose: conv.Purpose, Burn: conv.Purpose == BurnWorkPurpose,
		Workflow: conv.Purpose == RunPurpose || turn.wfRole != "" || turn.wfStep != "", Files: files}
	if err := e.scores().JobEnded(context.Background(), j, request, src); err != nil {
		slog.Warn("chat: score job", "job", j.ID, "err", err)
	}
}

// Quotas is the room each connection has right now, for suggestions
// (ADR-267): over = past its limit, the person's cap (ADR-136) or broken.
func (e *Engine) Quotas(ctx context.Context, ps []storage.Provider) map[string]scoring.Quota {
	now := time.Now()
	out := map[string]scoring.Quota{}
	for _, p := range ps {
		l, has := e.limits(ctx, p.ID)
		util := map[string]float64{}
		var resets time.Time
		for name, w := range l.Windows {
			util[name] = w.Utilization
			if w.ResetsAt.After(now) && (resets.IsZero() || w.ResetsAt.Before(resets)) {
				resets = w.ResetsAt
			}
		}
		over := capHit(p, l, has, now) != nil || (has && overNow(l, now))
		out[p.ID] = scoring.QuotaOf(p, util, over, resets, now)
	}
	return out
}

// ScoreRecorder writes and refreshes the job scores (ADR-267).
func (e *Engine) ScoreRecorder() scoring.Recorder { return e.scores() }
