package chat_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

// advisorFixture: the project's default agent on an Anthropic API (the one
// that gets stuck), and an advisor server of another vendor (an OpenAI-
// compatible API) answering with reply; what it is asked is kept.
type advisorFixture struct {
	fixture
	stuck   storage.Agent
	other   storage.Provider // the advisor's connection
	asked   func() []string
	advisor *storage.Agent
}

func setupAdvisor(t *testing.T, reply string, withAdvisor bool) advisorFixture {
	t.Helper()
	var (
		mu    sync.Mutex
		asked []string
	)
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"model":"claude-x","stop_reason":"end_turn","content":[{"type":"text","text":"same vendor answered"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(srvA.Close)
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		asked = append(asked, string(b))
		mu.Unlock()
		out, _ := json.Marshal(map[string]any{"model": "gpt-x", "choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": reply}}},
			"usage": map[string]int{"prompt_tokens": 1000, "completion_tokens": 100}})
		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
	}))
	t.Cleanup(srvB.Close)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, err := provs.Create(context.Background(), provider.Input{Name: "Claude API", Kind: storage.ProviderAnthropic, BaseURL: srvA.URL, APIKey: &key})
		if err != nil {
			t.Fatal(err)
		}
		return p
	})
	ctx := context.Background()
	key := "sk-other-0000"
	other, err := f.provs.Create(ctx, provider.Input{Name: "Other", Kind: storage.ProviderOpenAICompatible, BaseURL: srvB.URL, APIKey: &key, TierModels: map[string]string{"strong": "gpt-x", "balanced": "gpt-x", "fast": "gpt-x"}})
	if err != nil {
		t.Fatal(err)
	}
	agents, _ := f.st.Agents().List(ctx, f.project.ID)
	af := advisorFixture{fixture: f, stuck: agents[0], other: other, asked: func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}}
	if withAdvisor {
		a, err := f.st.Agents().Create(ctx, storage.Agent{ProjectID: f.project.ID, Key: "codex", Name: "Codex", ModelTier: storage.TierStrong, ProviderID: other.ID, LLMModel: "gpt-x"})
		if err != nil {
			t.Fatal(err)
		}
		af.advisor = &a
	}
	return af
}

func (f advisorFixture) scope(job string) officetools.Scope {
	return officetools.Scope{ProjectID: f.project.ID, ConversationID: "cnv_x", JobID: job, Agent: f.stuck.Name}
}

// The second same failure asks the advisor of another vendor, once: what it
// says goes back with the failure, and is kept for the chat (ADR-262).
func TestAdviceAppendedToFailure(t *testing.T) {
	t.Parallel()
	f := setupAdvisor(t, "The path is wrong: run it from internal/chat.", true)
	ctx := context.Background()
	sc := f.scope("job_1")
	if note := f.engine.AdviseFailure(ctx, sc, "go test ./x", "--- FAIL: TestA (0.2s)\n a_test.go:9: boom"); note != "" {
		t.Fatalf("first failure advised: %q", note)
	}
	note := f.engine.AdviseFailure(ctx, sc, "go test ./x", "--- FAIL: TestA (0.9s)\n a_test.go:9: boom")
	if !strings.Contains(note, "The path is wrong") || !strings.Contains(note, "Codex") || !strings.Contains(note, "2 times") {
		t.Fatalf("note = %q", note)
	}
	asked := f.asked()
	if len(asked) != 1 || !strings.Contains(asked[0], "go test ./x") || !strings.Contains(asked[0], "FAIL: TestA") {
		t.Fatalf("advisor asked %d times: %v", len(asked), asked)
	}
	if note := f.engine.AdviseFailure(ctx, sc, "go test ./x", "--- FAIL: TestA (1s)"); note != "" || len(f.asked()) != 1 {
		t.Fatal("asked twice about one failure")
	}
	list, err := f.engine.Advice(ctx, "cnv_x")
	if err != nil || len(list) != 1 {
		t.Fatalf("advice kept: %v %v", list, err)
	}
	a := list[0]
	if a.Status != storage.AdviceAnswered || a.AdvisorName != "Codex" || a.AgentName != f.stuck.Name || a.Trigger != "repeat" || a.Command != "go test ./x" || a.Model == "" {
		t.Fatalf("advice = %+v", a)
	}
}

