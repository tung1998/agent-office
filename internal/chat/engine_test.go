package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/usage"
)

type fixture struct {
	st      storage.Store
	engine  *chat.Engine
	provs   *provider.Service
	project storage.Repo
	dir     string
}

func setup(t *testing.T, providerIn func(provs *provider.Service) storage.Provider) fixture {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	st, err := sqlite.Open(filepath.Join(tmp, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	providerIn(provs)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\nworld\n"), 0o644)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "demo", Path: dir})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	return fixture{st: st, engine: chat.NewEngine(st, provs, u), provs: provs, project: project, dir: dir}
}

func collect(t *testing.T, turn *chat.Turn) []chat.Event {
	t.Helper()
	deadline := time.After(15 * time.Second)
	seq := 0
	var all []chat.Event
	for {
		evs, done, wake := turn.Since(seq)
		all = append(all, evs...)
		seq += len(evs)
		if done {
			return all
		}
		select {
		case <-wake:
		case <-deadline:
			t.Fatalf("turn did not finish; events: %+v", all)
		}
	}
}

func TestAnthropicToolLoopAndPatch(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Messages []json.RawMessage `json:"messages"`
			System   string            `json:"system"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if !strings.Contains(body.System, "không ghi file trực tiếp") {
			t.Errorf("system prompt missing patch rule: %q", body.System)
		}
		if calls == 1 {
			w.Write([]byte(`{"model":"claude-sonnet-5","stop_reason":"tool_use","content":[{"type":"text","text":"Để tôi đọc file."},{"type":"tool_use","id":"tu1","name":"read_file","input":{"path":"hello.txt"}}],"usage":{"input_tokens":100,"output_tokens":20}}`))
			return
		}
		// the tool result must have been sent back
		last := string(body.Messages[len(body.Messages)-1])
		if !strings.Contains(last, "1\\thello") {
			t.Errorf("tool result not returned: %s", last)
		}
		answer := "File có 2 dòng. Sửa như sau:\n```diff\n--- a/hello.txt\n+++ b/hello.txt\n@@ -1,2 +1,2 @@\n-hello\n+xin chào\n world\n```"
		out, _ := json.Marshal(map[string]any{"model": "claude-sonnet-5", "stop_reason": "end_turn",
			"content": []map[string]any{{"type": "text", "text": answer}}, "usage": map[string]int{"input_tokens": 150, "output_tokens": 60}})
		w.Write(out)
	}))
	defer srv.Close()
	f := setup(t, func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, err := provs.Create(context.Background(), provider.Input{Name: "C", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})
		if err != nil {
			t.Fatal(err)
		}
		return p
	})
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, err := f.engine.StartConversation(ctx, f.project.ID, "")
	if err != nil || conv.AgentName != "Trợ lý" {
		t.Fatalf("conversation = %+v, %v", conv, err)
	}
	turn, _, err := f.engine.Send(ctx, conv.ID, "Sửa lời chào trong hello.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.engine.Send(ctx, conv.ID, "again", nil); err != chat.ErrBusy {
		t.Fatalf("concurrent send err = %v", err)
	}
	events := collect(t, turn)
	types := []string{}
	for _, e := range events {
		types = append(types, e.Type)
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "tool") || !strings.Contains(joined, "patch") || !strings.HasSuffix(joined, "done") {
		t.Fatalf("event types = %s", joined)
	}
	done := events[len(events)-1].Message
	if len(done.Patches) != 1 || done.Patches[0].Status != "pending" || done.Patches[0].Files[0] != "hello.txt" || len(done.Tools) != 1 {
		t.Fatalf("done message = %+v", done)
	}
	if done.CostUSD == nil {
		t.Fatal("chat cost not recorded")
	}
	runs, _ := f.st.Runs().List(ctx, storage.RunFilter{Limit: 5})
	if len(runs) != 1 || runs[0].Kind != "chat" || runs[0].InputTokens != 250 {
		t.Fatalf("runs = %+v", runs)
	}

	// history keeps messages and patches
	hist, _ := f.engine.History(ctx, conv.ID)
	if len(hist) != 2 || hist[0].Role != "user" || len(hist[1].Patches) != 1 {
		t.Fatalf("history = %+v", hist)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// approved on the dashboard and from a bot at once: applied once, the
	// others are told it was decided (ADR-084)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		applied []chat.PatchDTO
		dup     int
	)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := f.engine.DecidePatch(ctx, done.Patches[0].ID, true)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				applied = append(applied, p)
			case errors.Is(err, chat.ErrDecided):
				dup++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(applied) != 1 || dup != 2 || applied[0].Status != "applied" || applied[0].DecidedBy != "human:a@b.c" {
		t.Fatalf("approve at once = %+v, %d refused", applied, dup)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "hello.txt")); string(b) != "xin chào\nworld\n" {
		t.Fatalf("file after approve = %q", b)
	}
	if _, err := f.engine.DecidePatch(ctx, done.Patches[0].ID, false); err != chat.ErrDecided {
		t.Fatalf("second decision err = %v", err)
	}
	if p, _ := f.st.Chat().GetPatch(ctx, done.Patches[0].ID); p.Status != "applied" {
		t.Fatalf("stored status = %s", p.Status)
	}
}

func TestClaudeCLIRunnerStreams(t *testing.T) {
	tmp := t.TempDir()
	bin, argsLog := filepath.Join(tmp, "claude"), filepath.Join(tmp, "args")
	os.WriteFile(bin, []byte(`#!/bin/sh
echo "$*" >> `+argsLog+`
case "$*" in *"--tools Read,Glob,Grep"*) ;; *) echo "missing read-only tools: $*" >&2; exit 2;; esac
case "$*" in *"--permission-mode dontAsk"*) ;; *) echo "may ask or bypass: $*" >&2; exit 2;; esac
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Xin "}}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"hello.txt"}}]}}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"chào"}}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"Xin chào","session_id":"sess-1","total_cost_usd":0.02,"usage":{"input_tokens":5,"cache_read_input_tokens":100,"output_tokens":7}}'
`), 0o755)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, err := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		if err != nil {
			t.Fatal(err)
		}
		return p
	})
	ctx := context.Background()
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	turn, _, err := f.engine.Send(ctx, conv.ID, "chào", nil)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, turn)
	var text strings.Builder
	for _, e := range events {
		if e.Type == "text" {
			text.WriteString(e.Text)
		}
		if e.Type == "error" {
			t.Fatalf("error event: %s", e.Text)
		}
	}
	done := events[len(events)-1].Message
	if text.String() != "Xin chào" || done.Content != "Xin chào" || len(done.Tools) != 1 || done.Tools[0].Summary != "Đọc hello.txt" {
		t.Fatalf("streamed=%q done=%+v", text.String(), done)
	}
	members, _ := f.st.Chat().Members(ctx, conv.ID) // the session is the agent's (ADR-044)
	if len(members) != 1 || members[0].SessionID != "sess-1" || members[0].Runtime != "claude_cli" || members[0].LastMessageID == "" {
		t.Fatalf("session not saved: %+v", members)
	}
	runs, _ := f.st.Runs().List(ctx, storage.RunFilter{Limit: 5})
	if len(runs) != 1 || runs[0].CostSource != "provider" || runs[0].InputTokens != 105 {
		t.Fatalf("runs = %+v", runs)
	}
	// default: the user's own CLI setup, with skills
	b, _ := os.ReadFile(argsLog)
	if a := string(b); !strings.Contains(a, "--setting-sources user,project,local") || strings.Contains(a, "--strict-mcp-config") ||
		!strings.Contains(a, "--tools Read,Glob,Grep,Skill") {
		t.Fatalf("default args = %s", a)
	}
}

// After an answer, the conversation knows how full its context is and the
// provider's latest usage windows are kept (shown in chat and the sidebar).
func TestClaudeCLIKeepsContextAndLimits(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "claude")
	os.WriteFile(bin, []byte(`#!/bin/sh
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.2,"resetsAt":1790593200}}}}'
echo '{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":4,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0},"content":[]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"sess-1","usage":{"input_tokens":4,"output_tokens":2},"modelUsage":{"claude-sonnet-5":{"contextWindow":200000}}}'
`), 0o755)
	var prov storage.Provider
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, err := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		if err != nil {
			t.Fatal(err)
		}
		prov = p
		return p
	})
	heard := make(chan chat.Limits, 1)
	f.engine.SetOnLimits(func(_ storage.Provider, l chat.Limits) { heard <- l })
	ctx := context.Background()
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	turn, _, err := f.engine.Send(ctx, conv.ID, "chào", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	conv, _ = f.st.Chat().GetConversation(ctx, conv.ID)
	if conv.ContextTokens != 1004 || conv.ContextWindow != 200000 {
		t.Fatalf("context = %d/%d", conv.ContextTokens, conv.ContextWindow)
	}
	var lim chat.Limits
	if ok, _ := f.st.Settings().Get(ctx, chat.LimitsKey(prov.ID), &lim); !ok || lim.Windows["five_hour"].Utilization != 0.2 {
		t.Fatalf("limits = %v %+v", ok, lim)
	}
	select { // and told to whoever watches them (the limit alerts)
	case got := <-heard:
		if got.Windows["five_hour"].Utilization != 0.2 {
			t.Fatalf("heard %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the limits were not told")
	}
}

// The person picks who answers in a chat (each agent has its own rights).
func TestSwitchConversationAgent(t *testing.T) {
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: "/bin/false"})
		return p
	})
	ctx := context.Background()
	agents, _ := f.engine.Agents(ctx, f.project.ID)
	if len(agents) < 2 {
		m, _ := f.st.OrgModels().GetForRepo(ctx, f.project.ID)
		if _, err := f.st.Agents().Create(ctx, storage.Agent{OrgModelID: m.ID, Key: "dev", Name: "Dev", Tier: storage.TierWorker, ModelTier: "fast"}); err != nil {
			t.Fatal(err)
		}
		agents, _ = f.engine.Agents(ctx, f.project.ID)
	}
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, agents[0].ID)
	conv.SessionID, conv.Runtime = "sess-old", "claude_cli"
	f.st.Chat().UpdateConversation(ctx, conv)
	if err := f.engine.SetAgent(ctx, conv.ID, "agt_nope"); err == nil {
		t.Fatal("an agent of another project was accepted")
	}
	if err := f.engine.SetAgent(ctx, conv.ID, agents[1].ID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.st.Chat().GetConversation(ctx, conv.ID)
	if got.AgentID != agents[1].ID || got.AgentName != agents[1].Name { // sessions stay per agent (ADR-044)
		t.Fatalf("conversation = %+v", got)
	}
}

// toHistory names the answers of other agents (after a switch).
func TestHistoryKeepsOtherAuthors(t *testing.T) {
	h := chat.HistoryFor([]storage.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b", Author: "Trưởng nhóm"}, {Role: "assistant", Content: "c", Author: "Dev"}}, "Dev")
	if len(h) != 3 || h[1].Author != "Trưởng nhóm" || h[2].Author != "" {
		t.Fatalf("history = %+v", h)
	}
}

// fakeTeamClaude answers as the agent named in its system prompt, with a
// session per agent, and logs every call's args and stdin.
func fakeTeamClaude(t *testing.T, reply string) (bin, dir string) {
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte(`#!/bin/sh
n=$(ls `+dir+` | grep -c 'args$')
n=$((n+1))
echo "$*" > `+dir+`/call$n.args
cat > `+dir+`/call$n.in
who=lead
case "$*" in *"Bạn là Dev"*) who=dev;; esac
echo '{"type":"system","subtype":"init","session_id":"sess-'$who'"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"'$who': `+reply+`","session_id":"sess-'$who'","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	return bin, dir
}

