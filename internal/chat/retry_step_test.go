package chat_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

const flakyStepWorkflow = `---
key: lam
name: Làm
roles:
  - { key: lam, name: Người làm, access: analyze }
steps:
  - id: lam
    type: agent
    role: lam
    prompt: "Sửa lỗi"
    next: xong
  - id: xong
    type: end
    summary: "{{steps.lam.output}}"
---
`

func flakyStepRun(t *testing.T, fails int) (fixture, storage.WorkflowRun, string) {
	t.Helper()
	old := chat.StepTransientWaits
	chat.StepTransientWaits = []time.Duration{20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	t.Cleanup(func() { chat.StepTransientWaits = old })
	f, calls := flakyFixture(t, fails)
	ctx := context.Background()
	agents, _ := f.st.Agents().List(ctx, f.project.ID)
	svc := &workflow.Service{Store: f.st, Lib: workflow.Library{Dir: filepath.Join(t.TempDir(), "workflows")}}
	if _, err := svc.Create(ctx, f.project.ID, flakyStepWorkflow, map[string]string{"lam": agents[0].ID}); err != nil {
		t.Fatal(err)
	}
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	if _, _, err := f.engine.Send(ctx, conv.ID, "#lam", nil); err != nil {
		t.Fatal(err)
	}
	var r storage.WorkflowRun
	until(t, "the run ended", func() bool {
		runs, _ := f.st.WorkflowRuns().List(ctx, f.project.ID, conv.ID, 1)
		if len(runs) == 0 {
			return false
		}
		r = runs[0]
		return r.Status != storage.RunRunning
	})
	return f, r, calls
}

// An agent's step the AI service failed under goes on in its session instead
// of failing the run: the daily upgrade's "Làm" step died so twice (ADR-260).
func TestStepRetriesTransient(t *testing.T) {
	f, r, calls := flakyStepRun(t, 4) // the step's turn and the runner's go-on, twice
	if r.Status != storage.RunDone || r.Result != "xong phần còn lại" {
		t.Fatalf("run = %s %q %q", r.Status, r.Result, r.Error)
	}
	if n := countLines(calls); n != 5 {
		t.Fatalf("calls = %d", n)
	}
	msgs, _ := f.st.Chat().ListMessages(context.Background(), r.ConversationID)
	waits := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, "Lỗi API tạm thời") {
			waits++
		}
	}
	if waits != 2 {
		t.Fatalf("waits said %d times", waits)
	}
}

// Past its tries the step fails the run as before.
func TestStepFailsAfterTransientCap(t *testing.T) {
	_, r, calls := flakyStepRun(t, 100)
	if r.Status == storage.RunDone || !strings.Contains(r.Error, "529") {
		t.Fatalf("run = %s %q", r.Status, r.Error)
	}
	if n := countLines(calls); n != 8 { // 4 turns, each with the runner's own go-on
		t.Fatalf("calls = %d", n)
	}
}