// Only an agent of another vendor advises: one of the same vendor is not
// asked, and the stuck agent is told why there was no second opinion.
func TestAdvisorDiffersFromVendor(t *testing.T) {
	t.Parallel()
	f := setupAdvisor(t, "x", false)
	ctx := context.Background()
	// a second agent on the same connection as the stuck one
	if _, err := f.st.Agents().Create(ctx, storage.Agent{ProjectID: f.project.ID, Key: "twin", Name: "Twin", ModelTier: storage.TierStrong}); err != nil {
		t.Fatal(err)
	}
	sc := f.scope("job_2")
	f.engine.AdviseFailure(ctx, sc, "make", "error: no rule")
	note := f.engine.AdviseFailure(ctx, sc, "make", "error: no rule")
	if !strings.Contains(note, "no advisor could be asked") || !strings.Contains(note, "another AI vendor") {
		t.Fatalf("note = %q", note)
	}
	list, _ := f.engine.Advice(ctx, "cnv_x")
	if len(list) != 1 || list[0].Status != storage.AdviceSkipped || list[0].AdvisorName != "" {
		t.Fatalf("advice = %+v", list)
	}
	// with one of another vendor, that one answers
	f2 := setupAdvisor(t, "Use the other target.", true)
	f2.st.Agents().Create(ctx, storage.Agent{ProjectID: f2.project.ID, Key: "twin", Name: "Twin", ModelTier: storage.TierStrong})
	sc = f2.scope("job_3")
	f2.engine.AdviseFailure(ctx, sc, "make", "error: no rule")
	if note := f2.engine.AdviseFailure(ctx, sc, "make", "error: no rule"); !strings.Contains(note, "Codex") || !strings.Contains(note, "Use the other target.") {
		t.Fatalf("note = %q", note)
	}
}

// A run asks for at most two advices; past them it is left to stop and ask.
func TestAdviceCapPerRun(t *testing.T) {
	t.Parallel()
	f := setupAdvisor(t, "advice", true)
	ctx := context.Background()
	sc := f.scope("job_4")
	got := 0
	for _, cmd := range []string{"a", "b", "c"} {
		f.engine.AdviseFailure(ctx, sc, cmd, "boom")
		if f.engine.AdviseFailure(ctx, sc, cmd, "boom") != "" {
			got++
		}
	}
	if got != 2 || len(f.asked()) != 2 {
		t.Fatalf("advised %d times, advisor asked %d", got, len(f.asked()))
	}
	// another run counts apart
	f.engine.AdviseFailure(ctx, f.scope("job_5"), "a", "boom")
	if f.engine.AdviseFailure(ctx, f.scope("job_5"), "a", "boom") == "" {
		t.Fatal("another run not advised")
	}
}

// No advisor without one: none named, says why; off in the settings: not
// even counted.
func TestNoAdvisorAvailableSaysWhy(t *testing.T) {
	t.Parallel()
	f := setupAdvisor(t, "x", false)
	ctx := context.Background()
	sc := f.scope("job_6")
	f.engine.AdviseFailure(ctx, sc, "npm test", "Error: cannot find module")
	note := f.engine.AdviseFailure(ctx, sc, "npm test", "Error: cannot find module")
	if !strings.Contains(note, "Do not run it again unchanged") || !strings.Contains(note, "no agent of another AI vendor") {
		t.Fatalf("note = %q", note)
	}
	u := usage.New(f.st, time.UTC)
	st, _ := u.Settings(ctx)
	st.AutoAdvisor = "off"
	if err := u.SaveSettings(ctx, st); err != nil {
		t.Fatal(err)
	}
	sc = f.scope("job_7")
	f.engine.AdviseFailure(ctx, sc, "npm test", "Error: cannot find module")
	if note := f.engine.AdviseFailure(ctx, sc, "npm test", "Error: cannot find module"); note != "" {
		t.Fatalf("advised while off: %q", note)
	}
	if list, _ := f.engine.Advice(ctx, "cnv_x"); len(list) != 1 {
		t.Fatalf("kept while off: %d", len(list))
	}
}

