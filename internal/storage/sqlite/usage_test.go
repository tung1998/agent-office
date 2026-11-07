package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Trend groups cost and average duration by day and agent, resolves the
// agent's display name, and keeps a run with no agent out of the named rows.
func TestRunRepoTrend(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	a1, err := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a1", Name: "Scout", ModelTier: "balanced"})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a2", Name: "Clerk", ModelTier: "balanced"})
	if err != nil {
		t.Fatal(err)
	}
	day1 := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 11, 9, 0, 0, 0, time.UTC)
	cost := func(v float64) *float64 { return &v }
	mustCreate := func(x storage.Run) {
		if _, err := st.Runs().Create(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	mustCreate(storage.Run{ProjectID: p.ID, AgentID: a1.ID, Status: "ok", CostUSD: cost(1), DurationMS: 100, CreatedAt: day1})
	mustCreate(storage.Run{ProjectID: p.ID, AgentID: a1.ID, Status: "ok", CostUSD: cost(3), DurationMS: 300, CreatedAt: day1})
	mustCreate(storage.Run{ProjectID: p.ID, AgentID: a2.ID, Status: "ok", CostUSD: cost(5), DurationMS: 500, CreatedAt: day2})
	mustCreate(storage.Run{ProjectID: p.ID, Status: "ok", CostUSD: cost(2), DurationMS: 200, CreatedAt: day2}) // no agent

	rows, err := st.Runs().Trend(ctx, day1.Add(-time.Hour), "agent", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	byDayKey := map[[2]string]storage.UsageTrendRow{}
	for _, r := range rows {
		byDayKey[[2]string{r.Day, r.Key}] = r
	}
	a1Row, ok := byDayKey[[2]string{"2026-01-10", a1.ID}]
	if !ok {
		t.Fatalf("missing day1/a1 row, got %+v", rows)
	}
	if a1Row.Label != "Scout" {
		t.Errorf("a1 label = %q, want Scout", a1Row.Label)
	}
	if a1Row.Runs != 2 || a1Row.CostUSD != 4 || a1Row.AvgDurationMS != 200 {
		t.Errorf("a1 row = %+v, want runs=2 cost=4 avg=200", a1Row)
	}
	a2Row, ok := byDayKey[[2]string{"2026-01-11", a2.ID}]
	if !ok || a2Row.Label != "Clerk" || a2Row.CostUSD != 5 {
		t.Fatalf("a2 row = %+v ok=%v", a2Row, ok)
	}
	noAgentRow, ok := byDayKey[[2]string{"2026-01-11", ""}]
	if !ok || noAgentRow.CostUSD != 2 {
		t.Fatalf("no-agent row = %+v ok=%v", noAgentRow, ok)
	}
}

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
