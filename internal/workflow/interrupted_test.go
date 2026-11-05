package workflow_test

import (
	"context"
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
	if n, err := (&workflow.Service{Store: st}).EndInterrupted(ctx, why); err != nil || n != 1 {
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
