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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/team"
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
	org := team.NewService(st, nil)
	providerIn(provs)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\nworld\n"), 0o644)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "demo", Path: dir})
	org.AddFromLibrary(ctx, project.ID, team.StarterAgent)
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
		if !strings.Contains(body.System, "You do not write files directly") {
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
	fakebin.Write(bin, []byte(`#!/bin/sh
echo "$*" >> `+argsLog+`
case "$*" in *"--tools Read,Glob,Grep"*) ;; *) echo "missing read-only tools: $*" >&2; exit 2;; esac
case "$*" in *"--permission-mode dontAsk"*) ;; *) echo "may ask or bypass: $*" >&2; exit 2;; esac
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Xin "}}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"hello.txt"}}]}}'
echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"chào"}}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"Xin chào","session_id":"sess-1","total_cost_usd":0.02,"usage":{"input_tokens":5,"cache_read_input_tokens":100,"output_tokens":7}}'
`))
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
	fakebin.Write(bin, []byte(`#!/bin/sh
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.2,"resetsAt":1790593200}}}}'
echo '{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":4,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0},"content":[]}}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"sess-1","usage":{"input_tokens":4,"output_tokens":2},"modelUsage":{"claude-sonnet-5":{"contextWindow":200000}}}'
`))
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
		if _, err := f.st.Agents().Create(ctx, storage.Agent{ProjectID: f.project.ID, Key: "dev", Name: "Dev", ModelTier: "fast"}); err != nil {
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

func call(t *testing.T, dir string, n int) (args, stdin string) {
	a, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("call%d.args", n)))
	i, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("call%d.in", n)))
	return string(a), string(i)
}

// fakeGroupClaude: like fakeTeamClaude, replies from reply-<who> files and
// sleeps from sleep-<who> files (who = lead | dev).
func fakeGroupClaude(t *testing.T) (bin, dir string) {
	dir = t.TempDir()
	bin = filepath.Join(dir, "claude")
	fakebin.Write(bin, []byte(`#!/bin/sh
while ! mkdir `+dir+`/.lock 2>/dev/null; do sleep 0.01; done
n=$(ls `+dir+` | grep -c 'args$')
n=$((n+1))
echo "$*" > `+dir+`/call$n.args
pwd -P > `+dir+`/call$n.pwd
rmdir `+dir+`/.lock
cat > `+dir+`/call$n.in
who=lead
case "$*" in *"You are Dev"*) who=dev;; *"You are QA"*) who=qa;; esac
case "$*" in *"-p /compact"*) echo '{"type":"system","subtype":"compact_boundary","compact_metadata":{"pre_tokens":150000,"post_tokens":4000}}'; echo '{"type":"result","subtype":"success","is_error":false,"result":""}'; exit 0;; esac
if [ -f `+dir+`/fail-$who ]; then rm `+dir+`/fail-$who; echo '{"type":"system","subtype":"init","session_id":"sess-'$who'"}'; echo 'boom' >&2; exit 1; fi
[ -f `+dir+`/sleep-$who ] && sleep $(cat `+dir+`/sleep-$who)
reply=ok
[ -f `+dir+`/reply-$who ] && reply=$(cat `+dir+`/reply-$who)
echo '{"type":"system","subtype":"init","session_id":"sess-'$who'"}'
echo '{"type":"assistant","message":{"model":"m","usage":{"input_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"content":[]}}'
result="$who: $reply"
[ -f `+dir+`/raw-$who ] && result=$(cat `+dir+`/raw-$who)
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"'"$result"'","session_id":"sess-'$who'","usage":{"input_tokens":1,"output_tokens":1}}'
`))
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
	dev, err := f.st.Agents().Create(ctx, storage.Agent{ProjectID: f.project.ID, Key: "dev", Name: "Dev", ModelTier: "fast"})
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
	if got := g.sendAll(t, "chào"); len(got) != 1 || got[0] != g.leadNm {
		t.Fatalf("authors = %v", got)
	}
	if got := g.sendAll(t, "@Dev xem lỗi này"); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("authors = %v", got)
	}
	if got := g.sendAll(t, "cảm ơn"); len(got) != 1 || got[0] != g.leadNm {
		t.Fatalf("no tag → the chat's agent, got %v", got)
	}
	members, _ := g.f.st.Chat().Members(g.context, g.conv.ID)
	if len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}
}