func call(t *testing.T, dir string, n int) (args, stdin string) {
	a, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("call%d.args", n)))
	i, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("call%d.in", n)))
	return string(a), string(i)
}

// ADR-044: each agent keeps its own session in a chat; coming back it gets
// only what was said since, with who said it.
func TestEachAgentKeepsItsSession(t *testing.T) {
	bin, dir := fakeTeamClaude(t, "ok")
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := context.Background()
	m, _ := f.st.OrgModels().GetForRepo(ctx, f.project.ID)
	dev, err := f.st.Agents().Create(ctx, storage.Agent{OrgModelID: m.ID, Key: "dev", Name: "Dev", Tier: storage.TierWorker, ModelTier: "fast"})
	if err != nil {
		t.Fatal(err)
	}
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	lead := conv.AgentID
	say := func(text string) {
		t.Helper()
		turn, _, err := f.engine.Send(ctx, conv.ID, text, nil)
		if err != nil {
			t.Fatal(err)
		}
		collect(t, turn)
	}
	say("chào")
	f.engine.SetAgent(ctx, conv.ID, dev.ID)
	say("sửa đi")
	f.engine.SetAgent(ctx, conv.ID, lead)
	say("tiếp")
	args2, in2 := call(t, dir, 2)
	if strings.Contains(args2, "--resume") || !strings.Contains(in2, "Cuộc trò chuyện trước đó") {
		t.Fatalf("Dev's first turn should be a transcript:\n%s\n%s", args2, in2)
	}
	args3, in3 := call(t, dir, 3)
	if !strings.Contains(args3, "--resume sess-lead") || !strings.Contains(in3, "[Dev]") || !strings.Contains(in3, "dev: ok") || !strings.Contains(in3, "tiếp") {
		t.Fatalf("lead's turn back:\n%s\n%s", args3, in3)
	}
	members, _ := f.st.Chat().Members(ctx, conv.ID)
	if len(members) != 2 || members[1].AgentID != dev.ID || members[1].SessionID != "sess-dev" {
		t.Fatalf("members = %+v", members)
	}
}

