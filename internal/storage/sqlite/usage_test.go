package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Runs page back from the last one shown, newest first, none twice.
func TestRunsPageBefore(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	for i := 0; i < 5; i++ {
		st.Runs().Create(ctx, storage.Run{Kind: "chat", ProjectID: p.ID, Status: "ok"})
	}
	since := time.Now().Add(-time.Hour)
	first, err := st.Runs().List(ctx, storage.RunFilter{Since: since, Limit: 2})
	if err != nil || len(first) != 2 {
		t.Fatalf("first = %d %v", len(first), err)
	}
	seen := map[string]bool{first[0].ID: true, first[1].ID: true}
	rest, err := st.Runs().List(ctx, storage.RunFilter{Since: since, Limit: 10, Before: first[1].ID})
	if err != nil || len(rest) != 3 {
		t.Fatalf("rest = %d %v", len(rest), err)
	}
	for _, r := range rest {
		if seen[r.ID] {
			t.Fatalf("run %s listed twice", r.ID)
		}
	}
}
