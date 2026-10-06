package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/attach"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/channels"
	"github.com/tung1998/agent-office/internal/perm"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

type conversationDTO struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	AgentID      string    `json:"agent_id"`
	AgentName    string    `json:"agent_name"`
	Title        string    `json:"title"`
	CreatedBy    string    `json:"created_by"`
	UpdatedAt    time.Time `json:"updated_at"`
	ActiveTurn   string    `json:"active_turn,omitempty"`
	Mode         string    `json:"mode"`
	EditMode     string    `json:"edit_mode"`
	Effort       string    `json:"effort"`  // the chat's thinking level ("" = its agent's)
	Cleaned      string    `json:"cleaned"` // data cleanup: content | summary ("" = as it was); takes no more messages
	Purpose      string    `json:"purpose"`
	Source       string    `json:"source"` // where it started: web | discord | telegram | auto
	AutomationID string    `json:"automation_id"`
	// how full the model's context was after the last answer (0 = unknown)
	ContextTokens int      `json:"context_tokens"`
	ContextWindow int      `json:"context_window"`
	ExternalURL   string   `json:"external_url,omitempty"` // where it is on Discord: its thread, or its first message
	Tags          []string `json:"tags"`
}

func (s *server) toConvDTO(c storage.Conversation) conversationDTO {
	d := conversationDTO{ID: c.ID, ProjectID: c.ProjectID, AgentID: c.AgentID, AgentName: c.AgentName, Title: c.Title, CreatedBy: c.CreatedBy, UpdatedAt: c.UpdatedAt, Mode: c.Mode, EditMode: c.EditMode, Effort: c.Effort, Cleaned: c.Cleaned, Purpose: c.Purpose, AutomationID: c.AutomationID,
		ContextTokens: c.ContextTokens, ContextWindow: c.ContextWindow, Source: actor.Source(c.CreatedBy), Tags: c.Tags}
	if d.Tags == nil {
		d.Tags = []string{}
	}
	if t, ok := s.cfg.Chat.Active(c.ID); ok {
		d.ActiveTurn = t.ID
	}
	if c.Purpose == "channel" {
		d.ExternalURL = channels.ConversationLink(context.Background(), s.cfg.Store, c.ID)
	}
	return d
}

func (s *server) chatError(w http.ResponseWriter, r *http.Request, err error) {
	var be *usage.BudgetError
	switch {
	case errors.As(err, &be):
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": be.Error(), "code": "budget"})
	case errors.Is(err, chat.ErrBusy), errors.Is(err, chat.ErrAgentBusy), errors.Is(err, storage.ErrAgentOff), errors.Is(err, chat.ErrCleaned):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, chat.ErrNoModel), errors.Is(err, chat.ErrNoAgent), errors.Is(err, chat.ErrNoFolder), errors.Is(err, chat.ErrDecided), errors.Is(err, automation.ErrUnknownSkill),
		errors.Is(err, attach.ErrNotFound), errors.Is(err, attach.ErrTooMany):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.writeDomainError(w, r, err)
	}
}

func (s *server) chatSkills(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Chat.Skills(r.Context(), r.PathValue("id"))
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": list})
}

