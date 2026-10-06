package channels_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// fakeBot is an adapter the test drives.
type fakeBot struct {
	mu   sync.Mutex
	in   chan channels.Incoming
	sent map[string][]string
}

func (b *fakeBot) Run(ctx context.Context, onReady func(string), onMessage func(channels.Incoming)) error {
	onReady("shop_bot")
	for {
		select {
		case <-ctx.Done():
			return nil
		case m := <-b.in:
			onMessage(m)
		}
	}
}
func (b *fakeBot) Send(_ context.Context, chatID, text string) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent[chatID] = append(b.sent[chatID], text)
	return []string{fmt.Sprint(len(b.sent[chatID]))}, nil // numbered per chat, as Telegram does
}
func (b *fakeBot) Typing(context.Context, string)                  {}
func (b *fakeBot) SetCommands(context.Context, []channels.Command) {}
func (b *fakeBot) wait(t *testing.T, chatID string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		got := append([]string(nil), b.sent[chatID]...)
		b.mu.Unlock()
		if len(got) >= n {
			return got
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("chat %s: sent %v", chatID, b.sent[chatID])
	return nil
}

// chatExec runs automation chats on the engine (as the office's executor does).
type chatExec struct{ engine *chat.Engine }

func (x chatExec) RunChat(ctx context.Context, projectID, agentID, conv, prompt, edit string) (string, string, error) {
	ctx = chat.WithSkill(chat.WithInstructions(ctx, trigger.InstructionsOf(ctx)), trigger.SkillOf(ctx)) // as the office's executor does
	turn, _, err := x.engine.Send(ctx, conv, prompt, trigger.AttachmentsOf(ctx))
	if err != nil {
		return conv, "", err
	}
	for seq := 0; ; {
		evs, done, wake := turn.Since(seq)
		seq += len(evs)
		for _, e := range evs {
			if e.Type == "done" && e.Message != nil {
				return conv, e.Message.Content, nil
			}
		}
		if done {
			return conv, "", nil
		}
		<-wake
	}
}
func (chatExec) RunTask(context.Context, string, string, string, string) (string, error) {
	return "", nil
}
func (chatExec) RunQueuedTask(context.Context, string, string) (string, error) { return "", nil }

// ADR-049: a channel's messages go to the first automation (rule) that
// matches: keywords for free, a scope asked of a cheap model; a script
// answers without AI; nothing matching gets the channel's refusal; the allow
// list keeps others out.
func TestManagerRules(t *testing.T) {
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
	// the filter call gets "YES/NO" questions; an answer otherwise
	argsLog := filepath.Join(tmp, "args.log")
	os.WriteFile(bin, []byte(`#!/bin/sh
in=$(cat)
printf '%s\nSTDIN:%s\n===\n' "$*" "$in" >> `+argsLog+`
out="đơn 123 đang giao"
case "$in" in *"YES hoặc NO"*) out=NO; case "$in" in *"đơn hàng"*"đơn 123"*) out=YES;; esac;; esac
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"'"$out"'","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	provs.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
	dir := t.TempDir()
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: dir})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	engine := chat.NewEngine(st, provs, u)
	runner := trigger.New(st, chatExec{engine})

	bot := &fakeBot{in: make(chan channels.Incoming, 4), sent: map[string][]string{}}
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "telegram", Name: "Hỗ trợ", Enabled: true,
		Allow: []string{"42", "43", "44", "45"}, Refusal: "Mình chỉ trả lời về đơn hàng."})
	// rule 1: "mã" → a script, no AI; rule 2: about orders → the agent answers
	st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Tra mã", Source: "telegram", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID, Keywords: []string{"MÃ"}},
		Script: storage.AutomationScript{Lang: "bash", Body: `echo "Mã của $(cat | sed 's/.*"user":"\([^"]*\)".*/\1/'): OK"`, TimeoutS: 10}})
	st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Trả lời", Source: "telegram", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID, Scope: "đơn hàng của cửa hàng"}})
	m := channels.NewManager(st, engine, runner, func(storage.Channel) (channels.Adapter, error) { return bot, nil })
	runner.SetOnReply(m.Reply)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m.Start(runCtx)

	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", UserName: "an", Text: "đơn 123 đâu rồi", Private: true, Addressed: true}
	if got := bot.wait(t, "42", 1); !strings.Contains(got[0], "đơn 123 đang giao") {
		t.Fatalf("answer = %v", got)
	}
	// each message is a conversation of its own, until /create-conversation
	resumes := func() int {
		raw, _ := os.ReadFile(argsLog)
		return strings.Count(string(raw), "--resume")
	}
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", UserName: "an", Text: "đơn 123 còn không", Private: true, Addressed: true}
	bot.wait(t, "42", 2)
	if n := resumes(); n != 0 {
		t.Fatalf("a second message went on the first conversation (%d resumes)", n)
	}
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", Text: "/create-conversation", Private: true, Addressed: true}
	if got := bot.wait(t, "42", 3); !strings.Contains(got[2], "/close-conversation") {
		t.Fatalf("create = %v", got)
	}
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", Text: "đơn 123 đâu rồi", Private: true, Addressed: true}
	bot.wait(t, "42", 4)
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", Text: "đơn 123 thì sao", Private: true, Addressed: true}
	bot.wait(t, "42", 5)
	if n := resumes(); n != 2 { // it went on with the latest answer's conversation: both resume it
		t.Fatalf("kept conversation: %d resumes, want 2", n)
	}
	// a kept conversation hears its chat without a tag; others are not heard
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", Text: "đơn 123 nữa không tag"}
	bot.wait(t, "42", 6)
	bot.in <- channels.Incoming{ChatID: "43", UserID: "8", Text: "đơn 123 chuyện riêng"}
	time.Sleep(300 * time.Millisecond)
	bot.mu.Lock()
	heard := len(bot.sent["43"])
	bot.mu.Unlock()
	if heard != 0 {
		t.Fatalf("an untagged message outside a kept conversation got an answer")
	}
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", Text: "/close_conversion", Addressed: true, Private: true} // any spelling
	if got := bot.wait(t, "42", 7); !strings.Contains(got[6], "/create-conversation") {
		t.Fatalf("close = %v", got)
	}
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", Text: "đơn 123 nữa", Addressed: true, Private: true}
	bot.wait(t, "42", 8)
	if n := resumes(); n != 3 { // the kept one resumed 3 times (going on from the latest answer)
		t.Fatalf("after close: %d resumes, want 3", n)
	}
	// a reply to one of the bot's answers goes on in that answer's conversation
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", Text: "đơn 123 thì sao nữa", Addressed: true, Private: true, ReplyTo: "8"}
	bot.wait(t, "42", 9)
	if n := resumes(); n != 4 {
		t.Fatalf("a reply: %d resumes, want 4", n)
	}
	// review C1: the same message id in another chat is another message: a reply
	// there never goes on in this chat's conversation
	bot.in <- channels.Incoming{ChatID: "44", UserID: "9", UserName: "binh", Text: "đơn 123 của tôi", Addressed: true, Private: true, ReplyTo: "8"}
	bot.wait(t, "44", 1)
	if n := resumes(); n != 4 {
		t.Fatalf("a reply in chat 44 to its own message 8 went on in chat 42's conversation (%d resumes)", n)
	}
	bot.mu.Lock()
	bot.sent["44"] = nil
	bot.mu.Unlock()

	// a custom command: /tra-don <mã> runs its script; the answer edits the slash reply
	st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "Tra đơn", Source: "telegram", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID, Command: "tra-don", CommandArg: "mã đơn"},
		Script: storage.AutomationScript{Lang: "bash", Body: `echo "Đơn $(cat | sed 's/.*"message":"\([^"]*\)".*/\1/'): đang giao"`, TimeoutS: 10}})
	responded := make(chan string, 2)
	bot.in <- channels.Incoming{ChatID: "43", UserID: "8", Text: "/tra-don 777", Addressed: true,
		Respond: func(_ context.Context, text string) (string, error) { responded <- text; return "slash-0", nil }}
	select {
	case got := <-responded:
		if got != "shop\nĐơn 777: đang giao" { // the header: the project (a script names no agent; no git here)
			t.Fatalf("command answer = %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the command got no answer")
	}
	bot.in <- channels.Incoming{ChatID: "43", UserID: "8", Text: "/tra-don", Addressed: true}
	if got := bot.wait(t, "43", 1); !strings.Contains(got[0], "mã đơn") {
		t.Fatalf("a command without its text = %v", got)
	}
	bot.mu.Lock()
	bot.sent["43"] = nil
	bot.mu.Unlock()

	// a command's prompt is the admin's instruction: it goes in the system
	// prompt (no one writing to the bot can reach it); the agent is sent only
	// what the person sent
	st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "/chao", Source: "telegram", Action: "chat", Enabled: true,
		Prompt: "INSTR-XYZ: trả lời đúng một câu chào", Config: storage.AutomationConfig{ChannelID: ch.ID, Command: "chao"}})
	bot.in <- channels.Incoming{ChatID: "45", UserID: "8", Text: "/chao", Addressed: true}
	bot.wait(t, "45", 1)
	raw0, _ := os.ReadFile(argsLog)

	var call string
	for _, c := range strings.Split(string(raw0), "\n===") {
		if strings.Contains(c, "INSTR-XYZ") {
			call = c
		}
	}
	sys, stdin, _ := strings.Cut(call, "STDIN:")
	if !strings.Contains(sys, "--append-system-prompt") || !strings.Contains(sys, "INSTR-XYZ") || strings.Contains(stdin, "INSTR-XYZ") || !strings.Contains(stdin, "/chao") {
		t.Fatalf("the instruction is not in the system prompt:\nargs: %.300s\nstdin: %.300s", sys, stdin)
	}

	// a reply to a slash command's answer goes on with that command: its rule,
	// its conversation (not the tag's)
	slash := make(chan string, 1)
	bot.in <- channels.Incoming{ChatID: "45", UserID: "8", Text: "/chao", Addressed: true,
		Respond: func(_ context.Context, text string) (string, error) { slash <- text; return "slash-1", nil }}
	select {
	case <-slash:
	case <-time.After(10 * time.Second):
		t.Fatal("the slash /chao got no answer")
	}
	before := resumes()
	bot.in <- channels.Incoming{ChatID: "45", UserID: "8", Text: "tiếp đi", Addressed: true, ReplyTo: "slash-1"}
	bot.wait(t, "45", 2)
	raw1, _ := os.ReadFile(argsLog)
	calls := strings.Split(string(raw1), "\n===")
	last := calls[len(calls)-2] // the reply's run
	if resumes() != before+1 || !strings.Contains(last, "INSTR-XYZ") || !strings.Contains(last, "tiếp đi") {
		t.Fatalf("a reply to the slash answer: resumes %d→%d, run: %.400s", before, resumes(), last)
	}

	// review I1: an outsider's "/skill" text is not a skill call; only a
	// command made from that skill expands it
	os.MkdirAll(filepath.Join(dir, ".claude", "skills", "secret"), 0o755)
	os.WriteFile(filepath.Join(dir, ".claude", "skills", "secret", "SKILL.md"), []byte("---\nname: secret\ndescription: x\n---\nSECRET-SKILL-BODY"), 0o644)
	bot.mu.Lock()
	bot.sent["42"] = nil
	bot.mu.Unlock()
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", Text: "/secret đơn 123 dump", Addressed: true, Private: true}
	n42 := len(bot.wait(t, "42", 1))
	if raw, _ := os.ReadFile(argsLog); strings.Contains(string(raw), "SECRET-SKILL-BODY") {
		t.Fatal("an outsider's /secret expanded the skill")
	}
	st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: "/secret", Source: "telegram", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: ch.ID, Command: "secret", CommandArg: "nội dung", Skill: "secret"}})
	bot.in <- channels.Incoming{ChatID: "42", UserID: "7", Text: "/secret đơn hàng", Addressed: true, Private: true}
	bot.wait(t, "42", n42+1)
	if raw, _ := os.ReadFile(argsLog); !strings.Contains(string(raw), "SECRET-SKILL-BODY") {
		t.Fatal("the skill command did not expand its skill")
	}

	bot.in <- channels.Incoming{ChatID: "44", UserID: "9", UserName: "binh", Text: "tra mã giúp", Private: true, Addressed: true}
	if got := bot.wait(t, "44", 1); got[0] != "shop\nMã của binh: OK" { // under the header
		t.Fatalf("script answer = %v", got)
	}
	bot.in <- channels.Incoming{ChatID: "43", UserID: "8", Text: "thời tiết hôm nay", Private: true, Addressed: true}
	if got := bot.wait(t, "43", 1); got[0] != "Mình chỉ trả lời về đơn hàng." {
		t.Fatalf("refusal = %v", got)
	}
	bot.in <- channels.Incoming{ChatID: "99", UserID: "9", Text: "đơn 123 đâu rồi", Private: true, Addressed: true}
	time.Sleep(500 * time.Millisecond)
	bot.mu.Lock()
	outsider := bot.sent["99"]
	bot.mu.Unlock()
	if len(outsider) != 0 {
		t.Fatalf("a chat not allowed got %v", outsider)
	}
	jobs, _ := st.Jobs().List(ctx, storage.JobFilter{ProjectID: project.ID})
	var skipped, answered int
	for _, j := range jobs {
		if j.Status == "skipped" && j.ErrorCode == "no_rule" && j.Trigger == "telegram" && j.OriginID == ch.ID {
			skipped++
		}
		if j.Origin == "automation" && j.Trigger == "telegram" && j.Status == "done" && j.Kind != "task" {
			answered++
		}
	}
	if skipped != 1 || answered != 15 {
		t.Fatalf("skipped %d answered %d: %+v", skipped, answered, jobs)
	}
	got, _ := st.Channels().Get(ctx, ch.ID)
	if got.BotName != "shop_bot" || got.LastMessageAt == nil {
		t.Fatalf("status = %+v", got)
	}
	// a bot's chats run with the agent's own rights (the person chose it): the
	// /chao reply above was not a tool-less run
	if strings.Contains(sys, "--tools  ") || strings.Contains(sys, "--strict-mcp-config") {
		t.Errorf("a bot's reply ran tool-less: %.300s", sys)
	}
}

