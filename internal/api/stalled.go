package api

import (
	"context"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
)

// stalledChats are p's chats whose last answer stopped because the AI ran out
// of tokens (quota, credit or the daily budget) and nobody went on since: one
// each, for an admin or the person who sent it. Tiếp tục sends Prompt.
func (s *server) stalledChats(ctx context.Context, u storage.User, p storage.Repo) []incident {
	failed, err := s.cfg.Store.Jobs().List(ctx, storage.JobFilter{ProjectID: p.ID, Kind: "chat_turn", Status: "failed",
		Since: time.Now().UTC().Add(-7 * 24 * time.Hour), Limit: 200})
	if err != nil {
		return nil
	}
	out := []incident{}
	seen := map[string]bool{}
	for _, j := range failed { // newest first
		if !chat.Stalled(j.ErrorCode) || j.ConversationID == "" || seen[j.ConversationID] {
			continue
		}
		seen[j.ConversationID] = true
		if u.Role != storage.RoleAdmin && j.CreatedBy != "human:"+u.Email {
			continue
		}
		// gone on since (a newer turn, whatever came of it): not stalled any more
		if last, err := s.cfg.Store.Jobs().List(ctx, storage.JobFilter{ConversationID: j.ConversationID, Limit: 1}); err != nil || len(last) == 0 || last[0].ID != j.ID {
			continue
		}
		c, err := s.cfg.Store.Chat().GetConversation(ctx, j.ConversationID)
		if err != nil || c.Cleaned != "" {
			continue
		}
		prompt := "Tiếp tục việc đang dở: lượt trước bị dừng vì hết token."
		if a, err := s.cfg.Store.Agents().Get(ctx, j.AgentID); err == nil && a.ID != c.AgentID {
			prompt = "@" + a.Key + " " + prompt // the agent that stopped, not the chat's own
		}
		out = append(out, incident{Kind: "stalled", Severity: "warning", ProjectID: p.ID, ProjectName: p.Name,
			Title: firstNonEmptyStr(c.Title, j.Title), Detail: j.Error, At: j.CreatedAt,
			Link: "/projects/" + p.ID + "?tab=chat&c=" + c.ID, ID: c.ID, Key: "stalled:" + c.ID, Prompt: prompt})
	}
	return out
}
