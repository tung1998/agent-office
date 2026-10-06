package api

import (
	"context"
	"net/http"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/audit"
)

// botRedrawer takes a decided proposal's buttons off its bot message.
type botRedrawer interface {
	Redraw(ctx context.Context, conversationID string)
}

// redrawBot: what was decided on the dashboard leaves no button to press on
// the bot's message (its agent is not run again for it here).
func (s *server) redrawBot(ctx context.Context, conversationID string) {
	if conversationID == "" {
		return
	}
	if rd, ok := s.cfg.Channels.(botRedrawer); ok {
		go rd.Redraw(context.WithoutCancel(ctx), conversationID)
	}
}

// skipAllProposals ("Bỏ qua tất cả"): every action and diff waiting that the
// overview lists (of one project, if given) is rejected, and no agent is run
// again about them.
func (s *server) skipAllProposals(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectID string `json:"project_id"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	u := userFrom(r)
	hidden := assistant.ID(ctx, s.cfg.Store)
	convs := map[string]bool{}
	actionsN, patchesN := 0, 0
	tried := map[string]bool{} // one try each: a failed one is not picked again
	for range 20 {             // 100 at a time
		progress := false
		if acts, err := s.cfg.Store.Actions().Pending(ctx, 100); err == nil {
			for _, a := range acts {
				if tried[a.ID] || in.ProjectID != "" && a.ProjectID != in.ProjectID {
					continue
				}
				tried[a.ID], progress = true, true
				who := audit.Who{Kind: "human", ID: u.ID, Name: u.Email, Via: "ui", ConversationID: a.ConversationID, JobID: a.JobID, TaskID: a.TaskID, ActionID: a.ID}
				if _, err := s.cfg.Actions.Decide(audit.With(ctx, who), a.ID, false, u.Email); err == nil {
					actionsN++
					convs[a.ConversationID] = true
				}
			}
		}
		if s.cfg.Chat != nil {
			if ps, err := s.cfg.Store.Chat().PendingPatches(ctx, 100); err == nil {
				for _, p := range ps {
					if tried[p.ID] {
						continue
					}
					c, err := s.cfg.Store.Chat().GetConversation(ctx, p.ConversationID)
					if err != nil || c.ProjectID == hidden || in.ProjectID != "" && c.ProjectID != in.ProjectID {
						continue
					}
					tried[p.ID], progress = true, true
					who := audit.Who{Kind: "human", ID: u.ID, Name: u.Email, Via: "ui", ConversationID: p.ConversationID, TaskID: p.TaskID}
					if _, err := s.cfg.Chat.DecidePatch(audit.With(ctx, who), p.ID, false); err == nil {
						patchesN++
						convs[p.ConversationID] = true
					}
				}
			}
		}
		if !progress {
			break
		}
	}
	n := actionsN + patchesN
	if n > 0 {
		s.audit(r, audit.Change{Action: "proposal.skip_all", Resource: "proposal", ProjectID: in.ProjectID,
			Detail: map[string]any{"count": n, "actions": actionsN, "patches": patchesN}})
	}
	for c := range convs {
		s.redrawBot(ctx, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"skipped": n, "actions": actionsN, "patches": patchesN})
}
