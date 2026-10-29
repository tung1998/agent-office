package heartbeat

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/verify"
)

var t0 = time.Date(2026, 10, 28, 2, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

// The night zalo-native stalled (28/10): cards waiting while the coordinator
// re-delegated, the same build command proposed in several chats, two
// agents handing work back and forth with nothing merged.
func TestDetectStuckNight(t *testing.T) {
	f := storage.HarnessFacts{}
	for i, c := range []string{"c1", "c2", "c3"} {
		f.Actions = append(f.Actions, storage.HarnessAction{ID: "act" + c, ConversationID: c, Kind: "run_command", Target: "sh app/build.sh", Status: "pending", CreatedAt: at(i)})
	}
	f.Actions = append(f.Actions, storage.HarnessAction{ID: "actok", ConversationID: "c1", Kind: "run_command", Target: "git status", Status: "done", CreatedAt: at(1)})
	for i := range 10 {
		f.Jobs = append(f.Jobs, storage.HarnessJob{ID: "j", ConversationID: "c9", AgentID: []string{"ceo", "coder"}[i%2], Kind: "chat_turn", Status: "done", CreatedAt: at(20 + i)})
		// the person and one agent: no hand-off
		f.Jobs = append(f.Jobs, storage.HarnessJob{ID: "k", ConversationID: "c8", AgentID: "ceo", Kind: "chat_turn", Status: "done", CreatedAt: at(20 + i)})
	}
	got := map[string]Signal{}
	for _, s := range Detect(f, at(35)) {
		got[s.Kind] = s
	}
	if s := got["stalled_cards"]; !strings.Contains(s.Summary, "3 card(s)") || len(s.Evidence) != 3 {
		t.Fatalf("stalled = %+v", s)
	}
	if s := got["repeated_proposal"]; s.Key != "repeated_proposal:run_command: sh app/build.sh" {
		t.Fatalf("repeated = %+v", s)
	}
	if s := got["churn"]; s.Key != "churn:c9" {
		t.Fatalf("churn = %+v", s)
	}
	if len(got) != 3 {
		t.Fatalf("signals = %+v", got)
	}
}

// Normal work raises nothing: a fresh card, checks that ran each time, a
// busy chat whose diffs merge, failures that differ.
func TestDetectQuiet(t *testing.T) {
	f := storage.HarnessFacts{
		Actions: []storage.HarnessAction{
			{ID: "a1", Kind: "run_command", Target: "go test ./...", Status: "done", CreatedAt: at(0)},
			{ID: "a2", Kind: "run_command", Target: "go test ./...", Status: "done", CreatedAt: at(5)},
			{ID: "a3", Kind: "run_command", Target: "go test ./...", Status: "done", CreatedAt: at(9)},
			{ID: "a4", Kind: "git_push", Target: "main", Status: "pending", CreatedAt: at(28)},
			{ID: "a5", Kind: "config_change", Status: "pending", CreatedAt: at(1)},
			{ID: "a6", Kind: "config_change", Status: "pending", CreatedAt: at(2)},
			{ID: "a7", Kind: "config_change", Status: "rejected", CreatedAt: at(3)},
		},
		Patches: []storage.HarnessPatch{{ID: "p1", ConversationID: "c1", Status: "applied", CreatedAt: at(25)},
			{ID: "p2", ConversationID: "c1", Status: "rejected", Detail: verify.Held + ": go vet", CreatedAt: at(10)}},
		Jobs: []storage.HarnessJob{{ID: "f1", Status: "failed", Error: "rate limit", CreatedAt: at(1)}, {ID: "f2", Status: "failed", ErrorCode: "cancelled", CreatedAt: at(2)},
			{ID: "f3", Status: "failed", ErrorCode: "cancelled", CreatedAt: at(3)}, {ID: "f4", Status: "failed", ErrorCode: "cancelled", CreatedAt: at(4)}},
	}
	for i := range 10 {
		f.Jobs = append(f.Jobs, storage.HarnessJob{ConversationID: "c1", AgentID: []string{"a", "b"}[i%2], Status: "done", CreatedAt: at(10 + i)})
	}
	// the settings cards wait 29 min while 13 turns started since: a stall
	sigs := Detect(f, at(30))
	if len(sigs) != 1 || sigs[0].Kind != "stalled_cards" {
		t.Fatalf("signals = %+v", sigs)
	}
	f.Actions = f.Actions[:4] // the settings cards decided: only a fresh push card waits
	if sigs := Detect(f, at(30)); len(sigs) != 0 {
		t.Fatalf("quiet work raised %+v", sigs)
	}
}

func TestDetectFailuresAndChecks(t *testing.T) {
	f := storage.HarnessFacts{}
	for i := range 3 {
		f.Jobs = append(f.Jobs, storage.HarnessJob{ID: "j", Status: "failed", Error: "exit status 1: swift build 12 errors", CreatedAt: at(i)})
		f.Patches = append(f.Patches, storage.HarnessPatch{ID: "p", Status: "rejected", Detail: verify.Held + ":\n`sh app/build.sh` lỗi", CreatedAt: at(i)})
	}
	kinds := ""
	for _, s := range Detect(f, at(10)) {
		kinds += s.Kind + " "
	}
	if kinds != "repeated_failure checks_failing " {
		t.Fatalf("kinds = %q", kinds)
	}
}

type fakeChat struct {
	mu    sync.Mutex
	calls []string // agent id: prompt
	done  chan struct{}
}

func (f *fakeChat) RunChat(ctx context.Context, projectID, agentID, conv, prompt, edit string) (string, string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, agentID+": "+prompt)
	f.mu.Unlock()
	return "cnv_x", "ok", nil
}

