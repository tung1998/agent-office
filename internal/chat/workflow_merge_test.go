package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A code/check step reads the project: it waits while a diff of the run (or
// a commit held for one) still waits to be merged, and says so once, instead
// of failing on a project that has not got the work yet.
func TestStepWaitsForRunDiffs(t *testing.T) {
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
	own, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "run"})
	e := &Engine{store: st}
	old := mergeWaitPoll
	mergeWaitPoll = 10 * time.Millisecond
	defer func() { mergeWaitPoll = old }()
	step := workflow.Step{ID: "kiem", Type: workflow.StepCode}

	// nothing waits: no wait, no note
	if err := e.waitMerged(ctx, own, step); err != nil {
		t.Fatal(err)
	}
	// an unrelated command card waiting for a person does not hold the step
	_, _ = st.Actions().Create(ctx, storage.Action{ProjectID: p.ID, ConversationID: own.ID, Kind: "run_command", Target: "ls", Status: "pending"})
	if err := e.waitMerged(ctx, own, step); err != nil {
		t.Fatal(err)
	}

	patch, _ := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, Diff: "x", Files: []string{"a.go"}, Status: "pending", Origin: "worktree"})
	commit, _ := st.Actions().Create(ctx, storage.Action{ProjectID: p.ID, ConversationID: own.ID, Kind: "git_commit", Target: "c", Status: "queued",
		Args: storage.ActionArgs{AfterDiff: true, Files: []string{"a.go"}}})
	done := make(chan error, 1)
	go func() { done <- e.waitMerged(ctx, own, step) }()
	select {
	case err := <-done:
		t.Fatalf("ran while the diff waits: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	_ = st.Chat().DecidePatch(ctx, patch.ID, "applied", "", "human:a", time.Now())
	select {
	case err := <-done:
		t.Fatalf("ran before the held commit: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	commit.Status = "done"
	_ = st.Actions().Update(ctx, commit)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("still waiting after the merge")
	}
	msgs, _ := st.Chat().ListMessages(ctx, own.ID)
	notes := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, "Chờ") && strings.Contains(m.Content, "gộp") {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("notes = %d: %+v", notes, msgs)
	}

	// stopped while waiting: the step ends with the run
	_, _ = st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, Diff: "y", Files: []string{"b.go"}, Status: "pending", Origin: "worktree"})
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := e.waitMerged(cctx, own, step); err == nil {
		t.Fatal("a stopped run still waits")
	}
}