// fakeGroupClaude: like fakeTeamClaude, replies from reply-<who> files and
// sleeps from sleep-<who> files (who = lead | dev).
func fakeGroupClaude(t *testing.T) (bin, dir string) {
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte(`#!/bin/sh
n=$(ls `+dir+` | grep -c 'args$')
n=$((n+1))
echo "$*" > `+dir+`/call$n.args
cat > `+dir+`/call$n.in
who=lead
case "$*" in *"Bạn là Dev"*) who=dev;; esac
case "$*" in *"-p /compact"*) echo '{"type":"system","subtype":"compact_boundary","compact_metadata":{"pre_tokens":150000,"post_tokens":4000}}'; echo '{"type":"result","subtype":"success","is_error":false,"result":""}'; exit 0;; esac
if [ -f `+dir+`/fail-$who ]; then rm `+dir+`/fail-$who; echo '{"type":"system","subtype":"init","session_id":"sess-'$who'"}'; echo 'boom' >&2; exit 1; fi
[ -f `+dir+`/sleep-$who ] && sleep $(cat `+dir+`/sleep-$who)
reply=ok
[ -f `+dir+`/reply-$who ] && reply=$(cat `+dir+`/reply-$who)
echo '{"type":"system","subtype":"init","session_id":"sess-'$who'"}'
echo '{"type":"assistant","message":{"model":"m","usage":{"input_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"'$who': '"$reply"'","session_id":"sess-'$who'","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	return bin, dir
}

type group struct {
	f       fixture
	dir     string
	conv    storage.Conversation
	lead    string
	dev     storage.Agent
	leadNm  string
	engine  *chat.Engine
	context context.Context
}

func newGroup(t *testing.T) group {
	bin, dir := fakeGroupClaude(t)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := context.Background()
	m, _ := f.st.OrgModels().GetForRepo(ctx, f.project.ID)
	dev, err := f.st.Agents().Create(ctx, storage.Agent{OrgModelID: m.ID, Key: "dev", Name: "Dev", Tier: storage.TierWorker, ModelTier: "fast"})
	if err != nil {
		t.Fatal(err)
	}
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	return group{f: f, dir: dir, conv: conv, lead: conv.AgentID, leadNm: conv.AgentName, dev: dev, engine: f.engine, context: ctx}
}

// sendAll sends text and follows every turn of the chain; the replies' authors.
func (g group) sendAll(t *testing.T, text string) (authors []string) {
	t.Helper()
	turn, _, err := g.engine.Send(g.context, g.conv.ID, text, nil)
	if err != nil {
		t.Fatal(err)
	}
	for turn != nil {
		events := collect(t, turn)
		last := events[len(events)-1]
		if last.Message != nil {
			authors = append(authors, last.Message.Author)
		}
		next, _ := g.engine.Turn(last.NextTurnID)
		if last.NextTurnID == "" {
			next = nil
		}
		turn = next
	}
	return authors
}

func TestMentionPullsAgentIn(t *testing.T) { // ADR-044
	g := newGroup(t)
	if got := g.sendAll(t, "@Dev xem lỗi này"); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("authors = %v", got)
	}
	if got := g.sendAll(t, "cảm ơn"); len(got) != 1 || got[0] != g.leadNm {
		t.Fatalf("no tag → the default agent, got %v", got)
	}
	members, _ := g.f.st.Chat().Members(g.context, g.conv.ID)
	if len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}
}

func TestTwoTagsAnswerInOrder(t *testing.T) {
	g := newGroup(t)
	got := g.sendAll(t, "@"+g.leadNm+" @Dev cùng xem")
	if len(got) != 2 || got[0] != g.leadNm || got[1] != "Dev" {
		t.Fatalf("authors = %v", got)
	}
	_, in2 := call(t, g.dir, 2)
	if !strings.Contains(in2, "lead: ok") || !strings.Contains(in2, "cùng xem") {
		t.Fatalf("Dev should see the lead's answer and the question:\n%s", in2)
	}
}

// waitAuthors waits until the chat has n replies (and errors), returning
// "author" for replies and "!" for notes.
func (g group) waitAuthors(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		msgs, _ := g.f.st.Chat().ListMessages(g.context, g.conv.ID)
		var out []string
		for _, m := range msgs {
			switch m.Role {
			case "assistant":
				out = append(out, m.Author)
			case "error":
				out = append(out, "!")
			}
		}
		_, busy := g.engine.Active(g.conv.ID)
		if len(out) >= n && !busy && len(g.engine.Running(g.conv.ID)) == 0 {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %v", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// delegateDuring hands a task to agent from inside the answer in progress,
// as the delegate tool does (the fake CLI cannot call office's MCP).
func (g group) delegateDuring(t *testing.T, agent, task string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if turn, ok := g.engine.Active(g.conv.ID); ok {
			if _, err := g.engine.Delegate(g.context, officetools.Scope{ProjectID: g.f.project.ID, ConversationID: g.conv.ID, RunRef: turn.ID, Agent: g.leadNm}, agent, task); err != nil {
				t.Fatal(err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no answer in progress")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ADR-044: an agent hands off with the delegate tool (like a subagent); it runs
// in the background and the one who asked reports back. A plain @Name in an
// answer only mentions: it pulls nobody in.
func TestAgentHandoffRunsInBackground(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "reply-lead"), []byte("như @Dev nói hôm qua"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "reply-dev"), []byte("xong"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "làm việc X", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.delegateDuring(t, "Dev", "sửa lỗi X trong api.go")
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	// lead → Dev (bg, from the tool) → lead reports; the lead's "@Dev" mention adds nothing
	got := g.waitAuthors(t, 3)
	want := []string{g.leadNm, "Dev", g.leadNm}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("authors = %v, want %v", got, want)
	}
	_, devIn := call(t, g.dir, 2)
	if !strings.Contains(devIn, "sửa lỗi X trong api.go") {
		t.Fatalf("Dev's prompt lacks the task:\n%s", devIn)
	}
	_, reportIn := call(t, g.dir, 3)
	if !strings.Contains(reportIn, "dev: xong") {
		t.Fatalf("report prompt:\n%s", reportIn)
	}
}

func TestMentionInAnswerPullsNobody(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "reply-lead"), []byte("@Dev có thể làm, bạn muốn giao không?"), 0o644)
	if got := g.sendAll(t, "ai làm được"); len(got) != 1 {
		t.Fatalf("authors = %v", got)
	}
	if got := g.waitAuthors(t, 1); len(got) != 1 {
		t.Fatalf("a mention pulled someone in: %v", got)
	}
}

func TestDelegateChecks(t *testing.T) {
	g := newGroup(t)
	sc := officetools.Scope{ProjectID: g.f.project.ID, ConversationID: g.conv.ID, RunRef: "nope", Agent: g.leadNm}
	if _, err := g.engine.Delegate(g.context, sc, "Nobody", "x"); err == nil {
		t.Fatal("an unknown agent was accepted")
	}
	if _, err := g.engine.Delegate(g.context, sc, g.leadNm, "x"); err == nil {
		t.Fatal("delegating to itself was accepted")
	}
	if _, err := g.engine.Delegate(g.context, officetools.Scope{ProjectID: g.f.project.ID, TaskID: "tsk_1", Agent: g.leadNm}, "Dev", "x"); err == nil {
		t.Fatal("delegate outside a chat was accepted")
	}
}

func TestPersonKeepsChattingWhileAgentWorks(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-dev"), []byte("2"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "làm X", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.delegateDuring(t, "Dev", "làm X")
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	os.WriteFile(filepath.Join(g.dir, "reply-lead"), []byte("đang đợi Dev"), 0o644)
	if got := g.sendAll(t, "trong lúc đợi thì sao"); len(got) != 1 || got[0] != g.leadNm {
		t.Fatalf("the person is not blocked by Dev, got %v", got)
	}
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "@Dev nhanh lên", nil); !errors.Is(err, chat.ErrAgentBusy) {
		t.Fatalf("tagging a working agent: %v", err)
	}
	if running := g.engine.Running(g.conv.ID); len(running) != 1 || running[0].AgentName != "Dev" || !running[0].Background {
		t.Fatalf("running = %+v", running)
	}
	g.waitAuthors(t, 4)
}

func TestStopDropsTheQueue(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("2"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "@"+g.leadNm+" @Dev xem", nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	turn.Cancel()
	events := collect(t, turn)
	if last := events[len(events)-1]; last.NextTurnID != "" {
		t.Fatalf("stopped, yet next = %s", last.NextTurnID)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(g.dir, "call2.args")); err == nil {
		t.Fatal("Dev ran after Stop")
	}
}

func TestBusyWhileTheNextAnswers(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-dev"), []byte("2"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "@"+g.leadNm+" @Dev xem", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn) // the lead is done, Dev is answering
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "chen ngang", nil); !errors.Is(err, chat.ErrBusy) {
		t.Fatalf("err = %v, want busy", err)
	}
}

// Review I2: later turns of a chain use the chat as it is now, and never write
// back an old copy (the person switched the default agent meanwhile).
func TestLaterTurnsKeepTheChatsSettings(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "sleep-dev"), []byte("1"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "làm X", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.delegateDuring(t, "Dev", "làm X")
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	if err := g.engine.SetAgent(g.context, g.conv.ID, g.dev.ID); err != nil { // while Dev works
		t.Fatal(err)
	}
	g.waitAuthors(t, 3) // Dev, then the lead reports
	c, _ := g.f.st.Chat().GetConversation(g.context, g.conv.ID)
	if c.AgentID != g.dev.ID {
		t.Fatalf("default agent written back to %s", c.AgentName)
	}
}

// Review I3: an agent never answers in the foreground while it works in the
// background (same session, same worktree).
func TestAgentNeverRunsTwiceAtOnce(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "sleep-dev"), []byte("2"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "@"+g.leadNm+" @Dev cùng xem", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.delegateDuring(t, "Dev", "việc riêng") // Dev will be busy in the background
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	got := g.waitAuthors(t, 3)
	devs := 0
	for _, a := range got {
		if a == "Dev" {
			devs++
		}
	}
	if devs != 1 || !slices.Contains(got, "!") {
		t.Fatalf("authors = %v: Dev should answer once, with a note that it was busy", got)
	}
}

// Review I6: background hand-offs count as running (an update waits for them)
// and are found by their job (cancelling that job stops them, not the chat).
func TestBackgroundTurnsAreVisible(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "sleep-dev"), []byte("2"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "làm X", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.delegateDuring(t, "Dev", "làm X")
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	var bg chat.RunningTurn
	for _, r := range g.engine.Running(g.conv.ID) {
		if r.Background {
			bg = r
		}
	}
	if bg.TurnID == "" || g.engine.ActiveTurns() < 1 {
		t.Fatalf("running = %+v, active = %d", g.engine.Running(g.conv.ID), g.engine.ActiveTurns())
	}
	dt, _ := g.engine.Turn(bg.TurnID)
	if got, ok := g.engine.TurnByJob(dt.JobID); !ok || got != dt {
		t.Fatalf("TurnByJob(%s) = %v %v", dt.JobID, got, ok)
	}
	g.waitAuthors(t, 3)
}

// Stop in the chat stops everything in it: the answer and the hand-offs
// working in the background (and so no report-back either).
func TestStopAllStopsBackground(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "sleep-dev"), []byte("3"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "làm X", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.delegateDuring(t, "Dev", "làm X")
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	if n := g.engine.StopAll(g.conv.ID); n != 1 {
		t.Fatalf("stopped %d turns, want the background one", n)
	}
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(filepath.Join(g.dir, "call3.args")); err == nil {
		t.Fatal("the lead reported back after Stop")
	}
	if len(g.engine.Running(g.conv.ID)) != 0 {
		t.Fatalf("still running: %+v", g.engine.Running(g.conv.ID))
	}
}

// Cheaper where it is enough: the report after a hand-off only sums up, so a
// strong agent writes it with the balanced model; and a ctx model tier (an
// automation's choice) overrides the agent's.
func TestCheaperModelForReportsAndOverrides(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "làm X", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.delegateDuring(t, "Dev", "làm X")
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	g.waitAuthors(t, 3)
	a1, _ := call(t, g.dir, 1)
	a3, _ := call(t, g.dir, 3)
	model := func(args string) string {
		_, after, _ := strings.Cut(args, "--model ")
		m, _, _ := strings.Cut(after, " ")
		return m
	}
	if model(a1) == model(a3) || !strings.Contains(model(a3), "sonnet") {
		t.Fatalf("answer model %q, report model %q", model(a1), model(a3))
	}
	// an automation asking for the fast tier
	ctx := chat.WithModelTier(g.context, "fast")
	turn, _, err = g.engine.Send(ctx, g.conv.ID, "việc hằng ngày", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	a4, _ := call(t, g.dir, 4)
	if !strings.Contains(model(a4), "haiku") {
		t.Fatalf("override model %q", model(a4))
	}
}

// Review minor: an agent whose first turn failed still knows what it has seen,
// so its next resumed turn gets what was said in between.
func TestFailedTurnKeepsWhatWasSeen(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "fail-lead"), []byte("1"), 0o644)
	g.sendAll(t, "lần đầu")
	members, _ := g.f.st.Chat().Members(g.context, g.conv.ID)
	if len(members) != 1 || members[0].LastMessageID == "" {
		t.Fatalf("members = %+v", members)
	}
}

// A bot's chat (Discord/Telegram) gives work to the team as a web chat does.
func TestDelegateInChannelChat(t *testing.T) {
	g := newGroup(t)
	conv, err := g.engine.StartConversationPurpose(g.context, g.f.project.ID, "", "channel")
	if err != nil {
		t.Fatal(err)
	}
	sc := officetools.Scope{ProjectID: g.f.project.ID, ConversationID: conv.ID, RunRef: "r1", Agent: g.leadNm}
	if _, err := g.engine.Delegate(g.context, sc, "Dev", "sửa lỗi"); err != nil {
		t.Fatalf("delegate from a bot chat: %v", err)
	}
	auto, _ := g.engine.StartConversationPurpose(g.context, g.f.project.ID, "", "automation")
	if _, err := g.engine.Delegate(g.context, officetools.Scope{ProjectID: g.f.project.ID, ConversationID: auto.ID, RunRef: "r2", Agent: g.leadNm}, "Dev", "x"); err == nil {
		t.Fatal("delegate from an automation-building chat was accepted")
	}
}

// In a bot's chat (Discord/Telegram) @tags pull agents in as on the web.
func TestMentionInChannelChat(t *testing.T) {
	g := newGroup(t)
	conv, err := g.engine.StartConversationPurpose(g.context, g.f.project.ID, "", "channel")
	if err != nil {
		t.Fatal(err)
	}
	g.conv = conv
	if got := g.sendAll(t, "@"+g.leadNm+" @Dev xem lỗi này"); len(got) != 2 || got[0] != g.leadNm || got[1] != "Dev" {
		t.Fatalf("authors = %v", got)
	}
}

// The agent's notes (ADR-068) are in its system prompt, in every new conversation.
func TestNotesInSystemPrompt(t *testing.T) {
	tmp := t.TempDir()
	bin, argsLog := filepath.Join(tmp, "claude"), filepath.Join(tmp, "args")
	os.WriteFile(bin, []byte(`#!/bin/sh
printf '%s\n' "$@" >> `+argsLog+`
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"sess-1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := context.Background()
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	f.st.Memories().Create(ctx, storage.Memory{ProjectID: f.project.ID, AgentID: conv.AgentID, Text: "Repo dùng pnpm, không dùng npm"})
	turn, _, err := f.engine.Send(ctx, conv.ID, "chào", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	b, _ := os.ReadFile(argsLog)
	if a := string(b); !strings.Contains(a, "- Repo dùng pnpm, không dùng npm") || !strings.Contains(a, "Ghi nhớ của bạn") {
		t.Fatalf("args = %s", a)
	}
}

// A bot's chat in administrator mode runs the turn with the machine.
func TestFullAccessTurn(t *testing.T) {
	tmp := t.TempDir()
	bin, argsLog := filepath.Join(tmp, "claude"), filepath.Join(tmp, "args")
	os.WriteFile(bin, []byte(`#!/bin/sh
echo "$*" >> `+argsLog+`
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"sess-1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := context.Background()
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	turn, _, err := f.engine.Send(chat.WithFullAccess(ctx), conv.ID, "chạy lệnh", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	b, _ := os.ReadFile(argsLog)
	if a := string(b); !strings.Contains(a, "--permission-mode bypassPermissions") || !strings.Contains(a, "administrator") {
		t.Fatalf("args = %s", a)
	}
}

func fullAccessFakeBin(t *testing.T, tmp string) (bin, argsLog string) {
	t.Helper()
	bin, argsLog = filepath.Join(tmp, "claude"), filepath.Join(tmp, "args")
	os.WriteFile(bin, []byte(`#!/bin/sh
echo "$*" >> `+argsLog+`
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"sess-1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	return bin, argsLog
}

// An agent an admin configured with "Chạy như administrator" (ADR-074) runs
// with the machine, once the chat/task is at Vận hành (Operate).
func TestAgentFullAccessPermission(t *testing.T) {
	bin, argsLog := fullAccessFakeBin(t, t.TempDir())
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := actor.With(context.Background(), "human:admin@x.io")
	if _, err := f.st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	f.engine.SetMode(ctx, conv.ID, perm.Operate)
	agent, err := f.st.Agents().Get(ctx, conv.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	agent.Permissions.Level, agent.Permissions.FullAccess, agent.Permissions.FullAccessBy = perm.Operate, true, "admin@x.io"
	if err := f.st.Agents().Update(ctx, agent); err != nil {
		t.Fatal(err)
	}
	turn, _, err := f.engine.Send(ctx, conv.ID, "chạy lệnh", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	b, _ := os.ReadFile(argsLog)
	if a := string(b); !strings.Contains(a, "--permission-mode bypassPermissions") || !strings.Contains(a, "administrator") {
		t.Fatalf("agent full access args = %s", a)
	}
}

// A chat left at Chỉ đọc never escalates, even when its agent has FullAccess.
func TestAgentFullAccessDoesNotEscalatePastReadMode(t *testing.T) {
	bin, argsLog := fullAccessFakeBin(t, t.TempDir())
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := actor.With(context.Background(), "human:admin@x.io")
	f.st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	f.engine.SetMode(ctx, conv.ID, perm.Read) // Chỉ đọc
	agent, _ := f.st.Agents().Get(ctx, conv.AgentID)
	agent.Permissions.Level, agent.Permissions.FullAccess, agent.Permissions.FullAccessBy = perm.Operate, true, "admin@x.io"
	f.st.Agents().Update(ctx, agent)
	turn, _, err := f.engine.Send(ctx, conv.ID, "chạy lệnh", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	b, _ := os.ReadFile(argsLog)
	if a := string(b); strings.Contains(a, "bypassPermissions") {
		t.Fatalf("Chỉ đọc chat escalated to full access: %s", a)
	}
}

// If whoever turned FullAccess on is no longer an admin, the agent falls
// back to its normal level (ADR-074).
func TestAgentFullAccessRevokedWhenEnablerNotAdmin(t *testing.T) {
	bin, argsLog := fullAccessFakeBin(t, t.TempDir())
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := actor.With(context.Background(), "human:member@x.io")
	f.st.Users().Create(ctx, storage.User{Email: "member@x.io", Role: storage.RoleMember, PasswordHash: "h"}) // no longer admin
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	f.engine.SetMode(ctx, conv.ID, perm.Operate)
	agent, _ := f.st.Agents().Get(ctx, conv.AgentID)
	agent.Permissions.Level, agent.Permissions.FullAccess, agent.Permissions.FullAccessBy = perm.Operate, true, "member@x.io"
	f.st.Agents().Update(ctx, agent)
	turn, _, err := f.engine.Send(ctx, conv.ID, "chạy lệnh", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	b, _ := os.ReadFile(argsLog)
	if a := string(b); strings.Contains(a, "bypassPermissions") {
		t.Fatalf("full access held after enabler lost admin: %s", a)
	}
}

// ADR-079: a task handed to another agent is its input and its result: a
// session of its own (not the agent's chat session) and no chat history.
func TestHandoffRunsOnItsOwn(t *testing.T) {
	g := newGroup(t)
	if got := g.sendAll(t, "@Dev xem giúp file api.go"); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("authors = %v", got) // Dev has its own session in the chat now
	}
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	turn, _, err := g.engine.Send(g.context, g.conv.ID, "bí mật trong lịch sử: CHUOI-RIENG", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.delegateDuring(t, "Dev", "chạy go vet và báo kết quả")
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	g.waitAuthors(t, 4) // Dev, lead, Dev (the task), lead (its report)
	args, in := call(t, g.dir, 3)
	if !strings.Contains(in, "chạy go vet và báo kết quả") {
		t.Fatalf("the task is not its input:\n%s", in)
	}
	if strings.Contains(args, "--resume") || strings.Contains(in, "CHUOI-RIENG") || strings.Contains(in, "xem giúp file api.go") {
		t.Fatalf("the handed-over task got the chat's session or history:\nargs %s\nin %s", args, in)
	}
}

// ADR-079: a session past 70% of what its model holds is compacted before
// the turn resumes it, as Claude Code does itself.
func TestFullSessionIsCompacted(t *testing.T) {
	g := newGroup(t)
	g.sendAll(t, "chào")
	ctx := context.Background()
	mems, _ := g.f.st.Chat().Members(ctx, g.conv.ID)
	if len(mems) != 1 || mems[0].SessionID == "" {
		t.Fatalf("members = %+v", mems)
	}
	m := mems[0]
	m.ContextTokens, m.ContextWindow = 150_000, 200_000
	g.f.st.Chat().UpsertMember(ctx, m)
	g.sendAll(t, "tiếp")
	compact, _ := call(t, g.dir, 2)
	run, _ := call(t, g.dir, 3)
	if !strings.Contains(compact, "-p /compact") || !strings.Contains(compact, "--resume "+m.SessionID) {
		t.Fatalf("not compacted first: %s", compact)
	}
	if !strings.Contains(run, "--resume "+m.SessionID) {
		t.Fatalf("the turn did not go on in its session: %s", run)
	}
}

// The office assistant limited to "answer only" (ADR-059) never escalates to
// full access either, even when its own agent has FullAccess (ADR-074 security fix).
func TestAgentFullAccessDoesNotEscalateUnderAnswerOnlyAssistant(t *testing.T) {
	bin, argsLog := fullAccessFakeBin(t, t.TempDir())
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := actor.With(context.Background(), "human:admin@x.io")
	f.st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	f.engine.SetAssistant(func(context.Context) string { return f.project.ID })
	assistant.SetMode(ctx, f.st, assistant.ModeAnswer)
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	f.engine.SetMode(ctx, conv.ID, perm.Operate)
	agent, _ := f.st.Agents().Get(ctx, conv.AgentID)
	agent.Permissions.Level, agent.Permissions.FullAccess, agent.Permissions.FullAccessBy = perm.Operate, true, "admin@x.io"
	f.st.Agents().Update(ctx, agent)
	turn, _, err := f.engine.Send(ctx, conv.ID, "chạy lệnh", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	b, _ := os.ReadFile(argsLog)
	if a := string(b); strings.Contains(a, "bypassPermissions") {
		t.Fatalf("answer-only assistant escalated to full access: %s", a)
	}
}

// ADR-081: a turn capped at proposing (a bot's message from Người dùng) runs
// at most there, whatever the chat's mode; its hand-offs keep the cap.
func TestCeilingCapsTheTurn(t *testing.T) {
	g := newGroup(t)
	g.engine.SetMode(g.context, g.conv.ID, perm.Operate)
	turn, _, err := g.engine.Send(chat.WithCeiling(g.context, perm.Propose), g.conv.ID, "chạy lệnh giúp", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	args, _ := call(t, g.dir, 1)
	if !strings.Contains(args, "Quyền của bạn trong lượt này: "+perm.Label(perm.Propose)) {
		t.Fatalf("the cap was not applied:\n%s", args)
	}
}

// ADR-084: proposals decided on the dashboard a moment apart send the chat's
// agent one message with both results, and it goes on.
func TestDecidedGoesOn(t *testing.T) {
	g := newGroup(t)
	g.sendAll(t, "chuẩn bị deploy")
	g.engine.SetDecidedWait(150 * time.Millisecond)
	g.engine.Decided(g.conv.ID, "admin@x.io", "✅ Đã duyệt Chạy lệnh: pnpm test — ok")
	g.engine.Decided(g.conv.ID, "admin@x.io", "❌ Đã từ chối Push lên remote: origin main")
	got := g.waitAuthors(t, 2)
	if len(got) != 2 {
		t.Fatalf("authors = %v", got)
	}
	_, in := call(t, g.dir, 2)
	if !strings.Contains(in, "pnpm test") || !strings.Contains(in, "origin main") || !strings.Contains(in, "Làm tiếp") {
		t.Fatalf("one message with both results:\n%s", in)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(g.dir, "call3.args")); err == nil {
		t.Fatal("the agent was run twice for one batch")
	}
}

// The chat's agents were paused meanwhile: decisions run no AI (the chat
// gets the paused notice), and a later batch does not replay the old one.
func TestDecidedPausedAgentDoesNotRun(t *testing.T) {
	g := newGroup(t)
	g.sendAll(t, "chuẩn bị deploy")
	before := cliCalls(t, g.dir)
	g.pause(t, g.lead)
	g.pause(t, g.dev.ID)
	g.engine.SetDecidedWait(50 * time.Millisecond)
	g.engine.Decided(g.conv.ID, "admin@x.io", "✅ Đã duyệt Chạy lệnh: pnpm test — ok")
	deadline := time.Now().Add(2 * time.Second)
	for ; time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		msgs, _ := g.f.st.Chat().ListMessages(g.context, g.conv.ID)
		if last := msgs[len(msgs)-1]; last.Role == "error" && strings.Contains(last.Content, "tạm nghỉ") {
			break
		}
	}
	if time.Now().After(deadline) {
		t.Fatal("no paused notice")
	}
	time.Sleep(200 * time.Millisecond)
	if n := cliCalls(t, g.dir); n != before {
		t.Fatalf("AI ran %d times for paused agents", n-before)
	}
}

// A bot chat's decisions go to its bot (its answer goes back to the chat), not
// to a turn on the dashboard.
func TestDecidedInBotChatGoesToTheBot(t *testing.T) {
	g := newGroup(t)
	ctx := context.Background()
	conv, _ := g.f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: g.f.project.ID, AgentID: g.lead, Purpose: "channel"})
	got := make(chan string, 1)
	g.engine.SetOnBotDecided(func(_ context.Context, id, who string, lines []string) {
		got <- id + "|" + who + "|" + strings.Join(lines, ";")
	})
	g.engine.SetDecidedWait(50 * time.Millisecond)
	g.engine.Decided(conv.ID, "admin@x.io", "✅ ok")
	select {
	case s := <-got:
		if s != conv.ID+"|admin@x.io|✅ ok" {
			t.Fatalf("to the bot = %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not handed to the bot")
	}
	if _, err := os.Stat(filepath.Join(g.dir, "call1.args")); err == nil {
		t.Fatal("a turn ran on the dashboard for a bot's chat")
	}
}
