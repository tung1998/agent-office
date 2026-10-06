package main

import (
	"context"
	"errors"
	"strings"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
)

// chatDecider decides proposals from a bot's chat (ADR-054) as the dashboard
// does: an approved one is the agent's change, approved by the person in the chat.
type chatDecider struct {
	store storage.Store
	chat  *chat.Engine
	acts  *actions.Service
}

func (d chatDecider) Decide(ctx context.Context, kind, id string, approve bool, by string) (string, error) {
	via, _, _ := strings.Cut(by, ":")
	if kind == "patch" {
		p, err := d.store.Chat().GetPatch(ctx, id)
		if err != nil {
			return "", err
		}
		project, agent := d.placeOf(ctx, p.ConversationID)
		who := audit.Who{Kind: "agent", Name: agent, ApprovedBy: by, Via: via, ConversationID: p.ConversationID}
		if !approve {
			who = audit.Who{Kind: "human", Name: by, Via: via, ConversationID: p.ConversationID}
		}
		ctx = audit.With(ctx, who)
		dto, err := d.chat.DecidePatch(ctx, id, approve)
		if err != nil {
			return "", err
		}
		verb := "patch.reject"
		if approve {
			verb = "patch." + dto.Status
		}
		var failed error
		if dto.Status == "failed" {
			failed = errors.New(dto.Detail)
		}
		_ = audit.Record(ctx, d.store.Audit(), audit.Change{Action: verb, ResourceID: id, ProjectID: project, Detail: map[string]any{"files": dto.Files, "detail": dto.Detail}, Err: failed})
		if failed != nil {
			return "", failed
		}
		return strings.Join(dto.Files, ", "), nil
	}
	cur, err := d.store.Actions().Get(ctx, id)
	if err != nil {
		return "", err
	}
	who := audit.Who{Kind: "agent", Name: cur.ProposedBy, ApprovedBy: by, Via: via, ConversationID: cur.ConversationID, JobID: cur.JobID, TaskID: cur.TaskID, ActionID: cur.ID}
	if !approve {
		who = audit.Who{Kind: "human", Name: by, Via: via, ConversationID: cur.ConversationID, ActionID: cur.ID}
	}
	ctx = audit.With(ctx, who)
	a, err := d.acts.Decide(ctx, id, approve, by)
	if err != nil {
		return "", err
	}
	verb := "action.reject"
	if approve {
		verb = "action.approve"
	}
	var runErr error
	if a.Status == "failed" {
		runErr = errors.New(a.Detail)
	}
	_ = audit.Record(ctx, d.store.Audit(), audit.Change{Action: verb, Resource: "action", ResourceID: a.ID, ProjectID: a.ProjectID,
		Detail: map[string]any{"kind": a.Kind, "target": a.Target, "status": a.Status}, Err: runErr})
	if runErr != nil {
		return "", runErr
	}
	return a.Detail, nil
}

// DecideAlways approves a command and lets its agent run the like of it on
// its own from now on ("♾️ Luôn cho phép").
func (d chatDecider) DecideAlways(ctx context.Context, id, by string) (string, error) {
	cur, err := d.store.Actions().Get(ctx, id)
	if err != nil {
		return "", err
	}
	via, _, _ := strings.Cut(by, ":")
	ctx = audit.With(ctx, audit.Who{Kind: "agent", Name: cur.ProposedBy, ApprovedBy: by, Via: via, ConversationID: cur.ConversationID, JobID: cur.JobID, TaskID: cur.TaskID, ActionID: cur.ID})
	a, allowed, err := d.acts.DecideAlways(ctx, id, by)
	if err != nil { // refused before anything ran
		return "", err
	}
	var runErr error
	if a.Status == "failed" {
		runErr = errors.New(a.Detail)
	}
	_ = audit.Record(ctx, d.store.Audit(), audit.Change{Action: "action.approve", Resource: "action", ResourceID: a.ID, ProjectID: a.ProjectID,
		Detail: map[string]any{"kind": a.Kind, "target": a.Target, "status": a.Status, "always": allowed.Pattern}, Err: runErr})
	if runErr != nil {
		return "", runErr
	}
	return a.Detail + "\n" + actions.AlwaysNote(allowed), nil
}

// placeOf: a conversation's project and agent.
func (d chatDecider) placeOf(ctx context.Context, conversationID string) (string, string) {
	c, err := d.store.Chat().GetConversation(ctx, conversationID)
	if err != nil {
		return "", ""
	}
	return c.ProjectID, c.AgentName
}