func (s *server) chatAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.cfg.Chat.Agents(r.Context(), r.PathValue("id"))
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	out := make([]agentDTO, 0, len(agents))
	for _, a := range agents { // paused ones too (enabled=false): their past messages keep their avatar
		out = append(out, toAgentDTO(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (s *server) listConversations(w http.ResponseWriter, r *http.Request) {
	// a page at a time (ADR-085): the newest 20, then ?before=<the last one's updated_at>
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var before time.Time
	if b := r.URL.Query().Get("before"); b != "" {
		before, _ = time.Parse(time.RFC3339Nano, b)
	}
	tags, err := cleanTags(r.URL.Query()["tag"]) // ?tag=a&tag=b: chats with both
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.cfg.Store.Chat().ListConversationsTagged(r.Context(), r.PathValue("id"), r.URL.Query().Get("source"), tags, before, limit+1)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	more := len(list) > limit
	if more {
		list = list[:limit]
	}
	out := make([]conversationDTO, 0, len(list))
	private := r.PathValue("id") == assistant.ID(r.Context(), s.cfg.Store) // assistant chats are each person's own
	me := "human:" + userFrom(r).Email
	for _, c := range list {
		if private && c.CreatedBy != me {
			continue
		}
		out = append(out, s.toConvDTO(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out, "has_more": more})
}

func (s *server) createConversation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AgentID string `json:"agent_id"`
		Purpose string `json:"purpose"` // "automation": a chat that builds one automation (ADR-042)
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	var (
		c   storage.Conversation
		err error
	)
	if in.Purpose == "automation" || in.Purpose == "skill" || in.Purpose == "template" { // template: written with the office assistant // a chat that builds one automation, or writes one skill
		c, err = s.cfg.Chat.StartConversationPurpose(r.Context(), r.PathValue("id"), in.AgentID, in.Purpose)
	} else {
		c, err = s.cfg.Chat.StartConversation(r.Context(), r.PathValue("id"), in.AgentID)
	}
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"conversation": s.toConvDTO(c)})
}

func (s *server) getConversation(w http.ResponseWriter, r *http.Request) {
	c, err := s.cfg.Store.Chat().GetConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	msgs, err := s.cfg.Chat.History(r.Context(), c.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	// ?limit=N: the last N messages (before ?before=<id>): a phone over a VPN
	// gets a page, the older ones when it scrolls up
	more := false
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 {
		end := len(msgs)
		if before := r.URL.Query().Get("before"); before != "" {
			for i, m := range msgs {
				if m.ID == before {
					end = i
					break
				}
			}
		}
		start := max(end-n, 0)
		msgs, more = msgs[start:end], start > 0
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": s.toConvDTO(c), "messages": msgs, "has_more": more, "members": s.chatMembers(r, c.ID),
		"running": s.cfg.Chat.Running(c.ID)})
}

// chatMembers: the agents in a chat (ADR-044), with their rights and context.
func (s *server) chatMembers(r *http.Request, conversationID string) []map[string]any {
	list, _ := s.cfg.Store.Chat().Members(r.Context(), conversationID)
	out := make([]map[string]any, 0, len(list))
	for _, m := range list {
		level := ""
		if a, err := s.cfg.Store.Agents().Get(r.Context(), m.AgentID); err == nil {
			level = perm.Agent(a)
		}
		out = append(out, map[string]any{"agent_id": m.AgentID, "agent_name": m.AgentName, "level": level,
			"context_tokens": m.ContextTokens, "context_window": m.ContextWindow})
	}
	return out
}

// deleteConversation: a member deletes only the chats they started; admins any.
func (s *server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r); u.Role != storage.RoleAdmin {
		c, err := s.cfg.Store.Chat().GetConversation(r.Context(), r.PathValue("id"))
		if err != nil {
			s.writeDomainError(w, r, err)
			return
		}
		if c.CreatedBy != "human:"+u.Email {
			writeError(w, http.StatusForbidden, "chỉ xóa được cuộc trò chuyện do bạn tạo")
			return
		}
	}
	if err := s.cfg.Chat.DeleteConversation(r.Context(), r.PathValue("id")); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text        string   `json:"text"`
		Attachments []string `json:"attachments"`
		Mode        string   `json:"mode"`      // permission mode for this chat from now on
		EditMode    string   `json:"edit_mode"` // where it changes code from now on
		Context     string   `json:"context"`   // the page the person is on (ADR-042)
		AgentID     string   `json:"agent_id"`  // who answers from now on
		Effort      *string  `json:"effort"`    // how hard it thinks from now on ("" = the agent's own; nil = keep)
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Effort != nil {
		if err := s.cfg.Chat.SetEffort(r.Context(), r.PathValue("id"), *in.Effort); err != nil {
			s.chatError(w, r, err)
			return
		}
	}
	if in.AgentID != "" {
		if err := s.cfg.Chat.SetAgent(r.Context(), r.PathValue("id"), in.AgentID); err != nil {
			s.chatError(w, r, err)
			return
		}
	}
	if in.Mode != "" {
		if err := s.cfg.Chat.SetMode(r.Context(), r.PathValue("id"), s.allowedMode(r, in.Mode)); err != nil {
			s.chatError(w, r, err)
			return
		}
	}
	if in.EditMode != "" {
		if err := s.cfg.Chat.SetEditMode(r.Context(), r.PathValue("id"), s.allowedEditMode(r, in.EditMode)); err != nil {
			s.chatError(w, r, err)
			return
		}
	}
	turn, msg, err := s.cfg.Chat.SendWithContext(r.Context(), r.PathValue("id"), in.Text, in.Context, in.Attachments)
	var off *chat.OffError
	if errors.As(err, &off) && msg.ID != "" { // only paused agents were called: the message and the notice, no turn
		_ = s.cfg.Store.Chat().MarkSeen(r.Context(), userFrom(r).ID, r.PathValue("id"), true)
		writeJSON(w, http.StatusAccepted, map[string]any{"turn_id": "", "message": plainMessageDTO(msg), "notice": plainMessageDTO(off.Message)})
		return
	}
	if err != nil {
		s.chatError(w, r, err)
		return
	}
	_ = s.cfg.Store.Chat().MarkSeen(r.Context(), userFrom(r).ID, r.PathValue("id"), true) // writing in it: seen
	writeJSON(w, http.StatusAccepted, map[string]any{"turn_id": turn.ID, "message": plainMessageDTO(msg)})
}

