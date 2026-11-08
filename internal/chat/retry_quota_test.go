package chat_test

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// quotaClaude is a Claude Code out of quota on its first fails calls (its
// own synthetic message names when it resets: in reset), then answering.
func quotaClaude(t *testing.T, fails int, reset string) (bin, calls string) {
	tmp := t.TempDir()
	bin, calls = filepath.Join(tmp, "claude"), filepath.Join(tmp, "calls")
	fakebin.Write(bin, []byte(`#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
printf '%s' "$*" | tr '\n' ' ' >> `+calls+`
echo >> `+calls+`
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
if [ "$(wc -l < `+calls+`)" -le `+strconv.Itoa(fails)+` ]; then
  echo '{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"You'"'"'ve hit your limit · resets in `+reset+`"}]},"api_error_status":429}'
  echo '{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"You'"'"'ve hit your limit · resets in `+reset+`","session_id":"s1"}'
  exit 1
fi
echo '{"type":"result","subtype":"success","is_error":false,"result":"xong phần còn lại","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`))
	return bin, calls
}

func quotaFixture(t *testing.T, fails int, reset string) (fixture, string) {
	old := chat.QuotaRetrySlack
	chat.QuotaRetrySlack = 50 * time.Millisecond
	t.Cleanup(func() { chat.QuotaRetrySlack = old })
	bin, calls := quotaClaude(t, fails, reset)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	return f, calls
}

// A person's turn stopped because the agent's connection ran out of quota
// is planned to go on just after the reset the CLI named, said so in the
// chat, and goes on by itself in its session (ADR-269).
func TestQuotaPlansRetryAtReset(t *testing.T) {
	f, calls := quotaFixture(t, 1, "1s")
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	before := time.Now()
	first, _, err := f.engine.Send(ctx, conv.ID, "sửa lỗi", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, first)
	jobs, _ := f.st.Jobs().List(ctx, storage.JobFilter{ConversationID: conv.ID})
	if len(jobs) != 1 || jobs[0].ErrorCode != chat.OutOfTokensCode || jobs[0].RetryAt == nil || jobs[0].RetryN != 1 {
		t.Fatalf("job: %+v", jobs)
	}
	// the reset the CLI named, not the 5 minutes of a report without one
	if at := *jobs[0].RetryAt; at.Before(before.Add(time.Second)) || at.After(time.Now().Add(5*time.Second)) {
		t.Fatalf("planned at %v", at)
	}
	r := f.engine.PlannedRetry(ctx, conv.ID)
	if r == nil || !r.Quota || r.N != 1 || r.Max != chat.QuotaRetries {
		t.Fatalf("planned: %+v", r)
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, conv.ID)
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "Hết quota AI") {
		t.Fatalf("no plan said: %+v", last)
	}
	if j := waitJobs(t, f, conv.ID, 2); j.Status != "done" {
		t.Fatalf("went on: %+v", j)
	}
	if n := countLines(calls); n != 2 {
		t.Fatalf("calls: %d", n)
	}
	msgs, _ = f.st.Chat().ListMessages(ctx, conv.ID)
	found := false
	for _, m := range msgs {
		found = found || (m.Role == "user" && strings.Contains(m.Content, "ran out of quota") && m.Author == "human:a@b.c")
	}
	if !found {
		t.Fatalf("no go-on message: %+v", msgs)
	}
}

// Writing in the chat calls the wait off; so does Thôi.
func TestNewMessageCancelsQuotaRetry(t *testing.T) {
	f, _ := quotaFixture(t, 2, "1h")
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	first, _, _ := f.engine.Send(ctx, conv.ID, "sửa lỗi", nil)
	collect(t, first)
	p := f.engine.PlannedRetry(ctx, conv.ID)
	if p == nil || !p.Quota || time.Until(p.At) < 50*time.Minute {
		t.Fatalf("planned: %+v", p)
	}
	if err := f.engine.CancelRetry(ctx, p.JobID); err != nil || f.engine.PlannedRetry(ctx, conv.ID) != nil {
		t.Fatalf("cancel: %v", err)
	}

	conv2, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	t2, _, _ := f.engine.Send(ctx, conv2.ID, "x", nil)
	collect(t, t2)
	p2 := f.engine.PlannedRetry(ctx, conv2.ID)
	if p2 == nil {
		t.Fatal("not planned")
	}
	next, _, _ := f.engine.Send(ctx, conv2.ID, "thôi, làm việc khác", nil)
	collect(t, next)
	if j, _ := f.st.Jobs().Get(ctx, p2.JobID); j.RetryAt != nil {
		t.Fatalf("still planned: %+v", j)
	}
}

