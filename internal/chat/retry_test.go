package chat_test

import (
	"context"
	"os"
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
)

// flakyClaude is a Claude Code that gives up on an overloaded API on its
// first fails calls (each prints its session and the API retries it gave up
// on), then answers. calls counts them.
func flakyClaude(t *testing.T, fails int) (bin, calls string) {
	tmp := t.TempDir()
	bin, calls = filepath.Join(tmp, "claude"), filepath.Join(tmp, "calls")
	fakebin.Write(bin, []byte(`#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
printf '%s' "$*" | tr '\n' ' ' >> `+calls+`
echo >> `+calls+`
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
if [ "$(wc -l < `+calls+`)" -le `+strconv.Itoa(fails)+` ]; then
  echo '{"type":"system","subtype":"api_retry","attempt":10,"max_retries":10,"error_status":529,"error":"overloaded_error"}'
  exit 1
fi
echo '{"type":"result","subtype":"success","is_error":false,"result":"xong phần còn lại","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`))
	return bin, calls
}

func countLines(path string) int {
	b, _ := os.ReadFile(path)
	if strings.TrimSpace(string(b)) == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(string(b)), "\n"))
}

func shortRetries(t *testing.T, waits ...time.Duration) {
	old, oldSpacing := chat.TransientRetryWaits, chat.RetryDueSpacing
	chat.TransientRetryWaits, chat.RetryDueSpacing = waits, 10*time.Millisecond
	t.Cleanup(func() { chat.TransientRetryWaits, chat.RetryDueSpacing = old, oldSpacing })
}

func flakyFixture(t *testing.T, fails int) (fixture, string) {
	bin, calls := flakyClaude(t, fails)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	return f, calls
}

// waitJobs waits for the chat to have n turns, the last one ended; it
// returns the last one (newest first in the list).
func waitJobs(t *testing.T, f fixture, convID string, n int) storage.Job {
	t.Helper()
	var jobs []storage.Job
	until(t, "turn "+strconv.Itoa(n)+" ended", func() bool {
		jobs, _ = f.st.Jobs().List(context.Background(), storage.JobFilter{ConversationID: convID})
		return len(jobs) >= n && jobs[0].Status != "running" && jobs[0].Status != "pending"
	})
	return jobs[0]
}

// A person's turn the API failed past the CLI's own go-on is planned to go
// on, said so in the chat, and goes on by itself in its session (ADR-260).
func TestTransientTurnResumes(t *testing.T) {
	shortRetries(t, 300*time.Millisecond)
	f, calls := flakyFixture(t, 2) // the run and the runner's own go-on both fail
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	first, _, err := f.engine.Send(ctx, conv.ID, "sửa lỗi", nil)
	if err != nil {
		t.Fatal(err)
	}
	if evs := collect(t, first); evs[len(evs)-1].Type != "error" {
		b, _ := os.ReadFile(calls)
		t.Fatalf("first turn: %q calls=%d", evs[len(evs)-1].Message.Content, strings.Count(string(b), "\n"))
	}
	jobs, _ := f.st.Jobs().List(ctx, storage.JobFilter{ConversationID: conv.ID})
	if len(jobs) != 1 || jobs[0].ErrorCode != chat.TransientCode || jobs[0].RetryAt == nil || jobs[0].RetryN != 1 {
		t.Fatalf("job: %+v", jobs)
	}
	if r := f.engine.PlannedRetry(ctx, conv.ID); r == nil || r.N != 1 || r.Max != 3 {
		t.Fatalf("planned: %+v", r)
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, conv.ID)
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "1/3") {
		t.Fatalf("no plan said: %+v", msgs)
	}
	if j := waitJobs(t, f, conv.ID, 2); j.Status != "done" {
		t.Fatalf("went on: %+v", j)
	}
	if msgs, _ := f.st.Chat().ListMessages(ctx, conv.ID); msgs[len(msgs)-1].Content != "xong phần còn lại" {
		t.Fatalf("answer: %+v", msgs[len(msgs)-1])
	}
	b, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[2], "--resume s1") {
		t.Fatalf("calls: %q", lines)
	}
	if r := f.engine.PlannedRetry(ctx, conv.ID); r != nil {
		t.Fatalf("still planned: %+v", r)
	}
	msgs, _ = f.st.Chat().ListMessages(ctx, conv.ID)
	var note storage.Message
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "[office]") {
			note = m
		}
	}
	if note.Author != "human:a@b.c" || !strings.HasPrefix(note.Content, "@") {
		t.Fatalf("go-on message: %+v", note)
	}
}