// plainMessageDTO is a message just stored (no tools or diffs yet).
func plainMessageDTO(m storage.Message) chat.MessageDTO {
	return chat.MessageDTO{ID: m.ID, Role: m.Role, Content: m.Content, Attachments: m.Attachments, Author: m.Author, CreatedAt: m.CreatedAt,
		Tools: []storage.ToolCall{}, Patches: []chat.PatchDTO{}}
}

// streamTurn sends a turn's events as Server-Sent Events, replaying from
// ?from (or Last-Event-ID) so a reconnect loses nothing.
func (s *server) streamTurn(w http.ResponseWriter, r *http.Request) {
	turn, ok := s.cfg.Chat.Turn(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Lượt trả lời đã kết thúc hoặc không tồn tại")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	seq, _ := strconv.Atoi(r.URL.Query().Get("from"))
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		if n, err := strconv.Atoi(last); err == nil {
			seq = n + 1
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		events, done, wake := turn.Since(seq)
		for _, e := range events {
			raw, _ := json.Marshal(e)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, raw)
			seq = e.Seq + 1
		}
		flusher.Flush()
		if done {
			return
		}
		select {
		case <-wake:
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *server) cancelTurn(w http.ResponseWriter, r *http.Request) {
	turn, ok := s.cfg.Chat.Turn(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Không tìm thấy lượt trả lời")
		return
	}
	turn.Cancel()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) decidePatch(w http.ResponseWriter, r *http.Request, approve bool) {
	var in struct {
		Skip bool `json:"skip"` // rejected, and its agent is not run again about it
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	skip := !approve && in.Skip
	projectID := ""
	if cur, err := s.cfg.Store.Chat().GetPatch(r.Context(), r.PathValue("id")); err == nil {
		r, projectID = s.patchWho(r, cur, approve)
	}
	p, err := s.cfg.Chat.DecidePatch(r.Context(), r.PathValue("id"), approve)
	if err != nil && !errors.Is(err, chat.ErrDecided) {
		s.chatError(w, r, err)
		return
	}
	if errors.Is(err, chat.ErrDecided) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "patch": p})
		return
	}
	action := "patch.reject"
	if approve {
		action = "patch." + p.Status
	} else if skip {
		action = "patch.skip"
	}
	var failed error
	if p.Status == "failed" {
		failed = errors.New(p.Detail)
	}
	s.audit(r, audit.Change{Action: action, ResourceID: p.ID, ProjectID: projectID, Detail: map[string]any{"files": p.Files, "detail": p.Detail}, Err: failed})
	cur, err := s.cfg.Store.Chat().GetPatch(r.Context(), p.ID)
	if err == nil && !skip { // its agent goes on (ADR-084); skipped, it does not
		line := "❌ Đã từ chối diff: " + strings.Join(p.Files, ", ")
		if approve {
			line = "✅ Diff " + strings.Join(p.Files, ", ") + ": " + firstNonEmptyStr(p.Detail, p.Status)
		}
		s.cfg.Chat.Decided(cur.ConversationID, userFrom(r).Email, line)
	}
	if err == nil {
		s.redrawBot(r.Context(), cur.ConversationID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"patch": p})
}

// getPatch is a diff with all of its text (a chat loads big ones cut short).
func (s *server) getPatch(w http.ResponseWriter, r *http.Request) {
	p, err := s.cfg.Store.Chat().GetPatch(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if p.ConversationID != "" && !s.mayOpen(r, p.ConversationID) {
		writeError(w, http.StatusNotFound, "không tìm thấy diff")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"patch": chat.FullPatchDTO(p)})
}

func (s *server) approvePatch(w http.ResponseWriter, r *http.Request) { s.decidePatch(w, r, true) }
func (s *server) rejectPatch(w http.ResponseWriter, r *http.Request)  { s.decidePatch(w, r, false) }

