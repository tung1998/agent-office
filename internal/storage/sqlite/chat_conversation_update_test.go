package sqlite_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// A single UpdateConversation shared by SetMode/SetAgent/SetEffort/SetEditMode/
// titleFrom blind-overwrites every column from whichever stale struct the
// caller read first, so one setting change can silently revert another that
// landed on the same (still untitled) conversation in the meantime. Narrow,
// single-column updates (UpdateMode, UpdateEditMode, UpdateEffort,
// UpdateAgent, UpdateTitleIfEmpty) fix that by touching only the field each
// caller actually changed.
func TestConversationNarrowUpdatesDontClobberEachOther(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	chat := st.Chat()

	conv, err := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "", Mode: "propose", EditMode: "worktree"})
	if err != nil {
		t.Fatal(err)
	}

	// SetEffort and SetEditMode each read the same freshly-created row, then
	// write back only their own field.
	if err := chat.UpdateEffort(ctx, conv.ID, "high"); err != nil {
		t.Fatalf("UpdateEffort: %v", err)
	}
	if err := chat.UpdateEditMode(ctx, conv.ID, "direct"); err != nil {
		t.Fatalf("UpdateEditMode: %v", err)
	}
	// titleFrom races in too, from a snapshot taken before either of the above.
	if err := chat.UpdateTitleIfEmpty(ctx, conv.ID, "first message"); err != nil {
		t.Fatalf("UpdateTitleIfEmpty: %v", err)
	}

	got, err := chat.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Effort != "high" {
		t.Errorf("Effort = %q, want %q (clobbered)", got.Effort, "high")
	}
	if got.EditMode != "direct" {
		t.Errorf("EditMode = %q, want %q (clobbered)", got.EditMode, "direct")
	}
	if got.Title != "first message" {
		t.Errorf("Title = %q, want %q (clobbered)", got.Title, "first message")
	}
}
