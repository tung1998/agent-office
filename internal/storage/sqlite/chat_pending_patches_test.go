package sqlite_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// TestPendingPatchesVisibleKeepsOwnProjectBeyondGlobalWindow verifies
// PendingPatchesVisible filters by project before the limit is applied, so a
// caller's own older pending patch is not pushed out of the window by other
// projects' newer ones (same bug family as ADR-224's PendingVisible).
func TestPendingPatchesVisibleKeepsOwnProjectBeyondGlobalWindow(t *testing.T) {
	ctx := context.Background()
	st, mine := openStore(t)
	other, err := st.Repos().Create(ctx, storage.Repo{Name: "other", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	mineConv, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: mine.ID, Title: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: mineConv.ID, Diff: "d"})
	if err != nil {
		t.Fatal(err)
	}

	otherConv, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: other.ID, Title: "other"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: otherConv.ID, Diff: "d"}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.Chat().PendingPatchesVisible(ctx, []string{mine.ID}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != want.ID {
		t.Fatalf("PendingPatchesVisible(mine) = %+v, want only %s", got, want.ID)
	}
}
