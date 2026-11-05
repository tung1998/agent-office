package trigger_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// While an update waits for office to be idle, a schedule that comes due is
// queued, not started (it would only make the wait longer); a bot's message
// still runs. Once the hold lets go the queued run starts (ADR-255).
func TestHoldKeepsUnattendedJobsQueued(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	var held atomic.Bool
	held.Store(true)
	r.SetHold(held.Load)
	past := time.Now().UTC().Add(-time.Minute)
	sched, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "check", Source: "schedule", Action: "chat", Enabled: true,
		Prompt: "kiểm tra", Config: storage.AutomationConfig{EveryMinutes: 60}, NextRunAt: &past})
	bot, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "bot", Source: "discord", Action: "chat", Enabled: true, Prompt: "{{payload}}"})
	if _, _, err := r.Enqueue(ctx, bot, "discord", `{"text":"chào"}`, "", ""); err != nil {
		t.Fatal(err)
	}
	r.Tick(ctx, time.Now().UTC())
	r.Wait()
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: sched.ID})
	if len(jobs) != 1 || jobs[0].Status != "pending" {
		t.Fatalf("held schedule jobs = %+v", jobs)
	}
	if bj, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: bot.ID}); len(bj) != 1 || bj[0].Status != "done" {
		t.Fatalf("a bot's message must still run: %+v", bj)
	}
	held.Store(false) // the update was cancelled (or office restarted)
	r.Tick(ctx, time.Now().UTC())
	r.Wait()
	jobs, _ = st.Jobs().List(ctx, storage.JobFilter{OriginID: sched.ID})
	if len(jobs) != 1 || jobs[0].Status != "done" {
		t.Fatalf("after the hold: %+v", jobs)
	}
}
