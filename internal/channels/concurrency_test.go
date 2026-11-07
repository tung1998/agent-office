package channels_test

import (
	"context"
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
	"github.com/tung1998/agent-office/internal/usage"
)

// blockingRunner blocks Enqueue for one channel until released, so a test can
// tell whether another channel's message was stuck behind it.
type blockingRunner struct {
	blockChannel string
	entered      chan struct{}
	release      chan struct{}
	store        storage.Store
}

func (r *blockingRunner) Enqueue(ctx context.Context, a storage.Automation, trig, payload, dedupe, debounce string) (storage.Job, string, error) {
	if a.Config.ChannelID == r.blockChannel {
		close(r.entered)
		<-r.release
	}
	job, err := r.store.Jobs().Create(ctx, storage.Job{ProjectID: a.ProjectID, Kind: "chat_turn", Origin: "user", OriginID: a.ID, Status: "pending"})
	return job, "queued", err
}
func (r *blockingRunner) StartReady(context.Context, time.Time) {}

// A message to one bot channel must not wait for another channel's message
// still being enqueued: Manager.handle must not hold its one mutex across
// Runner.Enqueue (internal/channels/manager.go).
func TestHandleDoesNotBlockOtherChannelsDuringEnqueue(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	u := usage.New(st, time.UTC)
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), u)

	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	chA, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "telegram", Name: "A", Enabled: true, Allow: []string{"*"}})
	chB, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "telegram", Name: "B", Enabled: true, Allow: []string{"*"}})
	_, _ = st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "ruleA", Source: "telegram", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: chA.ID, Keywords: []string{"slow"}},
		Script: storage.AutomationScript{Lang: "bash", Body: "echo ok", TimeoutS: 30}})
	_, _ = st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "ruleB", Source: "telegram", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: chB.ID, Keywords: []string{"fast"}},
		Script: storage.AutomationScript{Lang: "bash", Body: "echo ok", TimeoutS: 30}})

	runner := &blockingRunner{blockChannel: chA.ID, entered: make(chan struct{}), release: make(chan struct{}), store: st}
	botA := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	botB := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	m := channels.NewManager(st, engine, runner, func(ch storage.Channel) (channels.Adapter, error) {
		if ch.ID == chA.ID {
			return botA, nil
		}
		return botB, nil
	})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)

	botA.in <- channels.Incoming{ChatID: "1", UserID: "u", UserName: "u", Text: "slow", Private: true, Addressed: true}

	select {
	case <-runner.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel A's message to reach Runner.Enqueue")
	}

	botB.in <- channels.Incoming{ChatID: "2", UserID: "u", UserName: "u", Text: "fast", Private: true, Addressed: true}

	start := time.Now()
	for time.Now().Before(start.Add(1 * time.Second)) {
		jobs, _ := st.Jobs().List(ctx, storage.JobFilter{Kind: "chat_turn"})
		found := false
		for _, j := range jobs {
			if j.Status == "pending" && j.OriginID != chA.ID {
				found = true
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	elapsed := time.Since(start)
	close(runner.release)

	if elapsed >= 500*time.Millisecond {
		t.Fatalf("channel B's message took %s to be enqueued while channel A's Enqueue was still blocked: handle() held a global lock across Runner.Enqueue", elapsed)
	}
}

// earlyFinishRunner simulates a job so fast that its Runner.Enqueue calls
// Manager.Reply(final=true) before handle() has had a chance to register the
// job's waiter (the race left open once Enqueue no longer runs under m.mu).
type earlyFinishRunner struct {
	store storage.Store
	mgr   *channels.Manager
}

func (r *earlyFinishRunner) Enqueue(ctx context.Context, a storage.Automation, trig, payload, dedupe, debounce string) (storage.Job, string, error) {
	job, err := r.store.Jobs().Create(ctx, storage.Job{ProjectID: a.ProjectID, Kind: "chat_turn", Origin: "user", OriginID: a.ID, Status: "pending", Payload: payload})
	if err != nil {
		return job, "", err
	}
	r.mgr.Reply(ctx, job, "xong", nil, true) // finishes and replies before Enqueue even returns
	return job, "queued", err
}
func (r *earlyFinishRunner) StartReady(context.Context, time.Time) {}

// When a job's Reply(final) races ahead of handle() registering its waiter,
// handle() must notice (Manager.early) and clean up itself: no leaked
// m.waiting entry, no stuck "pending" slot for the chat, no 👀 left forever.
func TestHandleClearsWaiterWhenReplyArrivesBeforeRegistration(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	u := usage.New(st, time.UTC)
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), u)

	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "telegram", Name: "A", Enabled: true, Allow: []string{"*"}})
	_, _ = st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "ruleA", Source: "telegram", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID, Keywords: []string{"go"}},
		Script: storage.AutomationScript{Lang: "bash", Body: "echo ok", TimeoutS: 30}})

	runner := &earlyFinishRunner{store: st}
	bot := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	m := channels.NewManager(st, engine, runner, func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runner.mgr = m
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)

	bot.in <- channels.Incoming{ChatID: "1", UserID: "u", UserName: "u", Text: "go", Private: true, Addressed: true}

	bot.wait(t, "1", 1) // "xong" sent by Reply once handle() cleaned up and let it through

	// each early-finished message must free its chat's pending slot (handle()
	// held = false bets everything on Reply(final) calling m.pending.Done
	// later, but Reply already ran before the waiter existed, so with no
	// cleanup every one of these leaks a slot until MaxPending is exhausted
	// and a later message is wrongly turned away as busy)
	for i := 0; i < channels.MaxPending; i++ {
		bot.in <- channels.Incoming{ChatID: "1", UserID: "u", UserName: "u", Text: "go", Private: true, Addressed: true}
	}
	got := bot.wait(t, "1", channels.MaxPending+1)
	for _, s := range got {
		if strings.Contains(s, channels.BusyNotice) {
			t.Fatalf("chat's pending slot leaked after an early reply: sent %v", got)
		}
	}
}
