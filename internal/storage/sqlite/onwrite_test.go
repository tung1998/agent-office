package sqlite_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// Every write says which table it changed (the dashboard's live updates);
// reads say nothing; a transaction's writes too.
func TestOnWrite(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	var mu sync.Mutex
	var got []string
	st.(*sqlite.Store).OnWrite(func(table string) { mu.Lock(); got = append(got, table); mu.Unlock() })
	c, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "x"})
	st.Chat().ListConversations(ctx, p.ID, 10)
	st.InTx(ctx, func(tx storage.Store) error {
		_, err := tx.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", ConversationID: c.ID, Status: "pending"})
		return err
	})
	st.Jobs().Claim(ctx, time.Now().UTC(), 5, nil)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(got, "conversations") || !slices.Contains(got, "jobs") {
		t.Fatalf("tables = %v", got)
	}
	for _, x := range got {
		if x == "" {
			t.Fatalf("an unnamed write: %v", got)
		}
	}
}

// With live updates on, a replace is still one transaction: a failure leaves
// the notes as they were, and says nothing changed.
func TestOnWriteKeepsTransactions(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	mem := st.Memories()
	mem.Create(ctx, storage.Memory{ProjectID: p.ID, AgentID: "agt_1", Text: "cũ", Source: "person"})
	var got []string
	st.(*sqlite.Store).OnWrite(func(table string) { got = append(got, table) })
	dup := storage.Memory{ID: "mem_same", Text: "a"}
	if err := st.Memories().Replace(ctx, p.ID, "agt_1", []storage.Memory{dup, dup}); err == nil {
		t.Fatal("a duplicate id was stored")
	}
	if list, _ := st.Memories().List(ctx, p.ID, "agt_1"); len(list) != 1 || list[0].Text != "cũ" {
		t.Fatalf("a failed replace changed the notes: %+v", list)
	}
	if len(got) != 0 {
		t.Fatalf("a rolled back write was told: %v", got)
	}
	if err := st.Memories().Replace(ctx, p.ID, "agt_1", []storage.Memory{{Text: "mới"}}); err != nil || !slices.Contains(got, "agent_memories") {
		t.Fatalf("replace = %v, told %v", err, got)
	}
}

// A monitor checked again with the same result writes its check time
// quietly: the dashboard is told only when its status or message changes.
func TestMonitorCheckQuiet(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	m, err := st.Monitors().Create(ctx, storage.Monitor{ProjectID: p.ID, Name: "api", Type: "http", Target: "http://x", IntervalS: 30, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	st.(*sqlite.Store).OnWrite(func(table string) { got = append(got, table) })
	now := time.Now().UTC()
	m.Status, m.LastMessage, m.LastCheckedAt = "up", "200 OK", &now
	st.Monitors().SaveStatus(ctx, m)
	if len(got) != 1 {
		t.Fatalf("a change: told %v", got)
	}
	later := now.Add(30 * time.Second)
	m.LastCheckedAt, m.LastLatencyMS = &later, 42
	if err := st.Monitors().SaveStatus(ctx, m); err != nil || len(got) != 1 {
		t.Fatalf("the same result: told %v (%v)", got, err)
	}
	if x, _ := st.Monitors().Get(ctx, m.ID); x.LastCheckedAt == nil || !x.LastCheckedAt.Equal(later.Truncate(time.Microsecond)) && x.LastLatencyMS != 42 {
		t.Fatalf("check time not kept: %+v", x)
	}
	m.Status = "down"
	st.Monitors().SaveStatus(ctx, m)
	if len(got) != 2 {
		t.Fatalf("down: told %v", got)
	}
}

// A chat written says what: the message itself, the conversation made,
// changed or deleted.
func TestOnChat(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	var got []storage.Change
	st.(*sqlite.Store).OnChat(func(c storage.Change) { got = append(got, c) })
	c, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "x"})
	st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Content: "hi"})
	c.Title = "y"
	st.Chat().UpdateConversation(ctx, c)
	st.Chat().DeleteConversation(ctx, c.ID)
	kinds := []string{}
	for _, x := range got {
		kinds = append(kinds, x.Kind)
	}
	if strings.Join(kinds, ",") != "conversation,message,conversation,conversation.deleted" || got[1].Message == nil || got[1].Message.ID == "" || got[1].Message.Content != "hi" {
		t.Fatalf("changes = %+v", got)
	}
}
