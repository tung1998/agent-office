package api

import (
	"context"
	"errors"
	"net/http"
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
		if !manages(u) && j.CreatedBy != "human:"+u.Email {
			continue
		}
		// gone on since (a newer turn, whatever came of it): not stalled any more
		if last, err := s.cfg.Store.Jobs().List(ctx, storage.JobFilter{ConversationID: j.ConversationID, Limit: 1}); err != nil || len(last) == 0 || last[0].ID != j.ID {
			continue
		}
		c, err := s.cfg.Store.Chat().GetConversation(ctx, j.ConversationID)
		if err != nil || c.Cleaned != "" || s.burnOwned(ctx, c) {
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

// burnOwned: a chat a Burn runs (its own, a piece's, a review's, or a
// workflow run one of them called). The Burn goes on there by itself once
// the tokens are back (ADR-124): nothing for the person to do.
func (s *server) burnOwned(ctx context.Context, c storage.Conversation) bool {
	for range 4 { // a run called from a run called from a Burn's review…
		switch c.Purpose {
		case "burn", chat.BurnWorkPurpose, chat.BurnReviewPurpose:
			return true
		case chat.RunPurpose:
		default:
			return false
		}
		runs, err := s.cfg.Store.WorkflowRuns().List(ctx, c.ProjectID, c.ID, 5)
		if err != nil {
			return false
		}
		caller := ""
		for _, r := range runs {
			if r.ConversationID == c.ID {
				caller = r.CallerConversationID
				break
			}
		}
		if caller == "" {
			return false
		}
		if c, err = s.cfg.Store.Chat().GetConversation(ctx, caller); err != nil {
			return false
		}
	}
	return false
}

// retryingChats are p's chats whose last answer died on a passing API failure
// and goes on by itself later (ADR-260): when, which try; Chạy ngay or Thôi.
func (s *server) retryingChats(ctx context.Context, u storage.User, p storage.Repo) []incident {
	jobs, err := s.cfg.Store.Jobs().Retries(ctx)
	if err != nil {
		return nil
	}
	out := []incident{}
	for _, j := range jobs {
		if j.ProjectID != p.ID || j.ConversationID == "" || j.Kind != "chat_turn" || (!manages(u) && j.CreatedBy != "human:"+u.Email) {
			continue
		}
		r := s.cfg.Chat.PlannedRetry(ctx, j.ConversationID)
		if r == nil || r.JobID != j.ID {
			continue // not the chat's last turn any more
		}
		c, err := s.cfg.Store.Chat().GetConversation(ctx, j.ConversationID)
		if err != nil {
			continue
		}
		out = append(out, incident{Kind: "retrying", Severity: "info", ProjectID: p.ID, ProjectName: p.Name,
			Title: firstNonEmptyStr(c.Title, j.Title), Detail: j.Error, At: r.At,
			Link: "/projects/" + p.ID + "?tab=chat&c=" + c.ID, ID: c.ID, Key: "retrying:" + j.ID})
	}
	return out
}

// retryNow goes on now with the turn a chat planned to go on with later.
func (s *server) retryNow(w http.ResponseWriter, r *http.Request) {
	p := s.cfg.Chat.PlannedRetry(r.Context(), r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, chat.ErrNoRetry.Error())
		return
	}
	t, err := s.cfg.Chat.RetryNow(r.Context(), p.JobID)
	switch {
	case errors.Is(err, chat.ErrNoRetry):
		writeError(w, http.StatusNotFound, err.Error())
	case err != nil:
		writeError(w, http.StatusConflict, err.Error())
	default:
		turn := ""
		if t != nil {
			turn = t.ID
		}
		writeJSON(w, http.StatusOK, map[string]any{"turn_id": turn})
	}
}

// cancelRetry calls a chat's planned go-on off.
func (s *server) cancelRetry(w http.ResponseWriter, r *http.Request) {
	p := s.cfg.Chat.PlannedRetry(r.Context(), r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, chat.ErrNoRetry.Error())
		return
	}
	if err := s.cfg.Chat.CancelRetry(r.Context(), p.JobID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}
