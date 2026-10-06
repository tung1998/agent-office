package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
		}
		s.redrawBot(r.Context(), a.ConversationID)
		out := map[string]any{"action": chat.ToActionDTO(a)}
		if allowed != nil {
			out["always"] = allowed
		}
		writeJSON(w, http.StatusOK, out)
	}
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
