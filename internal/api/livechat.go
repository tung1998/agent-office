package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"
	"sync"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/events"
	"github.com/tung1998/agent-office/internal/storage"
)

// Chats and "Cần xử lý", pushed with their data (ADR-078): a message added is
// sent as it is, a conversation made or changed as its row, so an open page
// puts it in place instead of loading the list again; what needs a person is
// worked out again for each one looking when what it comes from changes.

// incidentTables are what "Cần xử lý" is worked out from.
var incidentTables = []string{"jobs", "actions", "patches", "channels", "automations", "monitors", "processes", "providers", "runs", "messages", "conversation_reads", "settings", "repos"}

type livePush struct {
	mu        sync.Mutex
	incidents map[string][32]byte // the last list each person was sent
}

func (s *server) startLiveData() {
	b := s.cfg.Events
	if b == nil {
		return
	}
	s.push.incidents = map[string][32]byte{}
	b.OnChat(s.chatChanged)
	b.OnFlush(func(tables []string) {
		if slices.ContainsFunc(tables, func(t string) bool { return slices.Contains(incidentTables, t) }) {
			s.pushIncidents(context.Background(), false)
		}
	})
}

// canSee: whose page may show the chat (an office assistant chat is its
// maker's own, as GET /api/conversations/{id} has it).
func (s *server) canSee(c storage.Conversation) func(events.Viewer, map[string]bool) bool {
	private := c.ProjectID == assistant.ID(context.Background(), s.cfg.Store)
	return func(v events.Viewer, _ map[string]bool) bool { return !private || c.CreatedBy == "human:"+v.Email }
}

func (s *server) chatChanged(c storage.Change) {
	ctx := context.Background()
	b := s.cfg.Events
	if c.Kind == "conversation.deleted" {
		b.Send(events.Event{Name: "conversation.deleted", Data: map[string]any{"id": c.ConversationID}}, nil)
		return
	}
	conv, err := s.cfg.Store.Chat().GetConversation(ctx, c.ConversationID)
	if err != nil {
		return
	}
	see := s.canSee(conv)
	if c.Kind == "message" && c.Message != nil {
		m := c.Message
		d := chat.MessageDTO{ID: m.ID, Role: m.Role, Content: m.Content, Tools: m.Tools, Attachments: m.Attachments, Author: m.Author, CreatedAt: m.CreatedAt,
			Patches: []chat.PatchDTO{}, Actions: []chat.ActionDTO{}}
		if d.Tools == nil {
			d.Tools = []storage.ToolCall{}
		}
		if d.Attachments == nil {
			d.Attachments = []storage.Attachment{}
		}
		b.Send(events.Event{Name: "message", Data: map[string]any{"conversation_id": conv.ID, "project_id": conv.ProjectID, "message": d}}, see)
	}
	b.Send(events.Event{Name: "conversation", Data: map[string]any{"conversation": s.toConvDTO(conv)}}, see)
}

// pushConversation sends a chat's row again (an answer started or ended in it).
func (s *server) pushConversation(id string) {
	if s.cfg.Events != nil && id != "" {
		s.chatChanged(storage.Change{Kind: "conversation", ConversationID: id})
	}
}

// pushIncidents works out "Cần xử lý" for each person looking and sends it
// when it changed since (always, to a page that just opened: force).
func (s *server) pushIncidents(ctx context.Context, force bool) {
	for _, v := range s.cfg.Events.Viewers() {
		s.pushIncidentsTo(ctx, v, force)
	}
}

func (s *server) pushIncidentsTo(ctx context.Context, v events.Viewer, force bool) {
	u := storage.User{ID: v.UserID, Email: v.Email, Role: storage.RoleMember}
	if v.Admin {
		u.Role = storage.RoleAdmin
	}
	list, err := s.incidentsFor(ctx, u)
	if err != nil {
		return
	}
	data := map[string]any{"incidents": list, "count": len(list)}
	raw, _ := json.Marshal(data)
	sum := sha256.Sum256(raw)
	s.push.mu.Lock()
	same := s.push.incidents[v.UserID] == sum
	s.push.incidents[v.UserID] = sum
	s.push.mu.Unlock()
	if same && !force {
		return
	}
	s.cfg.Events.Send(events.Event{Name: "incidents", Data: data}, func(x events.Viewer, _ map[string]bool) bool { return x.UserID == v.UserID })
}
