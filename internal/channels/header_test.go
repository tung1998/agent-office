package channels_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/attach"
	"github.com/tung1998/agent-office/internal/channels"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/usage"
)

// The line on top of a bot's answer: its template, the parts it has, off.
func TestHeaderLine(t *testing.T) {
	for _, c := range []struct{ tpl, kind, agent, project, branch, want string }{
		{"", "discord", "Trợ lý", "shop", "main", "-# Trợ lý · shop · main"},
		{"", "telegram", "Trợ lý", "shop", "main", "Trợ lý · shop · main"},
		{"", "discord", "", "shop", "", "-# shop"}, // a script's answer: no agent; not a git folder
		{"{project} ({branch})", "discord", "A", "shop", "dev", "-# shop (dev)"},
		{"-", "discord", "A", "shop", "dev", ""},
	} {
		if got := channels.HeaderLine(c.tpl, c.kind, c.agent, c.project, c.branch); got != c.want {
			t.Errorf("%q %s: %q, want %q", c.tpl, c.kind, got, c.want)
		}
	}
}

// A bot's answer starts with who answered, in which project and branch; a
// follow-up of another agent names that agent instead of signing the text.
func TestReplyHeader(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	engine := chat.NewEngine(st, provs, usage.New(st, time.UTC))
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "-b", "feature-x", dir).CombinedOutput(); err != nil {
		t.Skip("git:", string(out))
	}
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: dir})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	agents, _ := engine.Agents(ctx, project.ID)
	st.Agents().Create(ctx, storage.Agent{OrgModelID: agents[0].OrgModelID, Key: "tester", Name: "Tester", Tier: "worker", ReportsTo: []string{agents[0].Key}, ModelTier: "fast"})
	bot := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if c, _ := st.Channels().Get(ctx, ch.ID); c.BotName != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	conv, _ := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "42", ConversationID: conv.ID})
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Đã xem xong", nil, false)
	got := bot.wait(t, "42", 1)
	if got[0] != "-# "+conv.AgentName+" · shop · feature-x\nĐã xem xong" {
		t.Fatalf("answer = %q", got[0])
	}
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Tester:\nTest đạt", nil, false)
	got = bot.wait(t, "42", 2)
	if got[1] != "-# Tester · shop · feature-x\nTest đạt" {
		t.Fatalf("follow-up = %q", got[1])
	}
	ch.Header = "-"
	st.Channels().Update(ctx, ch)
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Xong rồi", nil, false)
	if got = bot.wait(t, "42", 3); got[2] != "Xong rồi" || strings.Contains(got[2], "-#") {
		t.Fatalf("off = %q", got[2])
	}
}

// slowBot takes its time to connect, as Discord does.
type slowBot struct {
	fakeBot
	ready chan struct{}
}

func (b *slowBot) Run(ctx context.Context, onReady func(string), onMessage func(channels.Incoming)) error {
	select {
	case <-b.ready:
	case <-ctx.Done():
		return nil
	}
	return b.fakeBot.Run(ctx, onReady, onMessage)
}

// A bot being connected says so (the dashboard follows it), then runs.
func TestBotState(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true})
	off, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Off"})
	bot := &slowBot{fakeBot: fakeBot{in: make(chan channels.Incoming), sent: map[string][]string{}}, ready: make(chan struct{})}
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(2 * time.Second); m.State(ch.ID) != "connecting"; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("state = %q, want connecting", m.State(ch.ID))
		}
	}
	close(bot.ready)
	for deadline := time.Now().Add(2 * time.Second); m.State(ch.ID) != "running"; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("state = %q, want running", m.State(ch.ID))
		}
	}
	if s := m.State(off.ID); s != "" {
		t.Fatalf("an off bot: %q", s)
	}
}

// A thread made from a message the bot answered goes on with that message's
// conversation, and the conversation links to the thread on Discord.
func TestThreadFromAnswer(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	bot := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	conv, _ := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "c2", ConversationID: conv.ID})
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Đây là kết quả", nil, false)
	bot.wait(t, "c2", 1) // the answer is message "1" of c2
	bot.in <- channels.Incoming{ChatID: "1", GuildID: "g", ThreadOf: "1"}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if id, _ := st.Channels().Thread(ctx, ch.ID, "in:1"); id == conv.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the thread is not the conversation's")
		}
	}
	if got := channels.ConversationLink(ctx, st, conv.ID); got != "https://discord.com/channels/g/1" {
		t.Fatalf("link = %q", got)
	}
	// an answer remembered before threads were (only its reply key): found through the thread's channel
	old, _ := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	st.Channels().SetThread(ctx, ch.ID, "msg:c2:77", old.ID)
	bot.in <- channels.Incoming{ChatID: "77", GuildID: "g", ThreadOf: "77", ParentID: "c2"}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if id, _ := st.Channels().Thread(ctx, ch.ID, "in:77"); id == old.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an old answer's thread is not its conversation's")
		}
	}
}

// threadBot makes threads as Discord does: from a message, the thread's id is the message's.
type threadBot struct {
	fakeBot
	made chan [3]string // chat, from message, name
}

func (b *threadBot) MakeThread(_ context.Context, chatID, fromMsg, name string) (string, error) {
	b.made <- [3]string{chatID, fromMsg, name}
	if fromMsg != "" {
		return fromMsg, nil
	}
	return "t-new", nil
}

