package channels_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// A message turned away while its chat already has MaxPending messages in
// flight is never dropped silently: the chat is told once per busy spell and
// each one leaves a skipped job; once the chat is free it is heard again.
func TestBusyChatTellsAndLogs(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	u := usage.New(st, time.UTC)
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), u)
	runner := trigger.New(st, chatExec{engine})
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	gate := filepath.Join(tmp, "gate")

	bot := &fakeBot{in: make(chan channels.Incoming, 16), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "telegram", Name: "Hỗ trợ", Enabled: true, Allow: []string{"42"}})
	rule, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Chậm", Source: "telegram", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID, Keywords: []string{"TIN"}},
		Script: storage.AutomationScript{Lang: "bash", Body: `while [ ! -f ` + gate + ` ]; do sleep 0.05; done; echo xong`, TimeoutS: 30}})
	m := channels.NewManager(st, engine, runner, func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runner.SetOnReply(m.Reply)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)

	send := func(text string) {
		bot.in <- channels.Incoming{ChatID: "42", UserID: "7", UserName: "an", Text: text, Private: true, Addressed: true}
	}
	ruleJobs := func() int {
		jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: rule.ID})
		return len(jobs)
	}
	busyJobs := func() int {
		jobs, _ := st.Jobs().List(ctx, storage.JobFilter{OriginID: ch.ID})
		n := 0
		for _, j := range jobs {
			if j.Status == "skipped" && j.ErrorCode == "busy" {
				n++
			}
		}
		return n
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
			if ok() {
				return
			}
		}
		t.Fatalf("timed out: %s (sent %v)", what, bot.sent["42"])
	}

	for i := 0; i < channels.MaxPending; i++ { // the chat's places fill up
		send("tin " + string(rune('a'+i)))
	}
	waitFor("the first messages queued", func() bool { return ruleJobs() == channels.MaxPending })
	send("tin thừa 1")
	send("tin thừa 2")
	waitFor("two skipped jobs", func() bool { return busyJobs() == 2 })
	time.Sleep(200 * time.Millisecond)
	bot.mu.Lock()
	got := append([]string(nil), bot.sent["42"]...)
	bot.mu.Unlock()
	if len(got) != 1 || got[0] != channels.BusyNotice {
		t.Fatalf("while busy the chat got %q, want the busy notice once", got)
	}

	os.WriteFile(gate, nil, 0o644) // the slow answers end: the chat is free
	bot.wait(t, "42", 1+channels.MaxPending)
	send("tin sau")
	waitFor("the next message heard", func() bool { return ruleJobs() == channels.MaxPending+1 })
	bot.wait(t, "42", 2+channels.MaxPending)

	os.Remove(gate) // a new busy spell is told again
	for i := 0; i < channels.MaxPending; i++ {
		send("tin lần hai " + string(rune('a'+i)))
	}
	waitFor("second spell queued", func() bool { return ruleJobs() == 2*channels.MaxPending+1 })
	send("tin thừa 3")
	waitFor("third skipped job", func() bool { return busyJobs() == 3 })
	got = bot.wait(t, "42", 3+channels.MaxPending)
	if last := got[len(got)-1]; !strings.HasPrefix(last, "⏳") {
		t.Fatalf("second busy spell not told: %q", got)
	}
	os.WriteFile(gate, nil, 0o644)
}
