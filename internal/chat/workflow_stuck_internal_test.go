package chat

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/workflow"
)

// Past the call-back retry cap, a chat kept busy must not leave the run
// stuck in "running" forever (sweepOrphans skips a run still held in
// e.wf.byConv): it must fail with a visible error instead.
func TestWfCallBackFailsRunPastRetryCap(t *testing.T) {
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
	coord, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "lead", Name: "Lead"})
	conv, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "run"})
	rec, err := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{
		ProjectID: p.ID, ConversationID: conv.ID, WorkflowKey: "x", WorkflowName: "X",
		CoordinatorID: coord.ID, CoordinatorName: coord.Name, Status: storage.RunRunning,
	})
	if err != nil {
		t.Fatal(err)
	}

	e := &Engine{store: st}
	e.wf.init()
	run := &wfRun{rec: rec, def: workflow.Def{Name: "X"}, coord: coord, done: make(chan struct{}), tries: 239}
	e.wf.byConv[conv.ID] = run

	// the chat is busy with another turn, as when a role's own background
	// work is still running in the same conversation
	e.active = map[string]*Turn{conv.ID: {ID: "busy-turn"}}

	e.wfCallBack(rec.ID, conv, p)

	if e.runHeld(rec) != nil {
		t.Fatal("run still held after its retry cap: sweepOrphans will skip it forever")
	}
	got, err := st.WorkflowRuns().Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != storage.RunFailed || got.Error == "" {
		t.Fatalf("run = %+v", got)
	}
}
