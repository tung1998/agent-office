package trigger_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// ADR-243: a turn that ends starts the automations following it, in the
// chat it ended in when asked; one of another agent, or another event, does not.
func TestEventTurnDoneRunsInItsChat(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	ag, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "dev", Name: "Dev", ModelTier: "fast"})
	rev, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "rev", Name: "Reviewer", ModelTier: "fast"})
	if _, err := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "Soát", Source: "event", Action: "chat", Enabled: true,
		AgentID: rev.ID, Prompt: "Soát lại: {{payload.text}}", Config: storage.AutomationConfig{Event: "turn.done", EventFrom: ag.ID, EventSameChat: true}}); err != nil {
		t.Fatal(err)
	}
	r.Emit(ctx, p.ID, "job_1", trigger.EventPayload{Event: "turn.done", From: ag.ID, ConversationID: "cnv_1", Status: "done", Text: "đã sửa"})
	r.Emit(ctx, p.ID, "job_1", trigger.EventPayload{Event: "turn.done", From: ag.ID, ConversationID: "cnv_1", Status: "done", Text: "đã sửa"}) // the same end again
	r.Emit(ctx, p.ID, "job_2", trigger.EventPayload{Event: "turn.done", From: rev.ID, ConversationID: "cnv_1", Status: "done"})
	r.Emit(ctx, p.ID, "job_3", trigger.EventPayload{Event: "turn.failed", From: ag.ID, ConversationID: "cnv_1", Status: "failed"})
	r.Wait()
	if len(ex.chats) != 1 || !strings.Contains(ex.chats[0], "Soát lại: đã sửa") || ex.convs[0] != "cnv_1" {
		t.Fatalf("runs %v in %v", ex.chats, ex.convs)
	}
}

// automation.done chains automations: B follows A, never itself, and the
// chain stops at its depth.
func TestEventChainsAutomations(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{}
	r := trigger.New(st, ex)
	ag, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "dev", Name: "Dev", ModelTier: "fast"})
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "A", Source: "schedule", Action: "chat", Enabled: true, AgentID: ag.ID, Prompt: "bước 1",
		Config: storage.AutomationConfig{EveryMinutes: 60}})
	if _, err := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "B", Source: "event", Action: "chat", Enabled: true, AgentID: ag.ID,
		Prompt: "bước 2 sau {{payload.from_name}}", Config: storage.AutomationConfig{Event: "automation.done"}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Enqueue(ctx, a, "manual", "", "", ""); err != nil {
		t.Fatal(err)
	}
	r.StartReady(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.chats) != 2 || !strings.HasPrefix(ex.chats[1], "bước 2 sau A") {
		t.Fatalf("chain ran %v", ex.chats)
	}
	// too deep: nothing more
	r.Emit(ctx, p.ID, "job_deep", trigger.EventPayload{Event: "automation.done", From: a.ID, Status: "done", Depth: 6})
	r.Wait()
	if len(ex.chats) != 2 {
		t.Fatalf("a chain past its depth ran: %v", ex.chats)
	}
}

func TestSpecEvent(t *testing.T) {
	s := trigger.Spec{Name: "x", Source: "event", Action: "chat", Event: "nope"}
	if s.Check() == nil {
		t.Fatal("an unknown event passed")
	}
	s.Event = "workflow.done"
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	var a storage.Automation
	s.EventSameChat = true // only a turn has a chat to go on in
	s.Apply(&a, time.Now().UTC())
	if a.Config.Event != "workflow.done" || a.Config.EventSameChat || a.NextRunAt != nil {
		t.Fatalf("applied %+v", a.Config)
	}
}
