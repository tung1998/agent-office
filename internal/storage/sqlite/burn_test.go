package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestBurnStore(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	b := st.Burn()
	if _, err := b.Session(ctx, p.ID); err != storage.ErrNotFound {
		t.Fatalf("no session yet: %v", err)
	}
	ends := time.Now().UTC().Add(time.Hour)
	s, err := b.SaveSession(ctx, storage.BurnSession{ProjectID: p.ID, ConversationID: "cnv_1", ModelTier: "fast", MaxSubagents: 1, ResultMode: "branch", EndsAt: &ends, State: "running"})
	if err != nil {
		t.Fatal(err)
	}
	s.Focus = "checkout"
	b.SaveSession(ctx, s)
	got, _ := b.SessionByConversation(ctx, "cnv_1")
	if got.ID != s.ID || got.Focus != "checkout" || got.EndsAt == nil {
		t.Fatalf("session = %+v", got)
	}
	if run, _ := b.Running(ctx); len(run) != 1 {
		t.Fatalf("running = %d", len(run))
	}
	it, _ := b.AddItem(ctx, storage.BurnItem{SessionID: s.ID, Title: "test fail ở checkout", Kind: "bug"})
	it.Status, it.Branch = "done", "burn/x"
	b.UpdateItem(ctx, it)
	items, _ := b.Items(ctx, s.ID)
	if len(items) != 1 || items[0].Status != "done" || items[0].Branch != "burn/x" {
		t.Fatalf("items = %+v", items)
	}
}
