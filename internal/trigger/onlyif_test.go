package trigger_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/collections"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// ADR-194: a frequent schedule runs only when its data has work: a record
// matching where (with $today), or one changed since its last run; a
// condition it cannot read lets it run.
func TestOnlyIf(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	cs := collections.New(st)
	if _, err := cs.Define(ctx, p.ID, storage.Collection{Key: "tasks", Fields: []storage.Field{
		{Name: "title", Type: storage.FieldText}, {Name: "status", Type: storage.FieldText}, {Name: "due", Type: storage.FieldDate}}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	auto := func(c *storage.AutomationCondition, last *time.Time) storage.Automation {
		return storage.Automation{ProjectID: p.ID, LastRunAt: last, Config: storage.AutomationConfig{EveryMinutes: 30, OnlyIf: c}}
	}
	pending := &storage.AutomationCondition{Collection: "tasks", Where: []storage.Cond{{Field: "status", Op: "in", Value: []any{"cho-kiem-tra", "bi-tra-lai"}}}}
	if !trigger.HasWork(ctx, st, auto(nil, nil), now) {
		t.Fatal("no condition: it runs")
	}
	if trigger.HasWork(ctx, st, auto(pending, nil), now) {
		t.Fatal("ran with nothing to do")
	}
	rec, _, _ := cs.Upsert(ctx, p.ID, "tasks", "", map[string]any{"title": "a", "status": "dang-lam", "due": "2026-10-20"}, "x")
	if trigger.HasWork(ctx, st, auto(pending, nil), now) {
		t.Fatal("a task in progress is no work for it")
	}
	overdue := &storage.AutomationCondition{Collection: "tasks", Where: []storage.Cond{{Field: "due", Op: "lt", Value: "$today"}}}
	if !trigger.HasWork(ctx, st, auto(overdue, nil), now) {
		t.Fatal("an overdue task did not wake it")
	}
	cs.Upsert(ctx, p.ID, "tasks", rec.ID, map[string]any{"title": "a", "status": "cho-kiem-tra", "due": "2026-10-20"}, "x")
	if !trigger.HasWork(ctx, st, auto(pending, nil), now) {
		t.Fatal("a task waiting for review did not wake it")
	}
	changed := &storage.AutomationCondition{Collection: "tasks", Changed: true}
	later := time.Now().Add(time.Hour)
	if trigger.HasWork(ctx, st, auto(changed, &later), now) {
		t.Fatal("ran with nothing changed since its last run")
	}
	earlier := time.Now().Add(-time.Hour)
	if !trigger.HasWork(ctx, st, auto(changed, &earlier), now) {
		t.Fatal("a change did not wake it")
	}
	if !trigger.HasWork(ctx, st, auto(&storage.AutomationCondition{Collection: "gone", Changed: true}, &later), now) {
		t.Fatal("a condition on a missing table must let it run (and show)")
	}
}

// ADR-196: a schedule watches the work board: open = work no one is doing
// (the Burn off with pieces left, or one in review); changed = a piece
// changed since its last run. No board yet: nothing to do.
func TestOnlyIfBoard(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	now := time.Now()
	auto := func(board string, last *time.Time) storage.Automation {
		return storage.Automation{ProjectID: p.ID, LastRunAt: last, Config: storage.AutomationConfig{EveryMinutes: 30, OnlyIf: &storage.AutomationCondition{Board: board}}}
	}
	if trigger.HasWork(ctx, st, auto(storage.BoardOpen, nil), now) || trigger.HasWork(ctx, st, auto(storage.BoardChanged, nil), now) {
		t.Fatal("no board yet: nothing to do")
	}
	b, err := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID, ModelTier: storage.TierBalanced, MaxParallel: 1, State: "running"})
	if err != nil {
		t.Fatal(err)
	}
	it, err := st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "q", Kind: "quest", Status: "found"})
	if err != nil {
		t.Fatal(err)
	}
	if trigger.HasWork(ctx, st, auto(storage.BoardOpen, nil), now) {
		t.Fatal("a running Burn takes the quest: no work for the schedule")
	}
	b.State = "stopped"
	if _, err := st.Burn().SaveSession(ctx, b); err != nil {
		t.Fatal(err)
	}
	if !trigger.HasWork(ctx, st, auto(storage.BoardOpen, nil), now) {
		t.Fatal("a quest waiting with the Burn off did not wake it")
	}
	it.Status = "done"
	if err := st.Burn().UpdateItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	if trigger.HasWork(ctx, st, auto(storage.BoardOpen, nil), now) {
		t.Fatal("nothing open left")
	}
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	if trigger.HasWork(ctx, st, auto(storage.BoardChanged, &later), now) {
		t.Fatal("ran with nothing changed since its last run")
	}
	if !trigger.HasWork(ctx, st, auto(storage.BoardChanged, &earlier), now) {
		t.Fatal("a piece finished since its last run did not wake it")
	}
	// A piece being worked on writes its cost and chats all along: no news.
	run := time.Now()
	doing := it
	doing.Status, doing.CostUSD = "doing", 0.4
	if err := st.Burn().UpdateItem(ctx, doing); err != nil {
		t.Fatal(err)
	}
	if trigger.HasWork(ctx, st, auto(storage.BoardChanged, &run), now) {
		t.Fatal("a piece still being worked on woke the schedule")
	}
	if _, err := st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "q2", Kind: "quest", Status: "queued"}); err != nil {
		t.Fatal(err)
	}
	if !trigger.HasWork(ctx, st, auto(storage.BoardChanged, &run), now) {
		t.Fatal("a piece added since its last run did not wake it")
	}
	both := storage.Automation{ProjectID: p.ID, LastRunAt: &later, Config: storage.AutomationConfig{EveryMinutes: 30,
		OnlyIf: &storage.AutomationCondition{Collection: "gone", Changed: true, Board: storage.BoardOpen}}}
	if !trigger.HasWork(ctx, st, both, now) {
		t.Fatal("board quiet: the table condition still decides (a missing table lets it run)")
	}
}

// any = open or changed.
func TestOnlyIfBoardAny(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	b, _ := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID, ModelTier: storage.TierBalanced, MaxParallel: 1, State: "stopped"})
	st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "q", Kind: "quest", Status: "found"})
	later := time.Now().Add(time.Hour)
	a := storage.Automation{ProjectID: p.ID, LastRunAt: &later, Config: storage.AutomationConfig{EveryMinutes: 30, OnlyIf: &storage.AutomationCondition{Board: storage.BoardAny}}}
	if !trigger.HasWork(ctx, st, a, time.Now()) {
		t.Fatal("open work, nothing changed: any must still run")
	}
}