// A stuck project gets one self-check turn, with its coordinator (the agent
// that may decide cards), then is left alone for the cooldown; the same sign
// is not raised again.
func TestCheckOpensOneTurn(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "zalo"})
	coder, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "coder", Name: "Coder", ModelTier: "balanced"})
	ceo, _ := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "ceo", Name: "CEO", ModelTier: "balanced", Permissions: storage.Permissions{Level: "operate", FullAccess: true}})
	c, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "x"})
	for range 3 {
		if _, err := st.Actions().Create(ctx, storage.Action{ProjectID: p.ID, ConversationID: c.ID, Kind: "run_command", Target: "sh app/build.sh", Status: "pending"}); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if _, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", ConversationID: c.ID, AgentID: coder.ID, Status: "done"}); err != nil {
			t.Fatal(err)
		}
	}
	fc := &fakeChat{}
	s := New(st, fc)
	opened := make(chan string, 2)
	s.opened = func(_, conv string) { opened <- conv }
	now := time.Now().Add(20 * time.Minute)
	s.now = func() time.Time { return now }

	sigs := s.Check(ctx, p)
	if len(sigs) == 0 {
		t.Fatal("no signal")
	}
	<-opened
	if len(fc.calls) != 1 || !strings.HasPrefix(fc.calls[0], ceo.ID+": [Self-check]") || !strings.Contains(fc.calls[0], "decide_request") || !strings.Contains(fc.calls[0], "sh app/build.sh") {
		t.Fatalf("calls = %q", fc.calls)
	}
	if again := s.Check(ctx, p); again != nil {
		t.Fatalf("in cooldown it raised %+v", again)
	}
	var stt state
	s.store.Settings().Get(ctx, stateKey(p.ID), &stt)
	stt.Last = now.Add(-cooldown - time.Minute) // past the cooldown, the same signs are still remembered
	s.store.Settings().Set(ctx, stateKey(p.ID), stt)
	if again := s.Check(ctx, p); again != nil {
		t.Fatalf("the same signs again: %+v", again)
	}
	for k := range stt.Seen { // long after: raised again
		stt.Seen[k] = now.Add(-remember)
	}
	s.store.Settings().Set(ctx, stateKey(p.ID), stt)
	if again := s.Check(ctx, p); again == nil {
		t.Fatal("not raised again after remember")
	}
	<-opened
}
