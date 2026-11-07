package sqlite_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// TestPendingVisibleKeepsOwnProjectBeyondGlobalWindow verifies PendingVisible
// filters by project before the limit is applied, so a caller's own older
// pending action is not pushed out of the window by other projects' newer
// ones (ADR-224) — the bug pending.go:12 had calling Pending(ctx, 100) then
// filtering by project in Go after the DB already capped to 100.
func TestPendingVisibleKeepsOwnProjectBeyondGlobalWindow(t *testing.T) {
	ctx := context.Background()
	st, mine := openStore(t)
	other, err := st.Repos().Create(ctx, storage.Repo{Name: "other", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	// Caller's own pending action, created first (so it is the oldest).
	want, err := st.Actions().Create(ctx, storage.Action{ProjectID: mine.ID, Kind: "k", Target: "t", TargetID: "1", Reason: "r", ProposedBy: "u"})
	if err != nil {
		t.Fatal(err)
	}

	// 100 newer pending actions in a project the caller cannot see.
	for i := 0; i < 100; i++ {
		if _, err := st.Actions().Create(ctx, storage.Action{ProjectID: other.ID, Kind: "k", Target: "t", TargetID: "1", Reason: "r", ProposedBy: "u"}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.Actions().PendingVisible(ctx, []string{mine.ID}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != want.ID {
		t.Fatalf("PendingVisible(mine) = %+v, want only %s", got, want.ID)
	}
}