// /create-thread (a reply to the bot's answer) makes a thread from that
// answer: its conversation goes on there. Asked for only: nothing else makes one.
func TestCreateThreadCommand(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	bot := &threadBot{fakeBot: fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}, made: make(chan [3]string, 2)}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	conv, err := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	if err != nil || conv.ID == "" {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "c2", ConversationID: conv.ID})
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Kế hoạch migrate", nil, false)
	bot.wait(t, "c2", 1)                                                                   // message "1"
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) { // remembered once sent
		if id, _ := st.Channels().Thread(ctx, ch.ID, "thread:1"); id == conv.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the answer is not remembered")
		}
	}
	bot.in <- channels.Incoming{ChatID: "c2", GuildID: "g", MessageID: "u9", UserID: "8", Text: "/create-thread Migrate admin", ReplyTo: "1", Addressed: true}
	select {
	case got := <-bot.made:
		if got != [3]string{"c2", "1", "Migrate admin"} {
			t.Fatalf("made = %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no thread made")
	}
	if got := bot.wait(t, "1", 1); !strings.Contains(got[0], "thread") {
		t.Fatalf("in the thread = %q", got)
	}
	if id, _ := st.Channels().Thread(ctx, ch.ID, "in:1"); id != conv.ID {
		t.Fatalf("the thread's conversation = %q", id)
	}
	if got := channels.ConversationLink(ctx, st, conv.ID); got != "https://discord.com/channels/g/1" {
		t.Fatalf("link = %q", got)
	}
	var keep string // /create-thread starts as a conversation: no tag needed there
	if ok, _ := st.Settings().Get(ctx, "channel_keep/"+ch.ID+"/1", &keep); !ok || keep == "" {
		t.Fatal("the new thread is not a kept conversation")
	}
}

// /create-thread from the "/" menu (Discord sends no message it replies to):
// the thread grows from the bot's latest answer here, with its conversation.
func TestCreateThreadSlash(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	bot := &threadBot{fakeBot: fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}, made: make(chan [3]string, 2)}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	conv, err := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "c2", ConversationID: conv.ID})
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Câu trả lời", nil, false)
	bot.wait(t, "c2", 1) // message "1"
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if id, _ := st.Channels().Thread(ctx, ch.ID, "thread:1"); id == conv.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the answer is not remembered")
		}
	}
	responded := make(chan string, 1)
	bot.in <- channels.Incoming{ChatID: "c2", GuildID: "g", UserID: "8", Text: "/create-thread", Addressed: true,
		Respond: func(_ context.Context, text string) (string, error) { responded <- text; return "r1", nil }}
	select {
	case got := <-bot.made:
		if got[0] != "c2" || got[1] != "1" {
			t.Fatalf("made = %v, want from the latest answer", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no thread made")
	}
	<-responded
	if id, _ := st.Channels().Thread(ctx, ch.ID, "in:1"); id != conv.ID {
		t.Fatalf("the thread's conversation = %q", id)
	}

	// again, with no newer answer: that one has its thread, so a new thread on
	// its own (a new conversation), not back into the first
	bot.in <- channels.Incoming{ChatID: "c2", GuildID: "g", UserID: "8", Text: "/create-thread Việc khác", Addressed: true,
		Respond: func(_ context.Context, text string) (string, error) { responded <- text; return "r2", nil }}
	select {
	case got := <-bot.made:
		if got[0] != "c2" || got[1] != "" || got[2] != "Việc khác" {
			t.Fatalf("made = %v, want a new thread on its own", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no second thread made")
	}
	if got := <-responded; !strings.Contains(got, "t-new") {
		t.Fatalf("responded %q", got)
	}
	if id, _ := st.Channels().Thread(ctx, ch.ID, "in:t-new"); id != "" {
		t.Fatalf("the new thread went on with %q", id)
	}
}

// In a thread of a conversation the bot still wants a tag, until
// /create-conversation there (that thread only), which keeps its conversation.
func TestThreadNeedsTag(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	bot := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	conv, _ := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	st.Channels().SetThread(ctx, ch.ID, "in:t1", conv.ID)                        // a thread of the conversation
	bot.in <- channels.Incoming{ChatID: "t1", UserID: "8", Text: "còn đó không"} // untagged: not for the bot
	bot.in <- channels.Incoming{ChatID: "t1", UserID: "8", Text: "/create-conversation", Addressed: true}
	got := bot.wait(t, "t1", 1)
	if len(got) != 1 || !strings.Contains(got[0], "hội thoại") {
		t.Fatalf("thread = %q (the untagged message was answered?)", got)
	}
	if id, _ := st.Channels().Thread(ctx, ch.ID, "in:t1"); id != conv.ID {
		t.Fatal("/create-conversation took the thread off its conversation")
	}
}

// Every message in a thread is one conversation (the thread's), tagged or
// not kept: the first tag makes it, the next ones go on in it.
func TestThreadIsOneConversation(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), nil)
	a, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "t9", InThread: true, Text: "một", Addressed: true})
	b, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "t9", InThread: true, Text: "hai", Addressed: true})
	c, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "c2", Text: "ba", Addressed: true})
	if a == "" || a != b {
		t.Fatalf("two tags in one thread: %q %q", a, b)
	}
	if c == a {
		t.Fatal("a channel message went into the thread's conversation")
	}
}