// The wait outlives office going down: the next office arms it again.
func TestQuotaRetrySurvivesRestart(t *testing.T) {
	f, _ := quotaFixture(t, 1, "1h")
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	first, _, _ := f.engine.Send(ctx, conv.ID, "sửa lỗi", nil)
	collect(t, first)
	p := f.engine.PlannedRetry(ctx, conv.ID)
	f.engine.Shutdown(time.Second)
	due := time.Now().UTC().Add(-time.Minute) // the quota reset while office was down
	if err := f.st.Jobs().SetRetry(ctx, p.JobID, &due, p.N); err != nil {
		t.Fatal(err)
	}
	// the connection's report reset too
	provs, _ := f.st.Providers().List(ctx)
	for _, pr := range provs {
		_ = f.st.Settings().Set(ctx, chat.LimitsKey(pr.ID), chat.Limits{Status: "allowed", UpdatedAt: time.Now()})
	}
	next := chat.NewEngine(f.st, f.provs, usage.New(f.st, time.UTC))
	if n := next.ResumeRetries(ctx); n != 1 {
		t.Fatalf("armed %d", n)
	}
	if j := waitJobs(t, f, conv.ID, 2); j.Status != "done" {
		t.Fatalf("went on: %+v", j)
	}
}

// Still out of quota after QuotaRetries waits, it gives up and says so.
func TestQuotaGivesUp(t *testing.T) {
	f, calls := quotaFixture(t, 100, "1s")
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	first, _, _ := f.engine.Send(ctx, conv.ID, "sửa lỗi", nil)
	collect(t, first)
	waitJobs(t, f, conv.ID, chat.QuotaRetries+1)
	time.Sleep(2 * time.Second)
	jobs, _ := f.st.Jobs().List(ctx, storage.JobFilter{ConversationID: conv.ID})
	if len(jobs) != chat.QuotaRetries+1 || jobs[0].RetryAt != nil || countLines(calls) != chat.QuotaRetries+1 {
		t.Fatalf("jobs=%d calls=%d", len(jobs), countLines(calls))
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, conv.ID)
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "thôi tự chạy tiếp") {
		t.Fatalf("last: %+v", last)
	}

}

// An agent's step out of quota waits for the reset its CLI named, then goes
// on in its session, instead of failing the run (ADR-269).
func TestStepWaitsForQuota(t *testing.T) {
	f, _ := quotaFixture(t, 1, "1s")
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
	waited := false
	until(t, "the run ended", func() bool {
		runs, _ := f.st.WorkflowRuns().List(ctx, f.project.ID, conv.ID, 1)
		if len(runs) == 0 {
			return false
		}
		r = runs[0]
		if cp := r.Checkpoint; cp != nil && cp.Marks["lam"].QuotaUntil != nil {
			waited = true
			if !workflow.Parked(ctx, f.st, r) { // an update does not wait for it
				t.Errorf("a step waiting for quota is not parked: %+v", cp)
			}
		}
		return r.Status != storage.RunRunning
	})
	if r.Status != storage.RunDone || r.Result != "xong phần còn lại" {
		t.Fatalf("run = %s %q %q", r.Status, r.Result, r.Error)
	}
	if !waited {
		t.Fatal("the wait was never kept with the run")
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, r.ConversationID)
	said := false
	for _, m := range msgs {
		said = said || strings.Contains(m.Content, "hết quota AI")
	}
	if !said {
		t.Fatal("the wait was not said in the run's chat")
	}
}

// A step waiting for its agent's quota outlives office going down: the next
// office waits only for what is left of it, then the step goes on (ADR-269).
func TestStepQuotaWaitSurvivesRestart(t *testing.T) {
	f, _ := quotaFixture(t, 1, "1h")
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
	until(t, "the step waits for its quota", func() bool {
		runs, _ := f.st.WorkflowRuns().List(ctx, f.project.ID, conv.ID, 1)
		if len(runs) == 0 || runs[0].Checkpoint == nil {
			return false
		}
		r = runs[0]
		return r.Checkpoint.Marks["lam"].QuotaUntil != nil
	})
	if q := r.Checkpoint.Marks["lam"].QuotaUntil; time.Until(*q) < 50*time.Minute {
		t.Fatalf("waits until %v, want the reset in 1h", q)
	}
	f.engine.Shutdown(2 * time.Second)
	if _, err := f.st.Jobs().FailRunning(ctx, "restart", "office khởi động lại", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// the quota reset while office was down
	r, _ = f.st.WorkflowRuns().Get(ctx, r.ID)
	past := time.Now().UTC().Add(-time.Minute)
	m := r.Checkpoint.Marks["lam"]
	m.QuotaUntil = &past
	r.Checkpoint.Marks["lam"] = m
	if err := f.st.WorkflowRuns().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	provs, _ := f.st.Providers().List(ctx)
	for _, pr := range provs {
		_ = f.st.Settings().Set(ctx, chat.LimitsKey(pr.ID), chat.Limits{Status: "allowed", UpdatedAt: time.Now()})
	}
	next := chat.NewEngine(f.st, f.provs, usage.New(f.st, time.UTC))
	keep := next.ResumableRuns(ctx)
	if !keep[r.ID] {
		t.Fatalf("not resumable: %v", keep)
	}
	if _, err := svc.EndInterrupted(ctx, "restart", keep); err != nil {
		t.Fatal(err)
	}
	next.ResumeWorkflows(ctx, "restart", keep)
	until(t, "the run ended", func() bool {
		r, _ = f.st.WorkflowRuns().Get(ctx, r.ID)
		return r.Status != storage.RunRunning
	})
	if r.Status != storage.RunDone || r.Result != "xong phần còn lại" {
		t.Fatalf("run = %s %q %q", r.Status, r.Result, r.Error)
	}
}
