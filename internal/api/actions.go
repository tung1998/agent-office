package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// decideAction approves (runs) or rejects an operation an agent proposed.
func (s *server) decideAction(approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r)
		// a provider's card: the key the person pasted goes straight to the change
		var in struct {
			APIKey string `json:"api_key"`
			Always bool   `json:"always"` // run_command: and let its agent run the like of it on its own
			Skip   bool   `json:"skip"`   // rejected, and its agent is not run again about it
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.APIKey != "" {
			r = r.WithContext(context.WithValue(r.Context(), cfgSecretKey{}, strings.TrimSpace(in.APIKey)))
		}
		if cur, err := s.cfg.Store.Actions().Get(r.Context(), r.PathValue("id")); err == nil {
			if approve && cur.Status == "queued" { // approved already: it runs in its turn
				writeJSON(w, http.StatusOK, map[string]any{"action": chat.ToActionDTO(cur)})
				return
			}
			if approve && s.cfg.Chat != nil && cur.Status == "pending" { // in the order its agent proposed them
				var order *chat.OrderError
				if err := s.cfg.Chat.CheckOrder(r.Context(), cur.ConversationID, cur.ID); errors.As(err, &order) && in.APIKey == "" {
					// an earlier one still waits: queued, it runs once that is decided
					a, err := s.cfg.Actions.Queue(r.Context(), cur.ID, u.Email)
					if err != nil {
						s.writeDomainError(w, r, err)
						return
					}
					s.audit(r, audit.Change{Action: "action.queue", Resource: "action", ResourceID: a.ID, ProjectID: a.ProjectID,
						Detail: map[string]any{"kind": a.Kind, "target": a.Target, "after": order.Seq}})
					s.redrawBot(r.Context(), a.ConversationID)
					writeJSON(w, http.StatusOK, map[string]any{"action": chat.ToActionDTO(a), "queued_after": order.Seq})
					return
				} else if err != nil {
					writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "action": chat.ToActionDTO(cur)})
					return
				}
			}
			who := proposerWho(cur, u)
			if !approve { // the person's decision
				who = audit.Who{Kind: "human", ID: u.ID, Name: u.Email, Via: "ui", ConversationID: cur.ConversationID, JobID: cur.JobID, TaskID: cur.TaskID, ActionID: cur.ID}
			}
			r = r.WithContext(audit.With(r.Context(), who))
		}
		var (
			a       storage.Action
			allowed *perm.Allowed
			err     error
		)
		if approve && in.Always {
			var x perm.Allowed
			a, x, err = s.cfg.Actions.DecideAlways(r.Context(), r.PathValue("id"), u.Email)
			allowed = &x
		} else {
			a, err = s.cfg.Actions.Decide(r.Context(), r.PathValue("id"), approve, u.Email)
		}
		if errors.Is(err, perm.ErrNotAlways) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, actions.ErrDecided) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "action": chat.ToActionDTO(a)})
			return
		}
		if err != nil {
			s.writeDomainError(w, r, err)
			return
		}
		skip := !approve && in.Skip
		verb := "action.reject"
		if approve {
			verb = "action.approve"
		} else if skip {
			verb = "action.skip"
		}
		var runErr error
		if a.Status == "failed" {
			runErr = errors.New(a.Detail)
		}
		detail := map[string]any{"kind": a.Kind, "target": a.Target, "status": a.Status}
		if allowed != nil {
			detail["always"] = allowed.Pattern
		}
		s.audit(r, audit.Change{Action: verb, Resource: "action", ResourceID: a.ID, ProjectID: a.ProjectID, Detail: detail, Err: runErr})
		if s.cfg.Chat != nil && !skip { // its agent goes on (ADR-084); skipped, it does not
			label := firstNonEmptyStr(actions.Kinds[a.Kind], a.Kind) + ": " + a.Target
			line := "❌ Đã từ chối " + label
			if approve {
				line = "✅ Đã duyệt " + label + " — " + firstNonEmptyStr(a.Detail, a.Status)
			}
			if allowed != nil {
				line += "\n" + actions.AlwaysNote(*allowed)
			}
			s.cfg.Chat.Decided(a.ConversationID, u.Email, line)
		} else if s.cfg.Chat != nil {
			s.cfg.Chat.Settled(a.ConversationID)
		}
		if s.cfg.Chat != nil { // what was queued behind it runs now, in order
			go s.cfg.Chat.Drain(a.ConversationID)
		}
		s.redrawBot(r.Context(), a.ConversationID)
		out := map[string]any{"action": chat.ToActionDTO(a)}
		if allowed != nil {
			out["always"] = allowed
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// getAction is one card as it stands now (a queued one, watched until it runs).
func (s *server) getAction(w http.ResponseWriter, r *http.Request) {
	a, err := s.cfg.Store.Actions().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": chat.ToActionDTO(a)})
}

// alwaysPreview: what "luôn cho phép" would add for a proposed command, and
// to which pack (empty pattern: it can't be allowed for good).
func (s *server) alwaysPreview(w http.ResponseWriter, r *http.Request) {
	a, err := s.cfg.Store.Actions().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	pattern, ok := perm.SuggestPattern(a.Target)
	if a.Kind != "run_command" || !ok {
		writeJSON(w, http.StatusOK, map[string]any{"pattern": "", "pack": "", "new_pack": false})
		return
	}
	root := ""
	if p, err := s.cfg.Store.Repos().Get(r.Context(), a.ProjectID); err == nil {
		root = p.Path
	}
	label, _, isNew := perm.PlanAlways(root, perm.LoadPolicy(r.Context(), s.cfg.Store, a.ProjectID), pattern)
	writeJSON(w, http.StatusOK, map[string]any{"pattern": pattern, "pack": label, "new_pack": isNew})
}

// proposerWho: an approved proposal is the agent's change, approved by the
// person (ADR-043).
func proposerWho(a storage.Action, u storage.User) audit.Who {
	via := "chat"
	if a.ConversationID == "" && a.TaskID != "" {
		via = "task"
	}
	return audit.Who{Kind: "agent", Name: a.ProposedBy, ApprovedBy: u.Email, Via: via,
		ConversationID: a.ConversationID, JobID: a.JobID, TaskID: a.TaskID, ActionID: a.ID}
}

// runChatCommand: the ▶ of a one-line shell block in a chat (useMarkdown) —
// the person runs the command themselves, in the project folder, without a
// shell (no | ; & > $), as a run_command card on the message it came from.
// Clicking it is the person's decision: the card is approved at once and
// shows the output; no agent turn is started about it.
func (s *server) runChatCommand(w http.ResponseWriter, r *http.Request) {
	ctx, u := r.Context(), userFrom(r)
	var in struct {
		Command   string `json:"command"`
		MessageID string `json:"message_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Command) == "" {
		writeError(w, http.StatusBadRequest, "thiếu lệnh")
		return
	}
	if strings.ContainsAny(strings.TrimSpace(in.Command), "\r\n") {
		writeError(w, http.StatusBadRequest, "chỉ chạy được lệnh một dòng")
		return
	}
	c, err := s.cfg.Store.Chat().GetConversation(ctx, r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if in.MessageID != "" { // the card shows under that message: one of this chat's
		msgs, err := s.cfg.Store.Chat().ListMessages(ctx, c.ID)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if !slices.ContainsFunc(msgs, func(m storage.Message) bool { return m.ID == in.MessageID }) {
			writeError(w, http.StatusBadRequest, "tin nhắn không thuộc cuộc trò chuyện")
			return
		}
	}
	sc := actions.Scope{ProjectID: c.ProjectID, ConversationID: c.ID, Agent: u.Email}
	a, err := s.cfg.Actions.Propose(ctx, sc, "run_command", in.Command, "Người dùng chạy từ khối lệnh trong chat")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if a.Status == "pending" && in.MessageID != "" && a.MessageID == "" {
		a.MessageID = in.MessageID
		if err := s.cfg.Store.Actions().Update(ctx, a); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	if a.Status == "pending" {
		ctx = audit.With(ctx, audit.Who{Kind: "human", ID: u.ID, Name: u.Email, Via: "ui", ConversationID: c.ID, ActionID: a.ID})
		r = r.WithContext(ctx)
		if a, err = s.cfg.Actions.Decide(ctx, a.ID, true, u.Email); errors.Is(err, actions.ErrDecided) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "action": chat.ToActionDTO(a)})
			return
		} else if err != nil {
			s.writeDomainError(w, r, err)
			return
		}
		var runErr error
		if a.Status == "failed" {
			runErr = errors.New(a.Detail)
		}
		s.audit(r, audit.Change{Action: "action.run", Resource: "action", ResourceID: a.ID, ProjectID: a.ProjectID,
			Detail: map[string]any{"kind": a.Kind, "target": a.Target, "status": a.Status}, Err: runErr})
	}
	s.redrawBot(ctx, c.ID)
	writeJSON(w, http.StatusOK, map[string]any{"action": chat.ToActionDTO(a)})
}
