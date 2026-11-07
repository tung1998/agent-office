package workflow_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A run office restarted under ends failed, and the chat that called it is
// told why office went down (an update, a stop, a crash), not only that it
// did (ADR-255).
func TestEndInterruptedSaysWhy(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/p"})
	caller, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "gọi"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, CallerConversationID: caller.ID, WorkflowKey: "daily-upgrade",
		WorkflowName: "Nâng cấp hằng ngày", Status: storage.RunRunning})
	if err != nil {
		t.Fatal(err)
	}
	why := "cập nhật lên bản mới, do admin@x.io yêu cầu; ngắt việc đang chạy: 1 quy trình (Nâng cấp hằng ngày)"
	if n, err := (&workflow.Service{Store: st}).EndInterrupted(ctx, why, nil); err != nil || n != 1 {
		t.Fatalf("EndInterrupted = %d, %v", n, err)
	}
	got, _ := st.WorkflowRuns().Get(ctx, run.ID)
	if got.Status != storage.RunFailed || !strings.Contains(got.Error, "admin@x.io") {
		t.Fatalf("run = %s %q", got.Status, got.Error)
	}
	msgs, _ := st.Chat().ListMessages(ctx, caller.ID)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, why) || !strings.Contains(msgs[0].Content, "/daily-upgrade") {
		t.Fatalf("messages = %+v", msgs)
	}
}

// A run that goes on after the restart (ADR-256) is left running; a sub-run
// of it that cannot go on ends without telling the chats (its parent's step
// runs it again), a run that kept where it was is told it may go on.
func TestEndInterruptedKeepsResumable(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/p"})
	caller, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "gọi"})
	sub, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "con", Purpose: "workflow_run"})
	kept, _ := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, CallerConversationID: caller.ID, WorkflowKey: "a", Status: storage.RunRunning,
		Checkpoint: &storage.RunCheckpoint{}})
	child, _ := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: sub.ID, CallerConversationID: caller.ID, ParentRunID: kept.ID,
		WorkflowKey: "b", Status: storage.RunRunning})
	other, _ := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, CallerConversationID: caller.ID, WorkflowKey: "c", Status: storage.RunRunning,
		Checkpoint: &storage.RunCheckpoint{}})
	n, err := (&workflow.Service{Store: st}).EndInterrupted(ctx, "", map[string]bool{kept.ID: true})
	if err != nil || n != 2 {
		t.Fatalf("EndInterrupted = %d, %v", n, err)
	}
	if got, _ := st.WorkflowRuns().Get(ctx, kept.ID); got.Status != storage.RunRunning {
		t.Fatalf("kept = %s", got.Status)
	}
	for _, id := range []string{child.ID, other.ID} {
		if got, _ := st.WorkflowRuns().Get(ctx, id); got.Status != storage.RunFailed || !strings.HasPrefix(got.Error, workflow.InterruptedDetail) {
			t.Fatalf("%s = %s %q", id, got.Status, got.Error)
		}
	}
	if msgs, _ := st.Chat().ListMessages(ctx, sub.ID); len(msgs) != 0 {
		t.Fatalf("the sub-run's chat was told: %+v", msgs)
	}
	msgs, _ := st.Chat().ListMessages(ctx, caller.ID)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "/c") || !strings.Contains(msgs[0].Content, "Chạy tiếp từ bước dở") {
		t.Fatalf("caller = %+v", msgs)
	}
}

// A run stuck Running for a long time (held for approval) must not be
// dropped from EndInterrupted's view by hundreds of runs that started more
// recently and already finished: List's newest-started-first cap of 500
// used to hide it (fixed by ListRunning, which filters by status in SQL).
func TestEndInterruptedFindsOldRunningRunPastListCap(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/p"})
	caller, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "gọi"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, CallerConversationID: caller.ID, WorkflowKey: "held",
		WorkflowName: "Chờ duyệt", Status: storage.RunRunning})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		r, err := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, CallerConversationID: caller.ID,
			WorkflowKey: fmt.Sprintf("w%d", i), Status: storage.RunRunning})
		if err != nil {
			t.Fatal(err)
		}
		r.Status = storage.RunDone
		if err := st.WorkflowRuns().Update(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	n, err := (&workflow.Service{Store: st}).EndInterrupted(ctx, "", nil)
	if err != nil || n != 1 {
		t.Fatalf("EndInterrupted = %d, %v", n, err)
	}
	got, _ := st.WorkflowRuns().Get(ctx, old.ID)
	if got.Status != storage.RunFailed {
		t.Fatalf("old run = %s, want Failed", got.Status)
	}
}
