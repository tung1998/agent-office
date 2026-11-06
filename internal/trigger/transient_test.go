package trigger_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/usage"
)

// flakyExec fails its first fails runs on an overloaded API (or on msg),
// then answers.
type flakyExec struct {
	mu      sync.Mutex
	fails   int
	msg     string
	prompts []string
	convs   []string
}

func (f *flakyExec) RunChat(ctx context.Context, projectID, agentID, conv, prompt, edit string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts, f.convs = append(f.prompts, prompt), append(f.convs, conv)
	if len(f.prompts) <= f.fails {
		if f.msg != "" {
			return "cnv_t", "", errors.New(f.msg)
		}
		return "cnv_t", "", errors.New("claude: exit status 1 · lỗi API cuối: 529 overloaded_error, lần 10/10")
	}
	return "cnv_t", "xong", nil
}

// An automation's agent run the AI service failed under is put back a while
// later and goes on in the chat it ran in, told why (ADR-260); past the
// tries all told it fails as before.
func TestAutomationRunGoesOnAfterTransient(t *testing.T) {
	old := trigger.TransientWaits
	trigger.TransientWaits = []time.Duration{time.Minute}
	t.Cleanup(func() { trigger.TransientWaits = old })
	ctx := context.Background()
	st, p := openStore(t)
	ex := &flakyExec{fails: 1}
	r := trigger.New(st, ex)
	past := time.Now().UTC().Add(-time.Minute)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "nâng cấp", Source: "schedule", Action: "chat", Enabled: true,
		Prompt: "làm việc hôm nay", Config: storage.AutomationConfig{EveryMinutes: 60 * 24}, NextRunAt: &past})
	now := time.Now().UTC()
	r.Tick(ctx, now)
	r.Wait()
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID})
	if len(jobs) != 1 || jobs[0].Status != "pending" || jobs[0].Resumes != 1 || jobs[0].ConversationID != "cnv_t" ||
		jobs[0].NextAttemptAt == nil || jobs[0].NextAttemptAt.Before(now.Add(50*time.Second)) {
		t.Fatalf("put back: %+v", jobs)
	}
	r.StartReady(ctx, now.Add(2*time.Minute))
	r.Wait()
	if len(ex.prompts) != 2 || ex.convs[1] != "cnv_t" || !strings.Contains(ex.prompts[1], "passing error") {
		t.Fatalf("went on: convs=%v prompts=%q", ex.convs, ex.prompts)
	}
	if j, _ := st.Jobs().Get(ctx, jobs[0].ID); j.Status != "done" {
		t.Fatalf("job: %s %s", j.Status, j.ErrorCode)
	}

	// always failing: put back storage.MaxJobResumes times, then failed
	ex2 := &flakyExec{fails: 100}
	r2 := trigger.New(st, ex2)
	b, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "luôn lỗi", Source: "schedule", Action: "chat", Enabled: true,
		Prompt: "x", Config: storage.AutomationConfig{EveryMinutes: 60 * 24}, NextRunAt: &past})
	r2.Tick(ctx, now)
	r2.Wait()
	for i := range storage.MaxJobResumes {
		r2.StartReady(ctx, now.Add(time.Duration(i+1)*2*time.Minute))
		r2.Wait()
	}
	jobs, _ = st.Jobs().List(ctx, storage.JobFilter{OriginID: b.ID})
	if len(jobs) != 1 || jobs[0].Status != "failed" || jobs[0].ErrorCode != trigger.TransientCode || len(ex2.prompts) != storage.MaxJobResumes+1 {
		t.Fatalf("gave up: %+v runs=%d", jobs, len(ex2.prompts))
	}

	// turned off in the settings: it fails at once
	if err := usage.New(st, nil).SaveSettings(ctx, usage.Settings{NoTransientRetry: true}); err != nil {
		t.Fatal(err)
	}
	ex3 := &flakyExec{fails: 1}
	r3 := trigger.New(st, ex3)
	c, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "tắt", Source: "schedule", Action: "chat", Enabled: true,
		Prompt: "x", Config: storage.AutomationConfig{EveryMinutes: 60 * 24}, NextRunAt: &past})
	r3.Tick(ctx, now)
	r3.Wait()
	if jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: c.ID}); len(jobs) != 1 || jobs[0].Status != "failed" {
		t.Fatalf("off: %+v", jobs)
	}
}

// An automation's run reads a passing failure as the chat, a workflow step and
// Burn do (chat.Transient): a gateway error or a dropped connection is put
// back too; signed out, out of quota or a broken CLI is not.
func TestAutomationTransientSameAsChat(t *testing.T) {
	old := trigger.TransientWaits
	trigger.TransientWaits = []time.Duration{time.Minute}
	t.Cleanup(func() { trigger.TransientWaits = old })
	ctx := context.Background()
	st, p := openStore(t)
	past := time.Now().UTC().Add(-time.Minute)
	for _, c := range []struct {
		msg  string
		back bool
	}{
		{"codex: unexpected status 502 Bad Gateway", true},
		{"codex: unexpected status 503 Service Unavailable", true},
		{"gemini: 504 Gateway Timeout", true},
		{"gemini: read tcp 10.0.0.2:51234: read: connection reset by peer", true},
		{"claude: read ECONNRESET", true},
		{"codex: stream disconnected before completion", true},
		{"codex: socket hang up", true},
		{"codex: You've hit your usage limit", false},
		{"claude: not logged in · Please run /login", false},
		{"codex: Error: spawn /x/codex ENOENT", false},
		{"go test ./... failed", false},
	} {
		ex := &flakyExec{fails: 1, msg: c.msg}
		r := trigger.New(st, ex)
		a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: c.msg, Source: "schedule", Action: "chat", Enabled: true,
			Prompt: "x", Config: storage.AutomationConfig{EveryMinutes: 60 * 24}, NextRunAt: &past})
		r.Tick(ctx, time.Now().UTC())
		r.Wait()
		jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID})
		if len(jobs) != 1 || (jobs[0].Status == "pending") != c.back {
			t.Errorf("%q: put back = %v, job %s %s", c.msg, jobs[0].Status == "pending", jobs[0].Status, jobs[0].ErrorCode)
		}
	}
}
