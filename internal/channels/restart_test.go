package channels_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/channels"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/usage"
)

// A bot run office's restart cut off and did not put back (its automation
// stops on a restart, or it was cut too often in a row) tells the person who
// asked, once the bot is connected again, instead of leaving them waiting
// for an answer that never comes (ADR-258).
func TestBotTurnCutIsTold(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	runner := trigger.New(st, chatExec{engine})
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	bot := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "telegram", Name: "Hỗ trợ", Enabled: true, Allow: []string{"*"}})
	rule, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Script", Source: "telegram", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID, OnRestart: storage.OnRestartStop},
		Script: storage.AutomationScript{Lang: "bash", Body: "echo hi"}})
	resumes, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Agent", Source: "telegram", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID, OnRestart: "resume"},
		Script: storage.AutomationScript{Lang: "bash", Body: "echo hi"}})
	payload := func(chatID string) string {
		raw, _ := json.Marshal(trigger.ChannelPayload{Message: "hỏi", ChatID: chatID, ChannelID: ch.ID})
		return string(raw)
	}
	mk := func(a storage.Automation, chatID string) storage.Job {
		j, err := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "script", Origin: "automation", OriginID: a.ID,
			Trigger: "telegram", Payload: payload(chatID), Status: "running"})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	stopped := mk(rule, "41")
	again := mk(resumes, "42")
	over := mk(resumes, "43")
	cut := []storage.Job{stopped, again, over}
	if _, err := st.Jobs().FailRunning(ctx, "restart", "office khởi động lại", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// "over" was already put back MaxJobResumes times, cut each time
	for range storage.MaxJobResumes {
		j, err := st.Jobs().Requeue(ctx, over.ID, "restart", storage.MaxJobResumes, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		j.Status = "running"
		_ = st.Jobs().Update(ctx, j)
		_, _ = st.Jobs().FailRunning(ctx, "restart", "office khởi động lại", time.Now().UTC())
		over = j
	}
	cut[2] = over
	back := trigger.RequeueCut(ctx, st, cut, time.Now().UTC())
	if len(back) != 1 || back[0].ID != again.ID {
		t.Fatalf("back = %+v", back)
	}
	left := trigger.CutLeft(cut, back)
	m := channels.NewManager(st, engine, runner, func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	if n := m.TellCut(left); n != 2 {
		t.Fatalf("told %d, want 2", n)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx) // told once it is connected
	if got := bot.wait(t, "41", 1); got[0] != channels.CutNotice {
		t.Fatalf("41 got %q", got)
	}
	if got := bot.wait(t, "43", 1); got[0] != channels.CutNoticeOver {
		t.Fatalf("43 got %q", got)
	}
	time.Sleep(100 * time.Millisecond)
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.sent["42"]) != 0 || len(bot.sent["41"]) != 1 {
		t.Fatalf("a run put back is not told; each once: %v", bot.sent)
	}
}
