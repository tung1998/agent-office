package trigger_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// An automation whose agent is paused does not run: the job is skipped with
// the notice and no AI is called; resumed, it runs as before.
func TestAutomationOfPausedAgentIsSkipped(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	m, _ := st.OrgModels().Create(ctx, storage.OrgModel{RepoID: p.ID, Key: "m", Name: "m", Kind: "solo"})
	ag, _ := st.Agents().Create(ctx, storage.Agent{OrgModelID: m.ID, Key: "a", Name: "A", Tier: storage.TierLead, ModelTier: "fast"})
	if err := st.Agents().SetEnabled(ctx, ag.ID, false); err != nil {
		t.Fatal(err)
	}
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "h", Source: "webhook", Action: "chat", Enabled: true, AgentID: ag.ID})
	var notified string
	a.Config.NotifyChannelID, a.Config.NotifyChatID = "chn_1", "chat_1"
	_ = st.Automations().Update(ctx, a)
	r.SetOnNotify(func(_ context.Context, _, _, text, _ string) { notified = text })
	if _, _, err := r.Enqueue(ctx, a, "webhook", "{}", "1", ""); err != nil {
		t.Fatal(err)
	}
	r.Tick(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.chats) != 0 {
		t.Fatalf("AI ran for a paused agent: %v", ex.chats)
	}
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: a.ID})
	if len(jobs) != 1 || jobs[0].Status != "skipped" || jobs[0].ErrorCode != "agent_off" || jobs[0].Error != storage.OffNotice("A") {
		t.Fatalf("jobs = %+v", jobs)
	}
	if notified == "" {
		t.Fatal("the automation's chat got no notice")
	}
	if err := st.Agents().SetEnabled(ctx, ag.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Enqueue(ctx, a, "webhook", "{}", "2", ""); err != nil {
		t.Fatal(err)
	}
	r.Tick(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.chats) != 1 {
		t.Fatalf("resumed agent did not run: %v", ex.chats)
	}
}
