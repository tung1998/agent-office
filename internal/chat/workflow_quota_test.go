package chat_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// shortStepWorkflow is flakyStepWorkflow with one second of time.
var shortStepWorkflow = strings.Replace(flakyStepWorkflow, "steps:\n", "limits: { timeout: 1s }\nsteps:\n", 1)

// quotaRun starts a run of src whose step's agent is out of quota until
// reset, and returns its fixture and the chat that called it.
func quotaRun(t *testing.T, src, reset string) (fixture, storage.Conversation) {
	t.Helper()
	f, _ := quotaFixture(t, 1, reset)
	ctx := context.Background()
	agents, _ := f.st.Agents().List(ctx, f.project.ID)
	svc := &workflow.Service{Store: f.st, Lib: workflow.Library{Dir: filepath.Join(t.TempDir(), "workflows")}}
	if _, err := svc.Create(ctx, f.project.ID, src, map[string]string{"lam": agents[0].ID}); err != nil {
		t.Fatal(err)
	}
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	if _, _, err := f.engine.Send(ctx, conv.ID, "#lam", nil); err != nil {
		t.Fatal(err)
	}
	return f, conv
}

func lastRun(f fixture, conv storage.Conversation) (storage.WorkflowRun, bool) {
	runs, _ := f.st.WorkflowRuns().List(context.Background(), f.project.ID, conv.ID, 1)
	if len(runs) == 0 {
		return storage.WorkflowRun{}, false
	}
	return runs[0], true
}

// A step waiting for its agent's quota stops the run's clock: a run of one
// second whose step waits three for the reset is not told it ran past its
// time (a weekly quota used to read as a run to stop), and the time it
// stood still is kept with it (ADR-270).
func TestQuotaWaitPausesRunDeadline(t *testing.T) {
	f, conv := quotaRun(t, shortStepWorkflow, "3s")
	var r storage.WorkflowRun
	until(t, "the run ended", func() bool {
		var ok bool
		r, ok = lastRun(f, conv)
		return ok && r.Status != storage.RunRunning
	})
	if r.Status != storage.RunDone {
		t.Fatalf("run = %s %q", r.Status, r.Error)
	}
	msgs, _ := f.st.Chat().ListMessages(context.Background(), r.ConversationID)
	for _, m := range msgs {
		if strings.Contains(m.Content, "quá thời gian dự kiến") {
			t.Fatalf("told it ran past its time while it waited for quota: %q", m.Content)
		}
	}
	if r.Checkpoint == nil || r.Checkpoint.Paused < 2*time.Second {
		t.Fatalf("the wait was not kept as time stood still: %+v", r.Checkpoint)
	}
}

// Chạy ngay ends a step's quota wait at once: the step tries its agent now
// instead of waiting for the reset (ADR-270).
func TestSkipQuotaWait(t *testing.T) {
	f, conv := quotaRun(t, flakyStepWorkflow, "1h")
	var r storage.WorkflowRun
	until(t, "the step waits for its quota", func() bool {
		var ok bool
		r, ok = lastRun(f, conv)
		return ok && r.Checkpoint != nil && r.Checkpoint.Marks["lam"].QuotaUntil != nil
	})
	if r.Checkpoint.Marks["lam"].QuotaFrom == nil {
		t.Fatal("the wait does not say since when")
	}
	if _, err := f.engine.SkipQuotaWait("run_nope"); !errors.Is(err, chat.ErrNoQuotaWait) {
		t.Fatalf("skip of no run = %v", err)
	}
	if n, err := f.engine.SkipQuotaWait(r.ID); err != nil || n != 1 {
		t.Fatalf("skip = %d %v", n, err)
	}
	until(t, "the run ended", func() bool {
		r, _ = f.st.WorkflowRuns().Get(context.Background(), r.ID)
		return r.Status != storage.RunRunning
	})
	if r.Status != storage.RunDone || r.Result != "xong phần còn lại" {
		t.Fatalf("run = %s %q %q", r.Status, r.Result, r.Error)
	}
	if _, err := f.engine.SkipQuotaWait(r.ID); !errors.Is(err, chat.ErrNoQuotaWait) {
		t.Fatalf("skip after the end = %v", err)
	}
}

// A reset further off than office waits for a step fails the run, saying
// why, instead of holding it for weeks (ADR-270).
func TestQuotaWaitTooLongFails(t *testing.T) {
	old := chat.QuotaMaxWait
	chat.QuotaMaxWait = 30 * time.Minute
	t.Cleanup(func() { chat.QuotaMaxWait = old })
	f, conv := quotaRun(t, flakyStepWorkflow, "1h")
	var r storage.WorkflowRun
	until(t, "the run ended", func() bool {
		var ok bool
		r, ok = lastRun(f, conv)
		return ok && r.Status != storage.RunRunning
	})
	if r.Status != storage.RunFailed || !strings.Contains(r.Error, "hết quota tới") {
		t.Fatalf("run = %s %q", r.Status, r.Error)
	}
}

// A wait office went down under, that the quota reset during, still does
// not count against the run's time: the next office adds it to the time
// the run stood still (ADR-270).
func TestQuotaWaitDeadlineSurvivesRestart(t *testing.T) {
	f, conv := quotaRun(t, flakyStepWorkflow, "1h")
	ctx := context.Background()
	var r storage.WorkflowRun
	until(t, "the step waits for its quota", func() bool {
		var ok bool
		r, ok = lastRun(f, conv)
		return ok && r.Checkpoint != nil && r.Checkpoint.Marks["lam"].QuotaUntil != nil
	})
	f.engine.Shutdown(2 * time.Second)
	if _, err := f.st.Jobs().FailRunning(ctx, "restart", "office khởi động lại", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	r, _ = f.st.WorkflowRuns().Get(ctx, r.ID)
	from, past := time.Now().UTC().Add(-10*time.Minute), time.Now().UTC().Add(-time.Minute)
	m := r.Checkpoint.Marks["lam"]
	m.QuotaFrom, m.QuotaUntil = &from, &past
	r.Checkpoint.Marks["lam"] = m
	if err := f.st.WorkflowRuns().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	provs, _ := f.st.Providers().List(ctx)
	for _, pr := range provs {
		_ = f.st.Settings().Set(ctx, chat.LimitsKey(pr.ID), chat.Limits{Status: "allowed", UpdatedAt: time.Now()})
	}
	next := chat.NewEngine(f.st, f.provs, usage.New(f.st, time.UTC))
	svc := &workflow.Service{Store: f.st}
	keep := next.ResumableRuns(ctx)
	if _, err := svc.EndInterrupted(ctx, "restart", keep); err != nil {
		t.Fatal(err)
	}
	next.ResumeWorkflows(ctx, "restart", keep)
	until(t, "the run ended", func() bool {
		r, _ = f.st.WorkflowRuns().Get(ctx, r.ID)
		return r.Status != storage.RunRunning
	})
	if r.Status != storage.RunDone {
		t.Fatalf("run = %s %q", r.Status, r.Error)
	}
	if p := r.Checkpoint.Paused; p < 8*time.Minute || p > 12*time.Minute {
		t.Fatalf("paused = %v, want the 9 minutes it waited while office was down", p)
	}
}
