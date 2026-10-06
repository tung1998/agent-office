package chat

import (
	"context"
	"errors"
	"strings"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/storage"
)

// StartConversationFor picks the agent of a new conversation without storing it.
func (e *Engine) StartConversationFor(ctx context.Context, projectID, agentID string) (storage.Conversation, error) {
	agents, err := e.Agents(ctx, projectID)
	if err != nil {
		return storage.Conversation{}, err
	}
	pick := func(a storage.Agent) storage.Conversation {
		return storage.Conversation{ProjectID: projectID, AgentID: a.ID, AgentName: a.Name, CreatedBy: actor.From(ctx)}
	}
	if agentID == "" { // the first lead that is not paused
		a, err := firstLead(agents)
		return pick(a), err
	}
	for _, a := range agents {
		if a.ID == agentID { // even paused: its first message gets the notice
			return pick(a), nil
		}
	}
	return storage.Conversation{}, ErrNoAgent
}

// StartConversationPurpose opens a thread for a purpose ("automation" = one
// that builds an automation, not listed with the project's chats).
func (e *Engine) StartConversationPurpose(ctx context.Context, projectID, agentID, purpose string) (storage.Conversation, error) {
	c, err := e.StartConversationFor(ctx, projectID, agentID)
	if err != nil {
		return c, err
	}
	c.Purpose = purpose
	return e.store.Chat().CreateConversation(ctx, c)
}

// Conversation is one chat as stored.
func (e *Engine) Conversation(ctx context.Context, id string) (storage.Conversation, error) {
	return e.store.Chat().GetConversation(ctx, id)
}

// ErrCleaned: the chat's data was cleaned up; it takes no more messages.
var ErrCleaned = errors.New("chat này đã được dọn dữ liệu nên không nhắn thêm được: hãy mở chat mới")

// AddTags puts tags on a chat beside its own ("Bug" = "bug"), at most 10 in all.
func (e *Engine) AddTags(ctx context.Context, conversationID string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	c, err := e.store.Chat().GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	all, seen := c.Tags, map[string]bool{}
	for _, t := range all {
		seen[strings.ToLower(t)] = true
	}
	for _, t := range tags {
		if !seen[strings.ToLower(t)] && len(all) < 10 {
			seen[strings.ToLower(t)] = true
			all = append(all, t)
		}
	}
	if len(all) == len(c.Tags) {
		return nil
	}
	return e.store.Chat().SetConversationTags(ctx, conversationID, all)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