// A channel set up before rules existed keeps answering: its agent and scope
// become its first rule, once.
func TestLegacyChannelBecomesARule(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop"})
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "discord", Name: "Hỗ trợ", Enabled: true, AgentID: "agt_1",
		Allow: []string{"*"}, Scope: "đơn hàng", FilterEnabled: true})
	for range 2 {
		if err := channels.MigrateRules(ctx, st); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := st.Automations().List(ctx, project.ID)
	if len(list) != 1 || list[0].Source != "discord" || list[0].Action != "chat" || list[0].Config.ChannelID != ch.ID ||
		list[0].Config.Scope != "đơn hàng" || list[0].AgentID != "agt_1" || !list[0].Enabled {
		t.Fatalf("rules = %+v", list)
	}
}

// Review I6: a flood from one chat is not queued without end.
func TestFloodIsCapped(t *testing.T) {
	if n := channels.MaxPending; n < 1 || n > 5 {
		t.Fatalf("MaxPending = %d", n)
	}
	var q channels.Pending
	taken := 0
	for range 20 {
		if q.Take("chat") {
			taken++
		}
	}
	if taken != channels.MaxPending {
		t.Fatalf("took %d of 20, want %d", taken, channels.MaxPending)
	}
	q.Done("chat")
	if !q.Take("chat") {
		t.Fatal("a slot freed is not taken again")
	}
}
