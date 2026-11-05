package chat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-189: when the answer whose diff replaced the old one asked its own
// commit, the commit held for the old diff is dropped (one commit, not a
// second failing "nothing to commit"); a commit of another answer stays.
func TestCarryDeferredNewerCommitWins(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	conv, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "c"})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(msg, status string) storage.Action {
		a, err := st.Actions().Create(ctx, storage.Action{ProjectID: p.ID, ConversationID: conv.ID, MessageID: msg, Kind: "git_commit", Target: msg, Status: status,
			Args: storage.ActionArgs{Message: msg, Files: []string{"a.go"}, AfterDiff: true}})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	old, other, newer := mk("m1", "queued"), mk("m0", "pending"), mk("m2", "pending")
	e := &Engine{store: st}
	e.carryDeferred(ctx, conv.ID, []string{"m1"}, "m2", []string{"a.go", "b.go"})
	if a, _ := st.Actions().Get(ctx, old.ID); a.Status != "rejected" || a.DecidedBy != "office" {
		t.Fatalf("old commit = %+v", a)
	}
	if a, _ := st.Actions().Get(ctx, other.ID); a.Status != "pending" || a.MessageID != "m0" {
		t.Fatalf("another answer's commit changed: %+v", a)
	}
	if a, _ := st.Actions().Get(ctx, newer.ID); a.Status != "pending" || len(a.Args.Files) != 1 {
		t.Fatalf("the newer commit changed: %+v", a)
	}
}
