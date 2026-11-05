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

func mergeStore(t *testing.T) (storage.Store, storage.Repo, storage.Conversation) {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	own, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "run"})
	return st, p, own
}

func countNotes(t *testing.T, st storage.Store, convID, word string) int {
	t.Helper()
	msgs, _ := st.Chat().ListMessages(context.Background(), convID)
	n := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, word) {
			n++
		}
	}
	return n
}

// A diff no one decides does not hold the step until the run's deadline:
// the chat is reminded while it waits, and past the cap the step runs,
// saying what it runs without.
func TestWaitMergedRemindsAndGivesUp(t *testing.T) {
	st, p, own := mergeStore(t)
	ctx := context.Background()
	e := &Engine{store: st}
	oldPoll, oldRemind, oldMax := mergeWaitPoll, mergeWaitRemind, mergeWaitMax
	mergeWaitPoll, mergeWaitRemind, mergeWaitMax = 5*time.Millisecond, 40*time.Millisecond, 200*time.Millisecond
	defer func() { mergeWaitPoll, mergeWaitRemind, mergeWaitMax = oldPoll, oldRemind, oldMax }()
	_, _ = st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, Diff: "x", Files: []string{"a.go"}, Status: "pending", Origin: "worktree"})
	_ = p
	done := make(chan error, 1)
	go func() { done <- e.waitMerged(ctx, own, workflow.Step{ID: "kiem", Type: workflow.StepCheck}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the step still waits past the cap")
	}
	if n := countNotes(t, st, own.ID, "vẫn đợi"); n < 1 {
		t.Fatalf("reminders = %d", n)
	}
	if n := countNotes(t, st, own.ID, "chạy luôn"); n != 1 || countNotes(t, st, own.ID, "a.go") < 2 {
		t.Fatal("no note that the step ran without the diff a.go")
	}
}

// A sub-workflow (or coordinate) step runs in a chat of its own: a code or
// check step after it waits for that run's diffs too.
func TestStepWaitsForChildRunDiffs(t *testing.T) {
	st, p, own := mergeStore(t)
	ctx := context.Background()
	e := &Engine{store: st}
	old := mergeWaitPoll
	mergeWaitPoll = 10 * time.Millisecond
	defer func() { mergeWaitPoll = old }()
	parent, _ := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: own.ID, Status: storage.RunRunning})
	sub, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "sub", Purpose: RunPurpose})
	_, _ = st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: sub.ID, CallerConversationID: own.ID, ParentRunID: parent.ID, Depth: 1, Status: storage.RunDone})
	// and its own sub-run, one level down
	subsub, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "subsub", Purpose: RunPurpose})
	_, _ = st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: subsub.ID, CallerConversationID: sub.ID, Depth: 2, Status: storage.RunDone})
	patch, _ := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: subsub.ID, Diff: "x", Files: []string{"c.go"}, Status: "pending", Origin: "worktree"})
	// a run of another chat is not this run's
	elsewhere, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "else"})
	_, _ = st.Chat().AddPatch(ctx, storage.Patch{ConversationID: elsewhere.ID, Diff: "y", Files: []string{"d.go"}, Status: "pending", Origin: "worktree"})

	done := make(chan error, 1)
	go func() { done <- e.waitMerged(ctx, own, workflow.Step{ID: "kiem", Type: workflow.StepCheck}) }()
	select {
	case err := <-done:
		t.Fatalf("ran while the sub-run's diff waits: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	_ = st.Chat().DecidePatch(ctx, patch.ID, "applied", "", "human:a", time.Now())
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("still waiting after the sub-run's diff merged")
	}
}

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