// The first message of a chat tagging an agent makes it the chat's agent.
func TestFirstTagPicksChatAgent(t *testing.T) {
	g := newGroup(t)
	if got := g.sendAll(t, "@Dev xem lỗi này"); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("authors = %v", got)
	}
	if got := g.sendAll(t, "cảm ơn"); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("no tag after a first tag → Dev, got %v", got)
	}
	if conv, _ := g.f.st.Chat().GetConversation(g.context, g.conv.ID); conv.AgentName != "Dev" {
		t.Fatalf("chat's agent = %q", conv.AgentName)
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
func (g group) delegateDuring(t *testing.T, agent, task, effort string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if turn, ok := g.engine.Active(g.conv.ID); ok {
			if _, err := g.engine.Delegate(g.context, officetools.Scope{ProjectID: g.f.project.ID, ConversationID: g.conv.ID, RunRef: turn.ID, Agent: g.leadNm}, agent, task, effort); err != nil {
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

// Work handed to an agent runs at the effort its delegator asked for, else
// at the agent's own, never at the chat's.
func TestDelegateEffort(t *testing.T) {
	g := newGroup(t)
	if err := g.engine.SetEffort(g.context, g.conv.ID, "max"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ effort, want string }{{"low", "--effort low"}, {"", ""}} {
		old, _ := filepath.Glob(filepath.Join(g.dir, "call*"))
		for _, f := range old {
			os.Remove(f)
		}
		os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("0.3"), 0o644)
		if _, _, err := g.engine.Send(g.context, g.conv.ID, "@"+g.leadNm+" giao việc", nil); err != nil {
			t.Fatal(err)
		}
		g.delegateDuring(t, "Dev", "sửa lỗi", c.effort)
		g.waitAuthors(t, 2)
		calls, _ := filepath.Glob(filepath.Join(g.dir, "call*.args"))
		dev := ""
		for _, f := range calls {
			b, _ := os.ReadFile(f)
			if strings.Contains(string(b), "You are Dev") {
				dev = string(b)
			} else if !strings.Contains(string(b), "--effort max") {
				t.Fatalf("the lead's own turn lost the chat's effort: %s", b)
			}
		}
		if dev == "" {
			t.Fatalf("effort %q: Dev never ran", c.effort)
		}
		if got := strings.Contains(dev, "--effort"); got != (c.want != "") || (c.want != "" && !strings.Contains(dev, c.want)) {
			t.Fatalf("effort %q: Dev ran with %s", c.effort, dev)
		}
	}
	sc := officetools.Scope{ProjectID: g.f.project.ID, ConversationID: g.conv.ID, RunRef: "r", Agent: g.leadNm}
	if _, err := g.engine.Delegate(g.context, sc, "Dev", "x", "huge"); err == nil {
		t.Fatal("an unknown effort was accepted")
	}
}

func TestDelegateChecks(t *testing.T) {
	g := newGroup(t)
	sc := officetools.Scope{ProjectID: g.f.project.ID, ConversationID: g.conv.ID, RunRef: "nope", Agent: g.leadNm}
	if _, err := g.engine.Delegate(g.context, sc, "Nobody", "x", ""); err == nil {
		t.Fatal("an unknown agent was accepted")
	}
	if _, err := g.engine.Delegate(g.context, sc, g.leadNm, "x", ""); err == nil {
		t.Fatal("delegating to itself was accepted")
	}
	if _, err := g.engine.Delegate(g.context, officetools.Scope{ProjectID: g.f.project.ID, TaskID: "tsk_1", Agent: g.leadNm}, "Dev", "x", ""); err == nil {
		t.Fatal("delegate outside a chat was accepted")
	}
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

// A bot's chat (Discord/Telegram) gives work to the team as a web chat does.
func TestDelegateInChannelChat(t *testing.T) {
	g := newGroup(t)
	conv, err := g.engine.StartConversationPurpose(g.context, g.f.project.ID, "", "channel")
	if err != nil {
		t.Fatal(err)
	}
	sc := officetools.Scope{ProjectID: g.f.project.ID, ConversationID: conv.ID, RunRef: "r1", Agent: g.leadNm}
	if _, err := g.engine.Delegate(g.context, sc, "Dev", "sửa lỗi", ""); err != nil {
		t.Fatalf("delegate from a bot chat: %v", err)
	}
	auto, _ := g.engine.StartConversationPurpose(g.context, g.f.project.ID, "", "automation")
	if _, err := g.engine.Delegate(g.context, officetools.Scope{ProjectID: g.f.project.ID, ConversationID: auto.ID, RunRef: "r2", Agent: g.leadNm}, "Dev", "x", ""); err == nil {
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

// A bot's chat in administrator mode runs the turn with the machine.
func TestFullAccessTurn(t *testing.T) {
	tmp := t.TempDir()
	bin, argsLog := filepath.Join(tmp, "claude"), filepath.Join(tmp, "args")
	fakebin.Write(bin, []byte(`#!/bin/sh
echo "$*" >> `+argsLog+`
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"sess-1","usage":{"input_tokens":1,"output_tokens":1}}'
`))
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
	fakebin.Write(bin, []byte(`#!/bin/sh
echo "$*" >> `+argsLog+`
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"sess-1","usage":{"input_tokens":1,"output_tokens":1}}'
`))
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

// The power follows the agent (ADR-217): a person who is no admin (a member,
// or the one a paired office or a linked project speaks for) runs an agent an
// admin made administrator as administrator; what is not a person never does.
func TestAgentFullAccessFollowsTheAgent(t *testing.T) {
	for _, tc := range []struct {
		who  string
		full bool
	}{{"human:member@x.io", true}, {"human:gone@x.io", false}, {"bot:tg", false}} {
		t.Run(tc.who, func(t *testing.T) {
			bin, argsLog := fullAccessFakeBin(t, t.TempDir())
			f := setup(t, func(provs *provider.Service) storage.Provider {
				p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
				return p
			})
			ctx := actor.With(context.Background(), tc.who)
			f.st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
			f.st.Users().Create(ctx, storage.User{Email: "member@x.io", Role: storage.RoleMember, PasswordHash: "h"})
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
			if got := strings.Contains(string(b), "bypassPermissions"); got != tc.full {
				t.Fatalf("full access = %v, want %v: %s", got, tc.full, b)
			}
		})
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
	if !strings.Contains(args, "Your permission this turn: "+perm.Label(perm.Propose)) {
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
	if !strings.Contains(in, "pnpm test") || !strings.Contains(in, "origin main") || !strings.Contains(in, "Go on with") {
		t.Fatalf("one message with both results:\n%s", in)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(g.dir, "call3.args")); err == nil {
		t.Fatal("the agent was run twice for one batch")
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

// ADR-210: a hand-off tied to a task of the work board tells the board hook
// when it starts and when it is done, with its answer.
func TestBoardHook(t *testing.T) {
	g := newGroup(t)
	var mu sync.Mutex
	var evs []chat.BoardEvent
	g.engine.SetBoardHook(func(_ context.Context, ev chat.BoardEvent) {
		mu.Lock()
		evs = append(evs, ev)
		mu.Unlock()
	})
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "giao việc", nil); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if turn, ok := g.engine.Active(g.conv.ID); ok {
			ctx := officetools.WithBoardTask(g.context, "item_1")
			if _, err := g.engine.Delegate(ctx, officetools.Scope{ProjectID: g.f.project.ID, ConversationID: g.conv.ID, RunRef: turn.ID, Agent: g.leadNm}, "Dev", "sửa lỗi", ""); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no answer in progress")
		}
	}
	g.waitAuthors(t, 3)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		mu.Lock()
		n := len(evs)
		mu.Unlock()
		if n >= 2 {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(evs) != 2 || evs[0].Task != "item_1" || evs[0].Done || evs[0].Agent != "Dev" || !evs[1].Done || evs[1].Result != "dev: ok" {
		t.Fatalf("events: %+v", evs)
	}
}

// ADR-221: an agent whose turn ends with tasks of the work board waiting for
// it takes the next one on at once; sent once on a board as it stands.
func TestNextWorkGoesOn(t *testing.T) {
	g := newGroup(t)
	var mu sync.Mutex
	sig := "a"
	g.engine.SetNextHook(func(_ context.Context, projectID string, agent storage.Agent, dflt bool) (chat.NextWork, bool) {
		mu.Lock()
		defer mu.Unlock()
		if projectID != g.f.project.ID || agent.ID != g.lead || !dflt {
			t.Errorf("hook: %s %s %v", projectID, agent.Name, dflt)
		}
		return chat.NextWork{Task: "bit_1", Title: "sửa popup", Open: 1, Sig: sig}, true
	})
	if got := g.sendAll(t, "chào"); len(got) != 2 || got[0] != g.leadNm || got[1] != g.leadNm {
		t.Fatalf("authors = %v", got)
	}
	if a, in := call(t, g.dir, 2); !strings.Contains(a+in, "task_id bit_1") {
		t.Fatalf("next turn's prompt: %q %q", a, in)
	}
	if got := g.sendAll(t, "tiếp"); len(got) != 1 { // the board did not move: not sent again
		t.Fatalf("same board: authors = %v", got)
	}
	mu.Lock()
	sig = "b"
	mu.Unlock()
	if got := g.sendAll(t, "xong"); len(got) != 2 {
		t.Fatalf("board moved: authors = %v", got)
	}
}

// ADR-221: a hand-off given to an agent still busy with an earlier one in the
// chat waits for it and starts once it is done, instead of being dropped.
func TestHandoffWaitsForBusyAgent(t *testing.T) {
	g := newGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("0.5"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "sleep-dev"), []byte("2"), 0o644)
	hand := func(text, task string) {
		t.Helper()
		if _, _, err := g.engine.Send(g.context, g.conv.ID, text, nil); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if turn, ok := g.engine.Active(g.conv.ID); ok {
				if _, err := g.engine.Delegate(g.context, officetools.Scope{ProjectID: g.f.project.ID, ConversationID: g.conv.ID, RunRef: turn.ID, Agent: g.leadNm}, "Dev", task, ""); err != nil {
					t.Fatal(err)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("no answer in progress")
			}
		}
	}
	hand("giao 1", "việc một")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if r := g.engine.Running(g.conv.ID); len(r) == 1 && r[0].Background {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Dev did not start alone")
		}
	}
	hand("giao 2", "việc hai")
	got := g.waitAuthors(t, 6) // lead, lead, the note, Dev, lead's report, Dev again (and its report)
	devs := 0
	for _, a := range got {
		if a == "Dev" {
			devs++
		}
	}
	if devs != 2 {
		t.Fatalf("authors = %v", got)
	}
	var second bool
	calls, _ := filepath.Glob(filepath.Join(g.dir, "call*.in"))
	for _, c := range calls {
		b, _ := os.ReadFile(c)
		second = second || strings.Contains(string(b), "việc hai")
	}
	if !second {
		t.Fatal("the waiting hand-off never ran")
	}
}