// A kept thread (/create-conversation there, or /create-thread) keeps its
// conversation: being a thread does not start another one.
func TestKeptThreadKeepsItsConversation(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), nil)
	st.Settings().Set(ctx, "channel_keep/"+ch.ID+"/t5", "gen1")
	kept, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "t5", Addressed: true}) // before the bot knew it was a thread
	again, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "t5", InThread: true, Addressed: true})
	if kept == "" || again != kept {
		t.Fatalf("kept %q, then %q", kept, again)
	}
}

// /create-conversation goes on with the latest answer's conversation (tagging,
// then keeping: nothing lost); after /close-conversation it starts afresh.
func TestCreateConversationContinues(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	bot := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	tagged, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "c2", Addressed: true})
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "c2", ConversationID: tagged})
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Câu trả lời", nil, false)
	bot.wait(t, "c2", 1)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var last string
		if ok, _ := st.Settings().Get(ctx, "channel_last/"+ch.ID+"/c2", &last); ok && last != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the answer is not remembered")
		}
	}
	bot.in <- channels.Incoming{ChatID: "c2", UserID: "8", Text: "/create-conversation", Addressed: true}
	got := bot.wait(t, "c2", 2)
	if !strings.Contains(got[1], "tiếp tục") {
		t.Fatalf("create = %q", got[1])
	}
	if kept, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "c2"}); kept != tagged {
		t.Fatalf("kept %q, want the tagged answer's %q", kept, tagged)
	}
	bot.in <- channels.Incoming{ChatID: "c2", UserID: "8", Text: "/close-conversation", Addressed: true}
	bot.wait(t, "c2", 3)
	bot.in <- channels.Incoming{ChatID: "c2", UserID: "8", Text: "/create-conversation", Addressed: true}
	bot.wait(t, "c2", 4)
	if fresh, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "c2"}); fresh == tagged || fresh == "" {
		t.Fatalf("after close, a fresh one: %q", fresh)
	}
}