// The person writing in the chat calls the planned retry off.
func TestTransientRetryCancelledByNewMessage(t *testing.T) {
	shortRetries(t, time.Hour)
	f, calls := flakyFixture(t, 2)
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	first, _, _ := f.engine.Send(ctx, conv.ID, "sửa lỗi", nil)
	collect(t, first)
	planned := f.engine.PlannedRetry(ctx, conv.ID)
	if planned == nil {
		t.Fatal("not planned")
	}
	next, _, err := f.engine.Send(ctx, conv.ID, "thôi, làm việc khác", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, next)
	if j, _ := f.st.Jobs().Get(ctx, planned.JobID); j.RetryAt != nil {
		t.Fatalf("still planned: %+v", j)
	}
	if _, err := f.engine.RetryNow(ctx, planned.JobID); err != chat.ErrNoRetry {
		t.Fatalf("retry after a new message: %v", err)
	}
	if n := countLines(calls); n != 3 {
		t.Fatalf("calls: %d", n)
	}
	// called off from the dashboard: nothing left to go on with
	f2, _ := flakyFixture(t, 2)
	conv2, _ := f2.engine.StartConversation(ctx, f2.project.ID, "")
	t2, _, _ := f2.engine.Send(ctx, conv2.ID, "x", nil)
	collect(t, t2)
	p := f2.engine.PlannedRetry(ctx, conv2.ID)
	if err := f2.engine.CancelRetry(ctx, p.JobID); err != nil || f2.engine.PlannedRetry(ctx, conv2.ID) != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := f2.engine.CancelRetry(ctx, p.JobID); err != chat.ErrNoRetry {
		t.Fatalf("cancel twice: %v", err)
	}
}

// A plan outlives office going down: the next office arms it again.
func TestTransientRetrySurvivesRestart(t *testing.T) {
	shortRetries(t, time.Hour)
	f, _ := flakyFixture(t, 2)
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	first, _, _ := f.engine.Send(ctx, conv.ID, "sửa lỗi", nil)
	collect(t, first)
	p := f.engine.PlannedRetry(ctx, conv.ID)
	f.engine.Shutdown(time.Second)
	due := time.Now().UTC().Add(-time.Minute) // it came due while office was down
	if err := f.st.Jobs().SetRetry(ctx, p.JobID, &due, p.N); err != nil {
		t.Fatal(err)
	}
	next := chat.NewEngine(f.st, f.provs, usage.New(f.st, time.UTC))
	if n := next.ResumeRetries(ctx); n != 1 {
		t.Fatalf("armed %d", n)
	}
	if j := waitJobs(t, f, conv.ID, 2); j.Status != "done" {
		t.Fatalf("went on: %+v", j)
	}
}

// Past the tries in a row it gives up and says the chat waits for the person;
// turned off in the settings, nothing is planned.
func TestTransientGivesUpAfterMax(t *testing.T) {
	shortRetries(t, 50*time.Millisecond)
	f, calls := flakyFixture(t, 100)
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	first, _, _ := f.engine.Send(ctx, conv.ID, "sửa lỗi", nil)
	collect(t, first)
	waitJobs(t, f, conv.ID, 4)
	time.Sleep(300 * time.Millisecond)
	if _, ok := f.engine.Active(conv.ID); ok {
		t.Fatal("a fifth turn")
	}
	jobs, _ := f.st.Jobs().List(ctx, storage.JobFilter{ConversationID: conv.ID})
	if len(jobs) != 4 || jobs[0].RetryAt != nil || countLines(calls) != 8 {
		t.Fatalf("jobs=%d calls=%d", len(jobs), countLines(calls))
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, conv.ID)
	if last := msgs[len(msgs)-1]; !strings.Contains(last.Content, "3 lần liên tiếp") {
		t.Fatalf("last: %+v", last)
	}

	u := usage.New(f.st, time.UTC)
	if err := u.SaveSettings(ctx, usage.Settings{NoTransientRetry: true}); err != nil {
		t.Fatal(err)
	}
	conv2, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	t2, _, _ := f.engine.Send(ctx, conv2.ID, "x", nil)
	collect(t, t2)
	if p := f.engine.PlannedRetry(ctx, conv2.ID); p != nil {
		t.Fatalf("planned while off: %+v", p)
	}
}