// patchWho: approving a patch records the agent that wrote it and the person
// who approved it; rejecting is the person's decision (ADR-043).
func (s *server) patchWho(r *http.Request, p storage.Patch, approve bool) (*http.Request, string) {
	u := userFrom(r)
	who := audit.Who{Kind: "human", ID: u.ID, Name: u.Email, Via: "ui", ConversationID: p.ConversationID, TaskID: p.TaskID}
	projectID := ""
	agent := ""
	if p.ConversationID != "" {
		if c, err := s.cfg.Store.Chat().GetConversation(r.Context(), p.ConversationID); err == nil {
			projectID, agent = c.ProjectID, c.AgentName
		}
		// in a group chat the diff is the agent's who wrote the answer (ADR-044)
		if msgs, err := s.cfg.Store.Chat().ListMessages(r.Context(), p.ConversationID); err == nil {
			for _, m := range msgs {
				if m.ID == p.MessageID && m.Author != "" {
					agent = m.Author
				}
			}
		}
	}
	if projectID == "" && p.TaskID != "" {
		if t, err := s.cfg.Store.Tasks().Get(r.Context(), p.TaskID); err == nil {
			projectID = t.ProjectID
		}
	}
	if approve && agent != "" {
		via := "chat"
		if p.TaskID != "" {
			via = "task"
		}
		who = audit.Who{Kind: "agent", Name: agent, ApprovedBy: u.Email, Via: via, ConversationID: p.ConversationID, TaskID: p.TaskID}
	}
	return r.WithContext(audit.With(r.Context(), who)), projectID
}

// stopConversation: Stop in a chat stops its answer and every hand-off
// working in the background.
func (s *server) stopConversation(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"stopped": s.cfg.Chat.StopAll(r.PathValue("id"))})
}

// assistantInfo: the office assistant's project, where its chats live (ADR-046).
func (s *server) assistantInfo(w http.ResponseWriter, r *http.Request) {
	id := assistant.ID(r.Context(), s.cfg.Store)
	if id == "" {
		writeError(w, http.StatusNotFound, "office chưa có trợ lý")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project_id": id, "mode": assistant.Mode(r.Context(), s.cfg.Store)})
}

// setAssistantMode sets the assistant's rights: answer, manage or admin (ADR-059).
func (s *server) setAssistantMode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode string `json:"mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	old := assistant.Mode(r.Context(), s.cfg.Store)
	if err := assistant.SetMode(r.Context(), s.cfg.Store, in.Mode); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "assistant.mode", Resource: "assistant", Before: map[string]any{"mode": old}, After: map[string]any{"mode": in.Mode}})
	writeJSON(w, http.StatusOK, map[string]any{"mode": in.Mode})
}

// mayOpen: an office assistant chat is its creator's alone (ADR-046);
// project chats are the project's.
func (s *server) mayOpen(r *http.Request, conversationID string) bool {
	c, err := s.cfg.Store.Chat().GetConversation(r.Context(), conversationID)
	if err != nil {
		return true // the handler reports it
	}
	return c.ProjectID != assistant.ID(r.Context(), s.cfg.Store) || c.CreatedBy == "human:"+userFrom(r).Email
}

func (s *server) ownChat(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.mayOpen(r, r.PathValue("id")) {
			writeError(w, http.StatusNotFound, "không tìm thấy cuộc trò chuyện")
			return
		}
		h(w, r)
	}
}

func (s *server) ownTurn(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if t, ok := s.cfg.Chat.Turn(r.PathValue("id")); ok && !s.mayOpen(r, t.ConversationID) {
			writeError(w, http.StatusNotFound, "không tìm thấy lượt trả lời")
			return
		}
		h(w, r)
	}
}

// recentConversations: the latest chats across the office's projects (the
// overview), bots' included; the office assistant's are each person's own.
func (s *server) recentConversations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 6
	}
	projects, err := s.cfg.Store.Repos().List(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	hidden := assistant.ID(ctx, s.cfg.Store)
	names := map[string]string{}
	var all []storage.Conversation
	for _, p := range projects {
		if p.ID == hidden {
			continue
		}
		names[p.ID] = p.Name
		list, err := s.cfg.Store.Chat().ListConversationsFrom(ctx, p.ID, "all", limit)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		all = append(all, list...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].UpdatedAt.After(all[j].UpdatedAt) })
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]conversationDTO, 0, len(all))
	for _, c := range all {
		out = append(out, s.toConvDTO(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out, "projects": names})
}

// markSeen: the person looked at the chat ({"seen": true}, also the default)
// or wants it back as unread ({"seen": false}).
func (s *server) markSeen(w http.ResponseWriter, r *http.Request) {
	in := struct {
		Seen *bool `json:"seen"`
	}{}
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	seen := in.Seen == nil || *in.Seen
	if err := s.cfg.Store.Chat().MarkSeen(r.Context(), userFrom(r).ID, r.PathValue("id"), seen); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