// A photo sent to the bot reaches the agent as an attachment of the message.
func TestFilesToTheAgent(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	bin := filepath.Join(tmp, "claude")
	os.WriteFile(bin, []byte(`#!/bin/sh
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"đã xem ảnh","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	provs.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	engine := chat.NewEngine(st, provs, u)
	engine.SetAttachments(attach.Store{Dir: filepath.Join(tmp, "att")})
	runner := trigger.New(st, chatExec{engine})
	bot := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Header: "-"})
	st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Trả lời", Source: "discord", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID}})
	m := channels.NewManager(st, engine, runner, func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runner.SetOnReply(m.Reply)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	bot.in <- channels.Incoming{ChatID: "c2", UserID: "8", Addressed: true, MessageID: "u1",
		Files: []channels.InFile{{Name: "lỗi.png", Size: int64(len(png)), Fetch: func(context.Context) ([]byte, error) { return png, nil }}}}
	if got := bot.wait(t, "c2", 1); got[0] != "đã xem ảnh" {
		t.Fatalf("answer = %q", got)
	}
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{ProjectID: project.ID})
	var p trigger.ChannelPayload
	for _, j := range jobs {
		if j.Trigger == "discord" {
			json.Unmarshal([]byte(j.Payload), &p)
		}
	}
	if len(p.Attachments) != 1 {
		t.Fatalf("payload = %+v", p)
	}
	msgs, _ := engine.History(ctx, p.ConversationID)
	if len(msgs) == 0 || len(msgs[0].Attachments) != 1 || msgs[0].Attachments[0].Name != "lỗi.png" {
		t.Fatalf("the agent's message = %+v", msgs)
	}
}

// buttonBot puts buttons under messages.
type buttonBot struct {
	fakeBot
	rows chan [][]channels.Button
}

func (b *buttonBot) SendButtons(ctx context.Context, chatID, text string, rows [][]channels.Button) ([]string, error) {
	b.rows <- rows
	return b.Send(ctx, chatID, text)
}

// buttonEdit is a message with buttons changed.
type buttonEdit struct {
	msg, text string
	rows      [][]channels.Button
}

// editBot also changes the buttons under its messages.
type editBot struct {
	buttonBot
	edits chan buttonEdit
}

func (b *editBot) EditButtons(_ context.Context, chatID, msgID, text string, rows [][]channels.Button) error {
	b.edits <- buttonEdit{msgID, text, rows}
	return nil
}

// actionDecider decides actions for real (their status), as office does.
type actionDecider struct{ actions storage.ActionRepo }

func (d actionDecider) Decide(ctx context.Context, kind, id string, approve bool, by string) (string, error) {
	a, err := d.actions.Get(ctx, id)
	if err != nil {
		return "", err
	}
	a.Status = "rejected"
	if approve {
		a.Status = "done"
	}
	return "Thành công", d.actions.Update(ctx, a)
}

// A button pressed takes the decided proposal's buttons off its message;
// once nothing waits, no button is left on it.
func TestPressedButtonsGoAway(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	bot := &editBot{buttonBot: buttonBot{fakeBot: fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}, rows: make(chan [][]channels.Button, 2)}, edits: make(chan buttonEdit, 4)}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Approvers: []string{"7"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	m.SetDecider(actionDecider{st.Actions()})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	conv, _ := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	lint, _ := st.Actions().Create(ctx, storage.Action{ProjectID: project.ID, ConversationID: conv.ID, Kind: "run_command", Target: "pnpm lint", Status: "pending"})
	test, _ := st.Actions().Create(ctx, storage.Action{ProjectID: project.ID, ConversationID: conv.ID, Kind: "run_command", Target: "pnpm test", Status: "pending"})
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "42", ConversationID: conv.ID})
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Xong", nil, true)
	select {
	case <-bot.rows:
	case <-time.After(3 * time.Second):
		t.Fatal("no buttons")
	}
	edited := func() buttonEdit {
		select {
		case e := <-bot.edits:
			return e
		case <-time.After(3 * time.Second):
			t.Fatal("the message was not changed")
		}
		return buttonEdit{}
	}
	// one decided: only the other's buttons stay (no "all" for one)
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", UserName: "an", Text: "/approve " + lint.ID, Addressed: true, ButtonMsg: "m1"}
	if e := edited(); e.msg != "m1" || len(e.rows) != 1 || e.rows[0][0].Data != "/approve "+test.ID || strings.Contains(e.text, "pnpm lint") {
		t.Fatalf("after one = %+v", e)
	}
	// the last skipped: rejected, no buttons, what was decided instead
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", UserName: "an", Text: "/skip " + test.ID, Addressed: true, ButtonMsg: "m1"}
	if e := edited(); e.msg != "m1" || len(e.rows) != 0 || !strings.Contains(e.text, "Đã bỏ qua") {
		t.Fatalf("after all = %+v", e)
	}
	if a, _ := st.Actions().Get(ctx, test.ID); a.Status != "rejected" {
		t.Fatalf("skipped = %s", a.Status)
	}
	// typed, with no button: no message to change
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", UserName: "an", Text: "/approve all", Addressed: true}
	select {
	case e := <-bot.edits:
		t.Fatalf("typed command changed %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}

// Decided on the dashboard (Bỏ qua, Bỏ qua tất cả): the bot's message with
// buttons keeps only those of what still waits, then none; no agent runs.
func TestDashboardDecisionRedrawsButtons(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	bot := &editBot{buttonBot: buttonBot{fakeBot: fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}, rows: make(chan [][]channels.Button, 2)}, edits: make(chan buttonEdit, 4)}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Approvers: []string{"7"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	m.SetDecider(actionDecider{st.Actions()})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	conv, _ := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	lint, _ := st.Actions().Create(ctx, storage.Action{ProjectID: project.ID, ConversationID: conv.ID, Kind: "run_command", Target: "pnpm lint", Status: "pending"})
	test, _ := st.Actions().Create(ctx, storage.Action{ProjectID: project.ID, ConversationID: conv.ID, Kind: "run_command", Target: "pnpm test", Status: "pending"})
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "42", ConversationID: conv.ID})
	st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "chat_turn", Origin: "automation", Trigger: "discord", ConversationID: conv.ID, Payload: string(payload), Status: "done"})
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Xong", nil, true)
	select {
	case <-bot.rows:
	case <-time.After(3 * time.Second):
		t.Fatal("no buttons")
	}
	bot.mu.Lock()
	msg := fmt.Sprint(len(bot.sent["42"])) // the message with buttons: the latest sent
	bot.mu.Unlock()
	edited := func() buttonEdit {
		select {
		case e := <-bot.edits:
			return e
		case <-time.After(3 * time.Second):
			t.Fatal("the message was not changed")
		}
		return buttonEdit{}
	}
	lint.Status = "rejected"
	st.Actions().Update(ctx, lint)
	m.Redraw(ctx, conv.ID)
	if e := edited(); e.msg != msg || len(e.rows) != 1 || e.rows[0][0].Data != "/approve "+test.ID {
		t.Fatalf("after one = %+v", e)
	}
	test.Status = "rejected"
	st.Actions().Update(ctx, test)
	m.Redraw(ctx, conv.ID)
	if e := edited(); e.msg != msg || len(e.rows) != 0 || !strings.Contains(e.text, "dashboard") {
		t.Fatalf("after all = %+v", e)
	}
	// nothing left to redraw
	m.Redraw(ctx, conv.ID)
	select {
	case e := <-bot.edits:
		t.Fatalf("redrawn again %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
}

// alwaysDecider also approves "luôn cho phép", recording which.
type alwaysDecider struct {
	actionDecider
	mu     sync.Mutex
	always []string
}

func (d *alwaysDecider) DecideAlways(ctx context.Context, id, by string) (string, error) {
	d.mu.Lock()
	d.always = append(d.always, id)
	d.mu.Unlock()
	return d.Decide(ctx, "action", id, true, by)
}

// A command that may be allowed for good has a "♾️ Luôn cho phép" button;
// a risky one has none, and its command is refused.
func TestAlwaysButton(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	bot := &editBot{buttonBot: buttonBot{fakeBot: fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}, rows: make(chan [][]channels.Button, 2)}, edits: make(chan buttonEdit, 4)}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Approvers: []string{"7"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	dec := &alwaysDecider{actionDecider: actionDecider{st.Actions()}}
	m.SetDecider(dec)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	conv, _ := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	sw, _ := st.Actions().Create(ctx, storage.Action{ProjectID: project.ID, ConversationID: conv.ID, Kind: "run_command", Target: "git switch main", Status: "pending"})
	rm, _ := st.Actions().Create(ctx, storage.Action{ProjectID: project.ID, ConversationID: conv.ID, Kind: "run_command", Target: "rm -rf dist", Status: "pending"})
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "42", ConversationID: conv.ID})
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Xong", nil, true)
	var rows [][]channels.Button
	select {
	case rows = <-bot.rows:
	case <-time.After(3 * time.Second):
		t.Fatal("no buttons")
	}
	if len(rows) < 2 || len(rows[0]) != 4 || rows[0][1].Data != "/approve-always "+sw.ID || !strings.Contains(rows[0][1].Label, "Luôn cho phép") || rows[0][3].Data != "/skip "+sw.ID || len(rows[1]) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	edited := func() buttonEdit {
		select {
		case e := <-bot.edits:
			return e
		case <-time.After(3 * time.Second):
			t.Fatal("the message was not changed")
		}
		return buttonEdit{}
	}
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", UserName: "an", Text: "/approve-always " + sw.ID, Addressed: true, ButtonMsg: "m1"}
	if e := edited(); len(e.rows) != 1 || e.rows[0][0].Data != "/approve "+rm.ID || strings.Contains(e.text, "git switch") {
		t.Fatalf("after always = %+v", e)
	}
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", UserName: "an", Text: "/approve-always " + rm.ID, Addressed: true, ButtonMsg: "m1"}
	edited()
	dec.mu.Lock()
	got := append([]string(nil), dec.always...)
	dec.mu.Unlock()
	if a, _ := st.Actions().Get(ctx, rm.ID); a.Status != "pending" || len(got) != 1 || got[0] != sw.ID {
		t.Fatalf("risky = %s, always = %v", a.Status, got)
	}
}

// What waits for approval comes with Approve / Reject buttons (a press is the command).
func TestPendingButtons(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	bot := &buttonBot{fakeBot: fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}, rows: make(chan [][]channels.Button, 2)}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Approvers: []string{"7"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	m.SetDecider(&fakeDecider{})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	conv, _ := engine.StartConversationPurpose(ctx, project.ID, "", "channel")
	lint, _ := st.Actions().Create(ctx, storage.Action{ProjectID: project.ID, ConversationID: conv.ID, Kind: "run_command", Target: "pnpm lint", Status: "pending"})
	st.Actions().Create(ctx, storage.Action{ProjectID: project.ID, ConversationID: conv.ID, Kind: "run_command", Target: "pnpm test", Status: "pending"})
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "42", ConversationID: conv.ID})
	m.Reply(ctx, storage.Job{ID: "job_1", Payload: string(payload)}, "Xong", nil, true)
	select {
	case rows := <-bot.rows:
		// a button names the proposal, not its number: numbers start again once all is decided
		if len(rows) != 3 || rows[0][0].Data != "/approve "+lint.ID || rows[0][1].Data != "/reject "+lint.ID || !rows[0][1].Danger || rows[2][0].Data != "/approve all" || !strings.Contains(rows[0][0].Label, "1") {
			t.Fatalf("rows = %+v", rows)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no buttons")
	}
	bot.mu.Lock()
	before := len(bot.sent["42"])
	bot.mu.Unlock()
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", UserName: "an", Text: "/approve " + lint.ID, Addressed: true}
	if got := bot.wait(t, "42", before+1); !strings.Contains(got[len(got)-1], "pnpm lint") || strings.Contains(got[len(got)-1], "pnpm test") {
		t.Fatalf("approve by id = %q", got)
	}
}

// liveBot reacts, edits and deletes, as Discord and Telegram do.
type liveBot struct {
	fakeBot
	mu2    sync.Mutex
	events []string
}

func (b *liveBot) note(s string) { b.mu2.Lock(); b.events = append(b.events, s); b.mu2.Unlock() }
func (b *liveBot) React(_ context.Context, chatID, msgID, emoji string, on bool) error {
	b.note(fmt.Sprintf("react %s %s %v", msgID, emoji, on))
	return nil
}
func (b *liveBot) Edit(_ context.Context, chatID, msgID, text string) error {
	b.note("edit " + msgID + " " + text)
	return nil
}
func (b *liveBot) Delete(_ context.Context, chatID, msgID string) error {
	b.note("delete " + msgID)
	return nil
}
func (b *liveBot) seen() string {
	b.mu2.Lock()
	defer b.mu2.Unlock()
	return strings.Join(b.events, "\n")
}

// A message the bot takes gets 👀; a long run shows its steps in one status
// message, edited as it goes and gone with the answer.
func TestProgress(t *testing.T) {
	bot := runProgress(t, "", "")
	seen := bot.seen()
	for _, want := range []string{"edit ", "Chạy test", "delete ", "react u1 👀 false"} {
		if !strings.Contains(seen, want) {
			t.Errorf("no %q in\n%s\nsent %v", want, seen, bot.sent["c2"])
		}
	}
	if !strings.Contains(bot.sent["c2"][0], "Đọc a.go") {
		t.Errorf("the status message = %q", bot.sent["c2"][0])
	}
	// the bot shows steps, this rule only its answer: as above
	if seen := runProgress(t, storage.ReplySteps, storage.ReplyAnswer).seen(); !strings.Contains(seen, "delete ") {
		t.Errorf("an answer-only rule kept its steps:\n%s", seen)
	}
}

// Reply mode steps (the bot's, or a rule's over it): the steps stay in the
// chat, all of them, then the answer.
func TestProgressSteps(t *testing.T) {
	for _, mode := range [][2]string{{storage.ReplySteps, ""}, {"", storage.ReplySteps}} {
		bot := runProgress(t, mode[0], mode[1])
		seen := bot.seen()
		if strings.Contains(seen, "delete ") {
			t.Errorf("%v: the steps were deleted:\n%s", mode, seen)
		}
		if !strings.Contains(seen, "edit 1 ✅ Các bước:\n• Đọc a.go\n• Chạy test") {
			t.Errorf("%v: no last edit with every step:\n%s", mode, seen)
		}
		if got := bot.sent["c2"]; len(got) != 2 || got[1] != "xong rồi" {
			t.Errorf("%v: sent = %q", mode, got)
		}
	}
}

// runProgress runs one bot message through two steps to its answer.
func runProgress(t *testing.T, channelMode, ruleMode string) *liveBot {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	bin := filepath.Join(tmp, "claude")
	os.WriteFile(bin, []byte(`#!/bin/sh
cat >/dev/null
sleep 1
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"xong rồi","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	provs.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	engine := chat.NewEngine(st, provs, u)
	runner := trigger.New(st, chatExec{engine})
	bot := &liveBot{fakeBot: fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Header: "-", ReplyMode: channelMode})
	st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Trả lời", Source: "discord", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID, ReplyMode: ruleMode}})
	m := channels.NewManager(st, engine, runner, func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	m.ProgressAfter = 0
	runner.SetOnReply(m.Reply)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	bot.in <- channels.Incoming{ChatID: "c2", UserID: "8", Addressed: true, MessageID: "u1", Text: "làm giúp"}
	for deadline := time.Now().Add(3 * time.Second); !strings.Contains(bot.seen(), "react u1 👀 true"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("no 👀: %s", bot.seen())
		}
	}
	var job storage.Job
	for deadline := time.Now().Add(3 * time.Second); job.ID == ""; time.Sleep(10 * time.Millisecond) {
		jobs, _ := st.Jobs().List(ctx, storage.JobFilter{ProjectID: project.ID})
		for _, j := range jobs {
			if j.Trigger == "discord" {
				job = j
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no job")
		}
	}
	m.Progress(ctx, job, "Đọc a.go")
	m.Progress(ctx, job, "Chạy test")
	if got := bot.wait(t, "c2", 2); got[len(got)-1] != "xong rồi" {
		t.Fatalf("sent = %q", got)
	}
	time.Sleep(100 * time.Millisecond)
	return bot
}

// A running bot posts an alert to a chat; an off one says so.
func TestNotify(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	bot := &fakeBot{in: make(chan channels.Incoming, 1), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true})
	off, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Off"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	if err := m.Notify(ctx, ch.ID, "c9", "⚠️ sắp hết"); err != nil {
		t.Fatal(err)
	}
	if got := bot.wait(t, "c9", 1); got[0] != "⚠️ sắp hết" {
		t.Fatalf("sent = %q", got)
	}
	if err := m.Notify(ctx, off.ID, "c9", "x"); err == nil {
		t.Fatal("an off bot sent")
	}
	// an automation's notice: a reply to it goes on in the run's chat, whatever the bot's agent
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	agents, _ := engine.Agents(ctx, project.ID)
	other, err := st.Agents().Create(ctx, storage.Agent{OrgModelID: agents[0].OrgModelID, Name: "Khác", Tier: agents[0].Tier, ModelTier: agents[0].ModelTier, Instructions: "x"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: project.ID, AgentID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.NotifyConversation(ctx, ch.ID, "c9", "**Migrate**\nxong 3 route", run.ID); err != nil {
		t.Fatal(err)
	}
	bot.wait(t, "c9", 2)
	if got, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "c9", ReplyTo: "2", Text: "rebase master", Addressed: true}); got != run.ID {
		t.Fatalf("reply to the notice went to %q, want the run's %q", got, run.ID)
	}
	if got, _ := m.ConversationFor(ctx, ch, channels.Incoming{ChatID: "c9", ReplyTo: "1", Text: "x", Addressed: true}); got == run.ID {
		t.Fatal("a reply to a plain alert went to the run's chat")
	}
}

