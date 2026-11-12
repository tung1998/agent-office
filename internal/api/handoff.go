package api

import (
	"net/http"
	"strings"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/storage"
)

// Handing a chat over to a teammate (ADR-277), as on-call shifts change: the
// chat becomes theirs (their bell shows it unread, they may delete it), a
// note says who handed it over and why, and its agent goes on with the same
// session, worktree and diffs: nothing to set up again.

type handoffPerson struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// receivers are the active accounts, other than the person of r, that see
// the project and so may take over its chat.
func (s *server) receivers(r *http.Request, projectID string) ([]handoffPerson, error) {
	users, err := s.cfg.Store.Users().List(r.Context())
	if err != nil {
		return nil, err
	}
	me := userFrom(r).ID
	out := []handoffPerson{}
	for _, u := range users {
		if u.ID == me || u.Disabled {
			continue
		}
		if p, err := s.scopeOf(r.Context(), u); err == nil && p.has(projectID) {
			out = append(out, handoffPerson{Email: u.Email, Name: u.Name})
		}
	}
	return out, nil
}

// handoffChat loads the chat of r and checks the person may hand it over:
// a project chat (not the office assistant's, which are private), theirs or
// one of a project they manage.
func (s *server) handoffChat(w http.ResponseWriter, r *http.Request) (storage.Conversation, bool) {
	c, err := s.cfg.Store.Chat().GetConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return c, false
	}
	if c.ProjectID == assistant.ID(r.Context(), s.cfg.Store) || c.Purpose != "" || c.CreatedBy != "" && !strings.HasPrefix(c.CreatedBy, "human:") {
		writeError(w, http.StatusBadRequest, "chỉ bàn giao được chat của project")
		return c, false
	}
	if u := userFrom(r); !manages(u) && c.CreatedBy != "human:"+u.Email {
		writeError(w, http.StatusForbidden, "chỉ người phụ trách chat hoặc quản lý mới bàn giao được")
		return c, false
	}
	return c, true
}

// GET /api/conversations/{id}/handoff: who may take the chat over.
func (s *server) handoffCandidates(w http.ResponseWriter, r *http.Request) {
	c, ok := s.handoffChat(w, r)
	if !ok {
		return
	}
	people, err := s.receivers(r, c.ProjectID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"owner": strings.TrimPrefix(c.CreatedBy, "human:"), "people": people})
}

// POST /api/conversations/{id}/handoff {"to": email, "note": "..."}.
func (s *server) handoffConversation(w http.ResponseWriter, r *http.Request) {
	in := struct {
		To   string `json:"to"`
		Note string `json:"note"`
	}{}
	if !decode(w, r, &in) {
		return
	}
	c, ok := s.handoffChat(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	to, err := s.cfg.Store.Users().GetByEmail(ctx, strings.TrimSpace(in.To))
	if err != nil || to.Disabled || to.ID == userFrom(r).ID {
		writeError(w, http.StatusBadRequest, "người nhận không hợp lệ")
		return
	}
	if p, err := s.scopeOf(ctx, to); err != nil || !p.has(c.ProjectID) {
		writeError(w, http.StatusBadRequest, "người nhận không thấy project này")
		return
	}
	note := strings.TrimSpace(in.Note)
	if r := []rune(note); len(r) > 2000 {
		note = string(r[:2000])
	}
	from := userFrom(r).Email
	if err := s.cfg.Store.Chat().SetConversationOwner(ctx, c.ID, "human:"+to.Email); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	text := "📋 Bàn giao chat: " + from + " → " + to.Email
	if note != "" {
		text += "\n\n" + note
	}
	text += "\n\n(Người nhận tiếp tục chat này: agent giữ nguyên ngữ cảnh, worktree và diff đang chờ.)"
	if _, err := s.cfg.Store.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Author: from, Content: text}); err != nil {
		s.internal(w, r, err)
		return
	}
	_ = s.cfg.Store.Chat().MarkSeen(ctx, to.ID, c.ID, false) // on their bell
	_ = audit.Record(ctx, s.cfg.Store.Audit(), audit.Change{Action: "conversation.handoff", ResourceID: c.ID, ProjectID: c.ProjectID,
		Detail: map[string]any{"from": from, "to": to.Email}})
	writeJSON(w, http.StatusOK, map[string]any{"owner": to.Email})
}
