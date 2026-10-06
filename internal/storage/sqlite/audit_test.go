package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// A row written before migration 00024 (old columns only) still reads back.
func TestAuditLegacyRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "office.db")
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO audit_log (id, actor, action, target, detail, at) VALUES ('aud_old','human:a@x.io','project.update','prj_9','{}','2026-01-01T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Audit().List(ctx, storage.AuditFilter{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("list = %d %v", len(rows), err)
	}
	if r := rows[0]; r.ID != "aud_old" || !r.OK || r.Before != nil || r.After != nil || r.Actor != "human:a@x.io" {
		t.Fatalf("legacy row: %+v", r)
	}
}

// Migration 00027 gives agents with hand-picked capabilities the user's MCP.
func TestUserMCPMigrationShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "office.db")
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, _ := sql.Open("sqlite", path)
	defer db.Close()
	var got string
	err = db.QueryRow(`SELECT json_insert('{"caps":["propose"]}', '$.caps[#]', 'tools.mcp')`).Scan(&got)
	if err != nil || got != `{"caps":["propose","tools.mcp"]}` {
		t.Fatalf("json_insert = %q %v", got, err)
	}
}

// Members of a chat: each agent's own session, what it has seen, when it joined.
func TestChatMembers(t *testing.T) {
	s, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	s.Migrate(ctx)
	p, _ := s.Repos().Create(ctx, storage.Repo{Name: "p"})
	c, err := s.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: c.ID, AgentID: "a1", AgentName: "Lead", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	s.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: c.ID, AgentID: "a2", AgentName: "Dev"})
	first, _ := s.Chat().Members(ctx, c.ID)
	s.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: c.ID, AgentID: "a1", AgentName: "Lead", SessionID: "s2", LastMessageID: "msg_9", ContextTokens: 5, ContextWindow: 100})
	got, err := s.Chat().Members(ctx, c.ID)
	if err != nil || len(got) != 2 || got[0].AgentID != "a1" || got[1].AgentID != "a2" {
		t.Fatalf("members = %+v %v", got, err)
	}
	if got[0].SessionID != "s2" || got[0].LastMessageID != "msg_9" || got[0].ContextWindow != 100 || !got[0].JoinedAt.Equal(first[0].JoinedAt) {
		t.Fatalf("upsert = %+v (joined %v)", got[0], first[0].JoinedAt)
	}
}

func TestChannels(t *testing.T) {
	s, _ := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	defer s.Close()
	ctx := context.Background()
	s.Migrate(ctx)
	p, _ := s.Repos().Create(ctx, storage.Repo{Name: "p"})
	c, err := s.Channels().Create(ctx, storage.Channel{ProjectID: p.ID, Kind: "telegram", Name: "Hỗ trợ", TokenEnc: "enc", Allow: []string{"42"}, FilterEnabled: true, Scope: "đơn hàng"})
	if err != nil {
		t.Fatal(err)
	}
	c.Name, c.Enabled = "Hỗ trợ 2", false
	if err := s.Channels().Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	s.Channels().SetStatus(ctx, c.ID, "shop_bot", "", nil)
	got, _ := s.Channels().Get(ctx, c.ID)
	if got.Name != "Hỗ trợ 2" || got.Enabled || got.Allow[0] != "42" || !got.FilterEnabled || got.BotName != "shop_bot" || got.TokenEnc != "enc" {
		t.Fatalf("channel = %+v", got)
	}
	conv, _ := s.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID})
	if id, _ := s.Channels().Thread(ctx, c.ID, "chat-1"); id != "" {
		t.Fatalf("thread before = %q", id)
	}
	s.Channels().SetThread(ctx, c.ID, "chat-1", conv.ID)
	if id, _ := s.Channels().Thread(ctx, c.ID, "chat-1"); id != conv.ID {
		t.Fatalf("thread = %q", id)
	}
	if all, _ := s.Channels().List(ctx, ""); len(all) != 1 {
		t.Fatalf("list all = %d", len(all))
	}
}