// An agent with FullAccess (ADR-074) approves a proposal at once, wherever
// its job ran (here a plain dashboard chat turn, not a bot's chat); a push
// still asks, so does creating/running an automation, and losing admin ends it.
func TestFullAccessApproverAgent(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	agents, _ := engine.Agents(ctx, project.ID)
	agent := agents[0]

	st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	full, _ := st.Agents().Get(ctx, agent.ID)
	full.Permissions.FullAccess, full.Permissions.FullAccessBy = true, "admin@x.io"
	st.Agents().Update(ctx, full)

	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), nil)
	// job.FullAccess is computed once, when a job is created (ADR-074 security
	// fix) — tests that create a job directly must set it themselves, as
	// internal/chat.Engine or internal/trigger.Runner would have.
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "chat_turn", Origin: "user", Trigger: "ui", AgentID: agent.ID, Status: "running",
		FullAccess: true, FullAccessBy: "admin@x.io"})

	if by, ok := m.DirectApprover(ctx, storage.Action{Kind: "run_command", Target: "ls", JobID: job.ID}); !ok || by != "admin@x.io" {
		t.Fatalf("full access agent = %q %v", by, ok)
	}
	if _, ok := m.DirectApprover(ctx, storage.Action{Kind: "git_push", Target: "origin fix", JobID: job.ID}); ok {
		t.Fatal("a push was approved under full access")
	}
	if _, ok := m.DirectApprover(ctx, storage.Action{Kind: "create_automation", Target: "x", JobID: job.ID}); ok {
		t.Fatal("create_automation was approved under full access")
	}
	if _, ok := m.DirectApprover(ctx, storage.Action{Kind: "run_automation", Target: "x", JobID: job.ID}); ok {
		t.Fatal("run_automation was approved under full access")
	}

	// a second agent, FullAccess enabled by someone no longer an admin
	st.Users().Create(ctx, storage.User{Email: "member@x.io", Role: storage.RoleMember, PasswordHash: "h"})
	st.Agents().Create(ctx, storage.Agent{OrgModelID: agent.OrgModelID, Key: "dev", Name: "Dev", Tier: storage.TierWorker, ModelTier: "fast"})
	devs, _ := engine.Agents(ctx, project.ID)
	var dev storage.Agent
	for _, a := range devs {
		if a.Key == "dev" {
			dev, _ = st.Agents().Get(ctx, a.ID)
		}
	}
	dev.Permissions.FullAccess, dev.Permissions.FullAccessBy = true, "member@x.io"
	st.Agents().Update(ctx, dev)
	job2, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "chat_turn", Origin: "user", Trigger: "ui", AgentID: dev.ID, Status: "running"})
	if _, ok := m.DirectApprover(ctx, storage.Action{Kind: "run_command", Target: "ls", JobID: job2.ID}); ok {
		t.Fatal("full access approved though its enabler is not an admin")
	}
}