// Past the day's budget of the advisors no advisor is asked.
func TestBudgetStopsAdvice(t *testing.T) {
	t.Parallel()
	f := setupAdvisor(t, "advice", true)
	ctx := context.Background()
	u := usage.New(f.st, time.UTC)
	st, _ := u.Settings(ctx)
	st.AdvisorDailyUSD = 0.5
	u.SaveSettings(ctx, st)
	spent := 0.6
	if _, err := f.st.Advice().Create(ctx, storage.Advice{ProjectID: f.project.ID, Trigger: "repeat", Status: storage.AdviceAnswered, CostUSD: &spent}); err != nil {
		t.Fatal(err)
	}
	sc := f.scope("job_8")
	f.engine.AdviseFailure(ctx, sc, "go vet", "error: x")
	note := f.engine.AdviseFailure(ctx, sc, "go vet", "error: x")
	if len(f.asked()) != 0 || !strings.Contains(note, "budget for today is used up") {
		t.Fatalf("asked %d, note %q", len(f.asked()), note)
	}
}

// A run that repeated a failure ends: under repeat_done the advisor reads
// its answer first; an objection adds a warning, an OK leaves it.
func TestDoneReviewedAfterRepeat(t *testing.T) {
	t.Parallel()
	f := setupAdvisor(t, "OBJECT\nThe test it says passes was never run again.", true)
	ctx := context.Background()
	conv := storage.Conversation{ID: "cnv_x", ProjectID: f.project.ID}
	// repeat only (the default): no review
	f.engine.AdviseFailure(ctx, f.scope("job_9"), "go test", "FAIL")
	f.engine.AdviseFailure(ctx, f.scope("job_9"), "go test", "FAIL")
	if got := f.engine.ReviewBeforeDone(ctx, conv, f.stuck, "job_9", "Done, all green."); got != "Done, all green." {
		t.Fatalf("reviewed under repeat: %q", got)
	}
	u := usage.New(f.st, time.UTC)
	st, _ := u.Settings(ctx)
	st.AutoAdvisor = "repeat_done"
	u.SaveSettings(ctx, st)
	// a run with no repeated failure is not reviewed
	before := len(f.asked())
	if got := f.engine.ReviewBeforeDone(ctx, conv, f.stuck, "job_clean", "Done."); got != "Done." || len(f.asked()) != before {
		t.Fatal("a clean run was reviewed")
	}
	got := f.engine.ReviewBeforeDone(ctx, conv, f.stuck, "job_9", "Done, all green.")
	if !strings.HasPrefix(got, "Done, all green.") || !strings.Contains(got, "chưa đồng ý") || !strings.Contains(got, "never run again") {
		t.Fatalf("answer = %q", got)
	}
	asked := f.asked()
	if last := asked[len(asked)-1]; !strings.Contains(last, "Done, all green.") || !strings.Contains(last, "go test") {
		t.Fatalf("review prompt = %s", last)
	}
	list, _ := f.engine.Advice(ctx, "cnv_x")
	if a := list[len(list)-1]; a.Trigger != "done" || !a.Objection {
		t.Fatalf("review kept = %+v", a)
	}
}

// A CLI's own tool (Bash, Edit) failing the same way twice: the event is
// not shown, the advisor is asked in the background, and its answer comes
// with the run's next office tool call.
func TestNativeToolFailureAdvisedLater(t *testing.T) {
	t.Parallel()
	f := setupAdvisor(t, "Read the lockfile first.", true)
	ctx := context.Background()
	conv := storage.Conversation{ID: "cnv_x", ProjectID: f.project.ID}
	ev := chat.Event{Type: "tool_failed", Tool: &storage.ToolCall{Name: "Bash", Summary: "pnpm install"}, Text: "ERR_PNPM_OUTDATED_LOCKFILE 12 deps"}
	if shown := f.engine.ToolFailed(ctx, conv, f.stuck, "job_n", ev); len(shown) != 0 {
		t.Fatalf("tool_failed shown: %+v", shown)
	}
	f.engine.ToolFailed(ctx, conv, f.stuck, "job_n", ev)
	sc := f.scope("job_n")
	var note string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && note == ""; time.Sleep(20 * time.Millisecond) {
		note = f.engine.TakeAdvice(sc)
	}
	if !strings.Contains(note, "Read the lockfile first.") || !strings.Contains(note, "pnpm install") {
		t.Fatalf("note = %q", note)
	}
	if f.engine.TakeAdvice(sc) != "" {
		t.Fatal("handed over twice")
	}
	if shown := f.engine.ToolFailed(ctx, conv, f.stuck, "job_n", chat.Event{Type: "text", Text: "hi"}); len(shown) != 1 {
		t.Fatal("other events not passed on")
	}
}
