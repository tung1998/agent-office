package trigger_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// The automations' runs office restarted under go on (ADR-257): an agent's
// in the chat it was cut in, told so, with the rights it was given (never
// more), its chain going on after it; a script only when its automation says
// so; a step of a run, or one put back too often, does not.
func TestCutRunsGoOn(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &fakeExec{answers: []string{"xong phần còn lại"}}
	r := trigger.New(st, ex)
	chat, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "kiểm tra", Source: "schedule", Action: "chat", Enabled: true,
		Prompt: "kiểm tra log", Config: storage.AutomationConfig{EveryMinutes: 60}})
	next, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "sau đó", Source: "event", Action: "script", Enabled: true,
		Script: storage.AutomationScript{Lang: "bash", Body: "echo tiếp"}, Config: storage.AutomationConfig{Event: "automation.done", EventFrom: chat.ID}})
	script, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "sao lưu", Source: "schedule", Action: "script", Enabled: true,
		Script: storage.AutomationScript{Lang: "bash", Body: "echo lại"}, Config: storage.AutomationConfig{EveryMinutes: 60}})
	again, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "đồng bộ", Source: "schedule", Action: "script", Enabled: true,
		Script: storage.AutomationScript{Lang: "bash", Body: "echo đồng bộ"}, Config: storage.AutomationConfig{EveryMinutes: 60, OnRestart: storage.OnRestartResume}})
	stop, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "dừng", Source: "schedule", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{EveryMinutes: 60, OnRestart: storage.OnRestartStop}})
	now := time.Now().UTC()
	run := func(a storage.Automation, j storage.Job) storage.Job {
		j.ProjectID, j.Origin, j.OriginID, j.Trigger, j.Status, j.StartedAt, j.Title = p.ID, "automation", a.ID, "schedule", "running", &now, a.Name
		if j.Kind == "" {
			j.Kind = "chat_turn"
		}
		j, err := st.Jobs().Create(ctx, j)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	cut := run(chat, storage.Job{ConversationID: "cnv_cut"})
	step := run(chat, storage.Job{ParentJobID: cut.ID, ConversationID: "cnv_judge"}) // its goal's judge: goes on with its run
	tooOften := run(chat, storage.Job{ConversationID: "cnv_loop", Resumes: storage.MaxJobResumes})
	plain := run(script, storage.Job{Kind: "script"})
	asked := run(again, storage.Job{Kind: "script"})
	stopped := run(stop, storage.Job{ConversationID: "cnv_stop"})
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{Origin: "automation", Status: "running"})
	if _, err := st.Jobs().FailRunning(ctx, "restart", "office khởi động lại", now); err != nil {
		t.Fatal(err)
	}

	back := trigger.RequeueCut(ctx, st, jobs, now)
	ids := map[string]bool{}
	for _, j := range back {
		ids[j.ID] = true
	}
	if len(back) != 2 || !ids[cut.ID] || !ids[asked.ID] {
		t.Fatalf("put back: %+v", back)
	}
	for _, j := range []storage.Job{step, tooOften, plain, stopped} {
		if cur, _ := st.Jobs().Get(ctx, j.ID); cur.Status != "failed" || cur.ErrorCode != "restart" {
			t.Errorf("%s (%s): %s %s", j.ID, j.Title, cur.Status, cur.ErrorCode)
		}
	}
	if again := trigger.RequeueCut(ctx, st, jobs, now); len(again) != 0 { // only a run ended by the restart
		t.Fatalf("put back twice: %+v", again)
	}

	r.StartReady(ctx, now)
	r.Wait()
	if len(ex.chats) != 1 || ex.convs[0] != "cnv_cut" || !strings.Contains(ex.chats[0], "Office restarted") || strings.Contains(ex.chats[0], "kiểm tra log") {
		t.Fatalf("runs: convs=%v chats=%q", ex.convs, ex.chats)
	}
	if j, _ := st.Jobs().Get(ctx, cut.ID); j.Status != "done" || j.Resumes != 1 {
		t.Fatalf("resumed run: %s resumes=%d", j.Status, j.Resumes)
	}
	if j, _ := st.Jobs().Get(ctx, asked.ID); j.Status != "done" || !strings.Contains(j.Output, "đồng bộ") {
		t.Fatalf("script asked to go on: %s %q", j.Status, j.Output)
	}
	chained, _ := st.Jobs().List(ctx, storage.JobFilter{Origin: "automation", OriginID: next.ID})
	if len(chained) != 1 || chained[0].Trigger != "event" {
		t.Fatalf("its chain: %+v", chained)
	}
}

// A run put back after a restart keeps the rights it was made with: a
// webhook's never gets full access on its second go.
func TestCutRunKeepsItsRights(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "hook", Source: "webhook", Action: "chat", Enabled: true})
	now := time.Now().UTC()
	j, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: a.ID, Trigger: "webhook",
		Status: "running", StartedAt: &now, ConversationID: "cnv_hook", ExtraDirs: []string{"/x"}})
	_, _ = st.Jobs().FailRunning(ctx, "restart", "x", now)
	back := trigger.RequeueCut(ctx, st, []storage.Job{j}, now)
	if len(back) != 1 || back[0].FullAccess || back[0].Trigger != "webhook" || len(back[0].ExtraDirs) != 1 || back[0].Status != "pending" {
		t.Fatalf("put back: %+v", back)
	}
}

// What a run cut off does, by its automation.
func TestResumesOnRestart(t *testing.T) {
	for _, c := range []struct {
		action, on string
		want       bool
	}{{"chat", "", true}, {"script", "", false}, {"workflow", "", false}, {"script", "resume", true}, {"chat", "stop", false}} {
		a := storage.Automation{Action: c.action, Config: storage.AutomationConfig{OnRestart: c.on}}
		if got := a.ResumesOnRestart(); got != c.want {
			t.Errorf("%s/%q: %v", c.action, c.on, got)
		}
	}
	if storage.CleanOnRestart(" resume ") != "resume" || storage.CleanOnRestart("maybe") != "" {
		t.Error("clean")
	}
}