// ADR-074 security fix: job.FullAccess/FullAccessBy are computed once, when
// the job started, but a proposal can stay pending a while after that (queued
// behind other jobs, or waiting on a human-in-the-loop approval) — if the
// admin who enabled full access is demoted AFTER the job started but BEFORE
// the proposal is decided, DirectApprover must re-check their admin status
// right now, not just trust what was true when the job was created.
func TestDirectApproverRechecksAdminAtDecisionTime(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	agents, _ := engine.Agents(ctx, project.ID)
	agent := agents[0]

	admin, _ := st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), nil)
	// the job was created while admin@x.io still was an admin
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "chat_turn", Origin: "user", Trigger: "ui", AgentID: agent.ID, Status: "running",
		FullAccess: true, FullAccessBy: "admin@x.io"})
	if by, ok := m.DirectApprover(ctx, storage.Action{Kind: "run_command", Target: "ls", JobID: job.ID}); !ok || by != "admin@x.io" {
		t.Fatalf("still an admin: full access = %q %v", by, ok)
	}

	// demoted (disabled) after the job started, before the proposal is decided
	st.Users().SetDisabled(ctx, admin.ID, true)
	if _, ok := m.DirectApprover(ctx, storage.Action{Kind: "run_command", Target: "ls", JobID: job.ID}); ok {
		t.Fatal("full access auto-approved though its enabler is no longer an admin")
	}
}

