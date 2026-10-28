package burn_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-210: a task of the board handed to an agent with delegate is in
// progress outside the Burn, then done with the agent's answer; a task of
// another project's board, or one the Burn is doing, is left as it is.
func TestBoardHandoff(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	other, _ := st.Repos().Create(ctx, storage.Repo{Name: "o", Path: t.TempDir()})
	b, err := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	add := func(status string) storage.BurnItem {
		it, err := st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "t-" + status, Kind: burn.KindQuest, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	svc := burn.New(st, nil, nil)
	task, busy := add("found"), add("doing")
	svc.OnBoard(ctx, chat.BoardEvent{ProjectID: other.ID, Task: task.ID, Agent: "Dev"})
	if it, _ := st.Burn().Item(ctx, task.ID); it.Status != "found" {
		t.Fatalf("another project's hand-off moved it: %s", it.Status)
	}
	svc.OnBoard(ctx, chat.BoardEvent{ProjectID: p.ID, Task: task.ID, Agent: "Dev"})
	if it, _ := st.Burn().Item(ctx, task.ID); it.Status != burn.StatusHanded {
		t.Fatalf("handed: %s", it.Status)
	}
	svc.OnBoard(ctx, chat.BoardEvent{ProjectID: p.ID, Task: task.ID, Agent: "Dev", Done: true, Result: "đã sửa"})
	if it, _ := st.Burn().Item(ctx, task.ID); it.Status != "done" || it.Summary != "Dev: đã sửa" {
		t.Fatalf("done: %s %q", it.Status, it.Summary)
	}
	svc.OnBoard(ctx, chat.BoardEvent{ProjectID: p.ID, Task: busy.ID, Agent: "Dev"})
	if it, _ := st.Burn().Item(ctx, busy.ID); it.Status != "doing" {
		t.Fatalf("took the Burn's piece: %s", it.Status)
	}
	failed := add("queued")
	svc.OnBoard(ctx, chat.BoardEvent{ProjectID: p.ID, Task: failed.ID, Agent: "Dev"})
	svc.OnBoard(ctx, chat.BoardEvent{ProjectID: p.ID, Task: failed.ID, Agent: "Dev", Done: true, Failed: true})
	if it, _ := st.Burn().Item(ctx, failed.ID); it.Status != "failed" {
		t.Fatalf("failed: %s", it.Status)
	}
}

// ADR-221: an agent's next task is the oldest quest waiting for it (its own,
// or one with no owner for the default agent); none while the Burn runs.
func TestBoardNext(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	b, err := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	add := func(title, owner, status, kind string) storage.BurnItem {
		it, err := st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: title, Owner: owner, Kind: kind, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	svc := burn.New(st, nil, nil)
	lead, dev := storage.Agent{Key: "lead"}, storage.Agent{Key: "dev"}
	add("handed", "lead", burn.StatusHanded, burn.KindQuest)
	add("found by a worker", "lead", "found", "bug")
	if _, ok := svc.Next(ctx, p.ID, lead, true); ok {
		t.Fatal("nothing waits for lead")
	}
	first := add("first", "", "found", burn.KindQuest)
	add("second", "lead", "queued", burn.KindQuest)
	next, ok := svc.Next(ctx, p.ID, lead, true)
	if !ok || next.Task != first.ID || next.Open != 2 {
		t.Fatalf("lead: %+v %v", next, ok)
	}
	if next, ok := svc.Next(ctx, p.ID, dev, false); ok {
		t.Fatalf("dev has none: %+v", next)
	}
	before := next.Sig
	it, _ := st.Burn().Item(ctx, first.ID)
	it.Status = "done"
	st.Burn().UpdateItem(ctx, it)
	if next, _ := svc.Next(ctx, p.ID, lead, true); next.Sig == before || next.Title != "second" {
		t.Fatalf("after one is done: %+v", next)
	}
	b.State = "running"
	st.Burn().SaveSession(ctx, b)
	if _, ok := svc.Next(ctx, p.ID, lead, true); ok {
		t.Fatal("the running Burn takes them")
	}
}
