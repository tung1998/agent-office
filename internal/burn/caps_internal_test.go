package burn

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/worktree"
)

// stopAfterReached keys its "already reached" flag by RunBranch alone, but
// RunBranch only names the second a run started, with no project id: two
// projects' runs that happen to start the same second must not share caps
// state.
func TestStopAfterReachedIsolatedAcrossProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()
	st, err := sqlite.Open(filepath.Join(tmp, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	pA, err := st.Repos().Create(ctx, storage.Repo{Name: "a", Path: filepath.Join(tmp, "a")})
	if err != nil {
		t.Fatal(err)
	}
	pB, err := st.Repos().Create(ctx, storage.Repo{Name: "b", Path: filepath.Join(tmp, "b")})
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, worktree.New(filepath.Join(tmp, "trees")))

	const sameRunBranch = "burn/2026-01-01-000000"
	bA := storage.BurnSession{ProjectID: pA.ID, RunBranch: sameRunBranch, StopAfter: 1}
	bB := storage.BurnSession{ProjectID: pB.ID, RunBranch: sameRunBranch, StopAfter: 1}

	itemsA := []storage.BurnItem{{Status: "done", RunBranch: sameRunBranch}}
	itemsB := []storage.BurnItem{{Status: "done", RunBranch: sameRunBranch}}

	if !s.stopAfterReached(bA, itemsA) {
		t.Fatal("project A expected to reach its StopAfter cap the first time")
	}
	// Project B's run, same RunBranch string, must raise its own cap
	// independently of A's — not be told "already reached" by A's state.
	if !s.stopAfterReached(bB, itemsB) {
		t.Fatal("project B's StopAfter cap was wrongly suppressed by project A's state (map keyed by RunBranch alone)")
	}
}