// An automation's own permission override (ADR-074) approves at once, in the
// name of who enabled it; its default ("agent" mode, no override) falls back
// to the agent's own — pending here since that agent has no FullAccess.
func TestFullAccessApproverAutomationOverride(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	agents, _ := engine.Agents(ctx, project.ID)
	agent := agents[0] // no FullAccess of its own

	st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	st.Users().Create(ctx, storage.User{Email: "member@x.io", Role: storage.RoleMember, PasswordHash: "h"})

	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), nil)

	override, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Sync", Source: "schedule", Action: "chat",
		AgentID: agent.ID, Enabled: true, PermissionMode: "override", OverrideFullAccess: true, OverrideAdminBy: "admin@x.io"})
	// job.FullAccess is computed once, when a job is created (ADR-074 security
	// fix) — tests that create a job directly must set it themselves, as
	// internal/trigger.Runner would have (its schedule trigger is trusted).
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "chat_turn", Origin: "automation", OriginID: override.ID, Trigger: "schedule", AgentID: agent.ID, Status: "running",
		FullAccess: true, FullAccessBy: "admin@x.io"})
	if by, ok := m.DirectApprover(ctx, storage.Action{Kind: "run_command", Target: "ls", JobID: job.ID}); !ok || by != "admin@x.io" {
		t.Fatalf("automation override = %q %v", by, ok)
	}
	if _, ok := m.DirectApprover(ctx, storage.Action{Kind: "git_push", Target: "origin fix", JobID: job.ID}); ok {
		t.Fatal("a push was approved under an automation's override")
	}

	// the default: no override, falls back to the agent (no FullAccess of its own)
	plain, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Plain", Source: "schedule", Action: "chat",
		AgentID: agent.ID, Enabled: true})
	job2, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "chat_turn", Origin: "automation", OriginID: plain.ID, Trigger: "schedule", AgentID: agent.ID, Status: "running"})
	if _, ok := m.DirectApprover(ctx, storage.Action{Kind: "run_command", Target: "ls", JobID: job2.ID}); ok {
		t.Fatal("agent mode without FullAccess was approved")
	}

	// an override whose enabler is no longer an admin: never approved
	revoked, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Revoked", Source: "schedule", Action: "chat",
		AgentID: agent.ID, Enabled: true, PermissionMode: "override", OverrideFullAccess: true, OverrideAdminBy: "member@x.io"})
	job3, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "chat_turn", Origin: "automation", OriginID: revoked.ID, Trigger: "schedule", AgentID: agent.ID, Status: "running"})
	if _, ok := m.DirectApprover(ctx, storage.Action{Kind: "run_command", Target: "ls", JobID: job3.ID}); ok {
		t.Fatal("override held though its enabler is not an admin")
	}
}

