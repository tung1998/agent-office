package trigger_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// Every AI connection of an automation's agent is out of quota until a known
// reset (Codex said "try again at Nov 13th"): a scheduled run is skipped
// with when it is free again, no AI is called, and it is not a failure. The
// automation "Soát office Mac mini mỗi giờ" failed 7 times so (2026-11-07/08).
func TestScheduleSkipsWhileAllConnectionsOutOfQuota(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a", Name: "A", ModelTier: "fast"})
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	until := time.Now().Add(5 * 24 * time.Hour)
	var asked string
	r.SetQuotaStop(func(_ context.Context, a storage.Agent) (time.Time, bool) {
		asked = a.ID
		return until, true
	})
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "h", Source: "webhook", Action: "chat", Enabled: true, AgentID: ag.ID})
	if _, _, err := r.Enqueue(ctx, a, "webhook", "{}", "1", ""); err != nil {
		t.Fatal(err)
	}
	r.Tick(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.chats) != 0 {
		t.Fatalf("AI ran while out of quota: %v", ex.chats)
	}
	if asked != ag.ID {
		t.Fatalf("quota asked for %q, want the automation's agent", asked)
	}
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID})
	if len(jobs) != 1 || jobs[0].Status != "skipped" || jobs[0].ErrorCode != trigger.QuotaCode ||
		!strings.Contains(jobs[0].Error, until.Local().Format("02/01 15:04")) {
		t.Fatalf("jobs = %+v", jobs)
	}
	failed, _ := st.Jobs().Failed(ctx, storage.JobFilter{ProjectID: p.ID})
	if len(failed) != 0 {
		t.Fatalf("a quota skip counted as a failure: %+v", failed)
	}

	// pressed by hand it runs: the person sees what the service says
	if _, _, err := r.Enqueue(ctx, a, "manual", "{}", "2", ""); err != nil {
		t.Fatal(err)
	}
	r.Tick(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.chats) != 1 {
		t.Fatalf("a manual run was skipped: %v", ex.chats)
	}
}

// Once a connection is free again (its reset passed), the next run runs.
func TestScheduleRunsAfterQuotaReset(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a", Name: "A", ModelTier: "fast"})
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	out := true
	r.SetQuotaStop(func(context.Context, storage.Agent) (time.Time, bool) {
		return time.Now().Add(time.Hour), out
	})
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "h", Source: "webhook", Action: "chat", Enabled: true, AgentID: ag.ID})
	if _, _, err := r.Enqueue(ctx, a, "webhook", "{}", "1", ""); err != nil {
		t.Fatal(err)
	}
	r.Tick(ctx, time.Now().UTC())
	r.Wait()
	out = false // the window reset
	if _, _, err := r.Enqueue(ctx, a, "webhook", "{}", "2", ""); err != nil {
		t.Fatal(err)
	}
	r.Tick(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.chats) != 1 {
		t.Fatalf("chats = %v, want the run after the reset only", ex.chats)
	}
	if a2, _ := st.Automations().Get(ctx, a.ID); !a2.Enabled {
		t.Fatal("a quota skip turned the automation off")
	}
}
