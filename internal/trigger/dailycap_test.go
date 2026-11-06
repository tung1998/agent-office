package trigger_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// spend leaves a finished job of a's tree that cost usd, created at at: a
// workflow step its run started (origin user, its root the automation's).
func spend(t *testing.T, st storage.Store, a storage.Automation, usd float64, at time.Time) {
	t.Helper()
	if _, err := st.Jobs().Create(context.Background(), storage.Job{ProjectID: a.ProjectID, Kind: "chat_turn", Origin: "user", Trigger: "ui", Status: "done",
		CreatedAt: at, StartedAt: &at, FinishedAt: &at, CostUSD: usd, RootJobID: "job_root", RootAutomationID: a.ID}); err != nil {
		t.Fatal(err)
	}
}

// The daily cap counts the workflow steps an automation's runs started: at
// the cap the next run does not start. Their jobs used to be nobody's, so a
// workflow automation went on far past its cap (ADR-263).
func TestDailyCapCountsWorkflowSteps(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "upgrade", Source: "schedule", Action: "workflow", Enabled: true,
		Prompt: "x", Config: storage.AutomationConfig{EveryMinutes: 60, Workflow: "daily-upgrade"}, Limits: storage.AutomationLimits{DailyCostUSD: 1}})
	spend(t, st, a, 2, time.Now().UTC())
	if _, _, err := r.Enqueue(ctx, a, "schedule", "", "", ""); err != nil {
		t.Fatal(err)
	}
	r.StartReady(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.chats) != 0 {
		t.Fatalf("ran past its cap: %q", ex.chats)
	}
	j := mainJob(t, st, a)
	if j.Status != "skipped" || j.ErrorCode != "budget" || !strings.Contains(j.Error, "$2.00/$1.00") {
		t.Fatalf("job = %s %s %q", j.Status, j.ErrorCode, j.Error)
	}
	if got, _ := st.Automations().Get(ctx, a.ID); !got.Enabled {
		t.Fatalf("a daily cap turned it off for good: %+v", got)
	}
}

// A cap is a day's: the automation stays on and runs again the next day. One
// turned off at its cap before (disabled_code daily_cost) is turned back on
// once its day turned, and not while it is still over (ADR-263).
func TestDailyCapResumesNextDay(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	r := trigger.New(st, &fakeExec{})
	now := time.Now().UTC()
	mk := func(name string) storage.Automation {
		a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: name, Source: "schedule", Action: "chat", Enabled: true,
			Prompt: "x", Config: storage.AutomationConfig{EveryMinutes: 60}, Limits: storage.AutomationLimits{DailyCostUSD: 1}})
		if err := st.Automations().Disable(ctx, a.ID, trigger.DailyCapCode, "chạm trần chi phí trong ngày", nil); err != nil {
			t.Fatal(err)
		}
		return a
	}
	yesterday, today := mk("yesterday"), mk("today")
	spend(t, st, yesterday, 5, now.AddDate(0, 0, -1))
	spend(t, st, today, 5, now)
	r.Tick(ctx, now)
	r.Wait()
	if got, _ := st.Automations().Get(ctx, yesterday.ID); !got.Enabled || got.DisabledCode != "" || got.NextRunAt == nil {
		t.Fatalf("a cap reached yesterday still keeps it off: %+v", got)
	}
	if got, _ := st.Automations().Get(ctx, today.ID); got.Enabled || got.DisabledCode != trigger.DailyCapCode {
		t.Fatalf("turned on while still over today's cap: %+v", got)
	}
}

// stepSpender is a goal's agent whose turns run a workflow: each round's
// steps cost what it is given, under the automation.
type stepSpender struct {
	goalExec
	st   storage.Store
	a    *storage.Automation
	cost float64
	t    *testing.T
}

func (s *stepSpender) RunChat(ctx context.Context, projectID, agentID, conv, prompt, edit string) (string, string, error) {
	spend(s.t, s.st, *s.a, s.cost, time.Now().UTC())
	return s.goalExec.RunChat(ctx, projectID, agentID, conv, prompt, edit)
}

// A goal's rounds stop at the daily cap, the workflow steps its turns ran
// counted (ADR-263).
func TestGoalDailyCapCountsWorkflow(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &stepSpender{goalExec: goalExec{dir: p.Path}, st: st, cost: 2, t: t}
	r := trigger.New(st, ex)
	a := goalAutomation(t, st, p, storage.AutomationGoal{Check: "test -f done", MaxRounds: 5})
	a.Limits.DailyCostUSD = 1
	if err := st.Automations().Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	ex.a = &a
	runOnce(t, r, st, a, "")
	if len(ex.prompts) != 1 {
		t.Fatalf("rounds went on past the cap: %d", len(ex.prompts))
	}
	if j := mainJob(t, st, a); !strings.Contains(j.Output, "chạm trần chi phí trong ngày") {
		t.Fatalf("job output = %q", j.Output)
	}
}