// A scheduled automation with its override set to full access gets its job
// created with FullAccess=true (through the real internal/trigger.Runner, not
// set by hand), so a normal command it proposes is approved at once; a push
// still waits (ADR-074).
func TestScheduledOverrideAutoApprovesButPushStillAsks(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	agents, _ := engine.Agents(ctx, project.ID)
	agent := agents[0] // no FullAccess of its own: only the override grants it

	st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	runner := trigger.New(st, chatExec{engine})
	m := channels.NewManager(st, engine, runner, nil)

	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Sync", Source: "schedule", Action: "chat", Enabled: true,
		AgentID: agent.ID, PermissionMode: "override", OverrideFullAccess: true, OverrideAdminBy: "admin@x.io", Limits: storage.AutomationLimits{DailyCostUSD: 5, DisableAfterFailures: 3}})
	job, _, err := runner.Enqueue(ctx, a, "schedule", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !job.FullAccess || job.FullAccessBy != "admin@x.io" {
		t.Fatalf("scheduled override job = %+v", job)
	}
	if by, ok := m.DirectApprover(ctx, storage.Action{Kind: "run_command", Target: "ls", JobID: job.ID}); !ok || by != "admin@x.io" {
		t.Fatalf("a normal command was not auto-approved: %q %v", by, ok)
	}
	if _, ok := m.DirectApprover(ctx, storage.Action{Kind: "git_push", Target: "origin fix", JobID: job.ID}); ok {
		t.Fatal("a push was auto-approved under a scheduled override")
	}
}

// A bot's two lists (ADR-081): a message from its Admin list runs as the
// agent's own (marked admin), one from Người dùng is not; nobody else is
// answered.
func TestAdminAndUsers(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	bin := filepath.Join(tmp, "claude")
	os.WriteFile(bin, []byte(`#!/bin/sh
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	provs.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	engine := chat.NewEngine(st, provs, u)
	bot := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"8"}, Approvers: []string{"7"}, Header: "-"})
	st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "@bot", Source: "discord", Action: "chat", Enabled: true, Config: storage.AutomationConfig{ChannelID: ch.ID}})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	for _, in := range []channels.Incoming{
		{ChatID: "c1", MessageID: "m1", UserID: "7", UserName: "an", Text: "chào", Addressed: true},
		{ChatID: "c2", MessageID: "m2", UserID: "8", UserName: "binh", Text: "chào", Addressed: true},
		{ChatID: "c3", MessageID: "m3", UserID: "9", UserName: "la", Text: "chào", Addressed: true},
	} {
		bot.in <- in
	}
	admin := map[string]bool{}
	for deadline := time.Now().Add(5 * time.Second); len(admin) < 2 && time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
		jobs, _ := st.Jobs().List(ctx, storage.JobFilter{ProjectID: project.ID})
		for _, j := range jobs {
			var p trigger.ChannelPayload
			if json.Unmarshal([]byte(j.Payload), &p) == nil && p.UserID != "" {
				admin[p.UserID] = p.Admin
			}
		}
	}
	if len(admin) != 2 || !admin["7"] || admin["8"] {
		t.Fatalf("admin by sender = %v (9, not listed, must not be there)", admin)
	}
}

// fileBot is a bot that takes files.
type fileBot struct {
	fakeBot
	files chan string
}

func (b *fileBot) SendFile(_ context.Context, chatID, name string, data []byte, caption string) (string, error) {
	b.files <- chatID + "|" + name + "|" + string(data) + "|" + caption
	return "f1", nil
}
func (b *fileBot) MaxFile() int64 { return 1 << 20 }

// ADR-083: a bot's chat run posts a file of its folder to that chat; nothing
// outside it, no secret's file.
func TestSendFileFor(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	engine := chat.NewEngine(st, provider.NewService(st, box, llm.Options{}), usage.New(st, time.UTC))
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "shot.png"), []byte("PNG"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1"), 0o644)
	outside := filepath.Join(t.TempDir(), "x.png")
	os.WriteFile(outside, []byte("X"), 0o644)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: dir})
	bot := &fileBot{fakeBot: fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}, files: make(chan string, 4)}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Dev", Enabled: true, Allow: []string{"*"}, Header: "-"})
	m := channels.NewManager(st, engine, trigger.New(st, chatExec{engine}), func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)
	for deadline := time.Now().Add(5 * time.Second); m.State(ch.ID) != "running"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the bot did not start")
		}
	}
	payload, _ := json.Marshal(trigger.ChannelPayload{ChannelID: ch.ID, ChatID: "c2", UserID: "7"})
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: project.ID, Kind: "chat_turn", Origin: "automation", Trigger: "discord", Payload: string(payload), Status: "running"})
	sc := actions.Scope{ProjectID: project.ID, JobID: job.ID}
	if _, err := m.SendFileFor(ctx, sc, "shot.png", "trang chủ"); err != nil {
		t.Fatal(err)
	}
	if got := <-bot.files; got != "c2|shot.png|PNG|trang chủ" {
		t.Fatalf("sent = %q", got)
	}
	for _, bad := range []string{".env", outside, "../x.png"} {
		if _, err := m.SendFileFor(ctx, sc, bad, ""); err == nil {
			t.Errorf("%s was sent", bad)
		}
	}
	if _, err := m.SendFileFor(ctx, actions.Scope{ProjectID: project.ID}, "shot.png", ""); err == nil {
		t.Error("sent from a run that is no bot's chat")
	}
}
