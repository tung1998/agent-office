package burn

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/worktree"
)

// record puts a piece's own earlier fail reason into its Detail, so the
// worker that picks it up next sees why it failed last time (Reflexion),
// separate from the run's general Lessons.
func TestRecordCarriesOwnFailReasonIntoBrief(t *testing.T) {
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
	p, err := st.Repos().Create(ctx, storage.Repo{Name: "demo", Path: tmp})
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, worktree.New(filepath.Join(tmp, "trees")))
	b, err := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID, RunBranch: "burn/test", State: "stopped"})
	if err != nil {
		t.Fatal(err)
	}

	// First try: recorded, then fails with a specific reason.
	title := "Fix the thing in foo.go"
	first, err := s.store.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: title, Kind: "bug", RunBranch: b.RunBranch})
	if err != nil {
		t.Fatal(err)
	}
	first.Status = "failed"
	first.Summary = "panic: nil pointer in foo.go line 42"
	if err := st.Burn().UpdateItem(ctx, first); err != nil {
		t.Fatal(err)
	}

	// Second try: a scan records the same piece again, worded a bit
	// differently (same pieceKey) so it is not turned away as a duplicate
	// of the still-open failed item (n=1 < maxTries=2).
	title2 := title + " (again)"
	if _, err := s.record(ctx, b, "scan1", ToolInput{Title: title2, Kind: "bug", Detail: "where/evidence/fix"}); err != nil {
		t.Fatal(err)
	}
	items, err := st.Burn().Items(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var second storage.BurnItem
	for _, it := range items {
		if it.Title == title2 {
			second = it
		}
	}
	if second.ID == "" {
		t.Fatal("second attempt not recorded")
	}
	if !strings.Contains(second.Detail, "panic: nil pointer in foo.go line 42") {
		t.Fatalf("brief of 2nd attempt missing 1st fail reason, detail = %q", second.Detail)
	}

	brief := workPrompt(b, second, false, false, false)
	if !strings.Contains(brief, "panic: nil pointer in foo.go line 42") {
		t.Fatalf("worker brief missing own earlier fail reason: %q", brief)
	}
}
