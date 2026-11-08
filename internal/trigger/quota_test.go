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

// A bot's message while every connection of its agent is out of quota: no AI
// is called, its person is told now when it will be answered, and the
// message waits, queued once for just after the reset; then it runs and its
// answer goes to the chat (ADR-269).
func TestBotMessageWaitsForQuota(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a", Name: "Trợ lý", ModelTier: "fast"})
	ex := &recExec{reply: "Đơn 123 đang giao"}
	r := trigger.New(st, ex)
	rs := &replies{}
	r.SetOnReply(rs.hook)
	until := time.Now().Add(3 * time.Hour)
	out := true
	r.SetQuotaStop(func(context.Context, storage.Agent) (time.Time, bool) { return until, out })
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "Trả lời", Source: "telegram", Action: "chat", Enabled: true,
		AgentID: ag.ID, Config: storage.AutomationConfig{ChannelID: "chn_1"}})
	runChannel(t, r, st, a, channelPayload("cnv_thread"))
	if len(ex.convs) != 0 {
		t.Fatalf("AI ran while out of quota: %q", ex.prompts)
	}
	if len(rs.got) != 1 || !rs.got[0].final || rs.got[0].err != nil ||
		!strings.Contains(rs.got[0].text, until.Local().Format("02/01 15:04")) || !strings.Contains(rs.got[0].text, "Trợ lý") {
		t.Fatalf("replies = %+v", rs.got)
	}
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID})
	if len(jobs) != 2 {
		t.Fatalf("jobs = %+v", jobs)
	}
	waiting, skipped := jobs[0], jobs[1]
	if skipped.Status != "skipped" || skipped.ErrorCode != trigger.QuotaCode {
		t.Fatalf("first: %+v", skipped)
	}
	if waiting.Status != "pending" || waiting.NextAttemptAt == nil || waiting.NextAttemptAt.Before(until) ||
		!strings.Contains(waiting.Payload, `"quota_waited":true`) || !strings.Contains(waiting.Payload, "đơn 123") {
		t.Fatalf("waiting: %+v", waiting)
	}
	// not before the reset
	r.Tick(ctx, time.Now().UTC().Add(time.Hour))
	r.Wait()
	if len(ex.convs) != 0 {
		t.Fatal("ran before the reset")
	}
	// after it, it runs, even if the report still says out (put off once only)
	r.Tick(ctx, until.Add(time.Hour).UTC())
	r.Wait()
	if len(ex.convs) != 1 || ex.convs[0] != "cnv_thread" || ex.prompts[0] != "đơn 123 đâu rồi?" {
		t.Fatalf("after the reset: convs=%q prompts=%q", ex.convs, ex.prompts)
	}
	if last := rs.got[len(rs.got)-1]; last.text != "Đơn 123 đang giao" || !last.final {
		t.Fatalf("answer: %+v", rs.got)
	}
	failed, _ := st.Jobs().Failed(ctx, storage.JobFilter{ProjectID: p.ID})
	if len(failed) != 0 {
		t.Fatalf("counted as a failure: %+v", failed)
	}
}

// A bot's message put off for quota is checked again when it runs: the
// reset moved on (a weekly limit read after a daily one), so it waits again
// for the new one, no AI called and its person not told twice; once the
// quota is back it is answered (ADR-270).
func TestBotDeferredRechecksQuota(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ag, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a", Name: "Trợ lý", ModelTier: "fast"})
	ex := &recExec{reply: "Đơn 123 đang giao"}
	r := trigger.New(st, ex)
	rs := &replies{}
	r.SetOnReply(rs.hook)
	until, out := time.Now().Add(3*time.Hour), true
	r.SetQuotaStop(func(context.Context, storage.Agent) (time.Time, bool) { return until, out })
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "Trả lời", Source: "telegram", Action: "chat", Enabled: true,
		AgentID: ag.ID, Config: storage.AutomationConfig{ChannelID: "chn_1"}})
	runChannel(t, r, st, a, channelPayload("cnv_thread"))
	first := until
	until = time.Now().Add(30 * time.Hour) // the weekly limit, read once the daily one reset
	r.Tick(ctx, first.Add(time.Hour).UTC())
	r.Wait()
	if len(ex.convs) != 0 {
		t.Fatalf("AI ran while still out of quota: %q", ex.prompts)
	}
	if len(rs.got) != 1 {
		t.Fatalf("its person was told again: %+v", rs.got)
	}
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID, Status: "pending"})
	if len(jobs) != 1 || jobs[0].NextAttemptAt == nil || jobs[0].NextAttemptAt.Before(until) ||
		!strings.Contains(jobs[0].Payload, `"quota_waits":2`) || !strings.Contains(jobs[0].Payload, "đơn 123") {
		t.Fatalf("waiting again: %+v", jobs)
	}
	out = false
	r.Tick(ctx, until.Add(time.Hour).UTC())
	r.Wait()
	if len(ex.convs) != 1 || ex.prompts[0] != "đơn 123 đâu rồi?" {
		t.Fatalf("after the new reset: convs=%q prompts=%q", ex.convs, ex.prompts)
	}
	if last := rs.got[len(rs.got)-1]; last.text != "Đơn 123 đang giao" {
		t.Fatalf("answer: %+v", rs.got)
	}
}

// A message put off as often as office waits, or a reset further off than
// it waits in all, runs: the service then says why it cannot answer, rather
// than its person waiting with no end (ADR-270).
func TestBotDeferGivesUp(t *testing.T) {
	for name, tc := range map[string]struct {
		defers int
		next   time.Duration
	}{
		"too often": {defers: 1, next: 30 * time.Hour},
		"too far":   {defers: 3, next: 20 * 24 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			old := trigger.BotQuotaDefers
			trigger.BotQuotaDefers = tc.defers
			t.Cleanup(func() { trigger.BotQuotaDefers = old })
			ctx := context.Background()
			st, p := openStore(t)
			ag, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a", Name: "Trợ lý", ModelTier: "fast"})
			ex := &recExec{reply: "ok"}
			r := trigger.New(st, ex)
			r.SetOnReply((&replies{}).hook)
			until := time.Now().Add(3 * time.Hour)
			r.SetQuotaStop(func(context.Context, storage.Agent) (time.Time, bool) { return until, true })
			a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "Trả lời", Source: "telegram", Action: "chat", Enabled: true,
				AgentID: ag.ID, Config: storage.AutomationConfig{ChannelID: "chn_1"}})
			runChannel(t, r, st, a, channelPayload("cnv_thread"))
			first := until
			until = time.Now().Add(tc.next)
			r.Tick(ctx, first.Add(time.Hour).UTC())
			r.Wait()
			if len(ex.convs) != 1 {
				t.Fatalf("convs = %q, want it run", ex.convs)
			}
		})
	}
}
