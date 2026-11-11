package trigger_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// goalExec is an agent that makes the file "done" in dir at its turn doneAt
// (0 = never), and a judge that answers with the verdicts given, in order.
type goalExec struct {
	mu       sync.Mutex
	dir      string
	doneAt   int
	prompts  []string
	verdicts []string
	judged   []string // the ceiling each judge ran under
}

func (g *goalExec) RunChat(ctx context.Context, projectID, agentID, conv, prompt, edit string) (string, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prompts = append(g.prompts, prompt)
	if len(g.prompts) == g.doneAt {
		_ = os.WriteFile(filepath.Join(g.dir, "done"), []byte("ok"), 0o644)
	}
	return "cnv_goal", "lượt " + string(rune('0'+len(g.prompts))), nil
}

func (g *goalExec) Judge(ctx context.Context, projectID, agentID, prompt string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.judged = append(g.judged, trigger.CeilingOf(ctx))
	v := g.verdicts[0]
	g.verdicts = g.verdicts[1:]
	return v, nil
}

func goalAutomation(t *testing.T, st storage.Store, p storage.Repo, g storage.AutomationGoal) storage.Automation {
	a, err := st.Automations().Create(context.Background(), storage.Automation{ProjectID: p.ID, Name: "fix", Source: "schedule", Action: "chat", Enabled: true,
		Prompt: "sửa cho tới khi xong", Config: storage.AutomationConfig{EveryMinutes: 60, Goal: &g}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mainJob(t *testing.T, st storage.Store, a storage.Automation) storage.Job {
	t.Helper()
	list, _ := st.Jobs().List(context.Background(), storage.JobFilter{OriginID: a.ID})
	for _, j := range list {
		if j.ParentJobID == "" {
			return j
		}
	}
	t.Fatal("no job")
	return storage.Job{}
}

// The check fails after the first turn: the chat is sent its output, and the
// second turn reaches the goal (ADR-132).
func TestGoalCheckThenPasses(t *testing.T) {
	st, p := openStore(t)
	ex := &goalExec{dir: p.Path, doneAt: 2}
	r := trigger.New(st, ex)
	a := goalAutomation(t, st, p, storage.AutomationGoal{Check: "test -f done || { echo 'chưa có file done'; exit 3; }"})
	runOnce(t, r, st, a, "")
	if len(ex.prompts) != 2 || !strings.Contains(ex.prompts[1], "chưa có file done") || !strings.Contains(ex.prompts[1], "exit 3") || !strings.Contains(ex.prompts[1], "round 2 of 5") {
		t.Fatalf("prompts = %q", ex.prompts)
	}
	j := mainJob(t, st, a)
	if j.Status != "done" || !strings.HasPrefix(j.Output, "Mục tiêu: đạt sau 2 vòng") || j.ExitCode == nil || *j.ExitCode != 0 {
		t.Fatalf("job = %s %q exit %v", j.Status, j.Output, j.ExitCode)
	}
}

// A goal with its own judge gets a short reflection note ("lesson") built
// from a failed round's feedback before the next round, instead of just the
// raw judge text (ADR-132 follow-up, closer to Reflexion).
func TestGoalJudgeReflectsBeforeNextRound(t *testing.T) {
	st, p := openStore(t)
	ex := &goalExec{dir: p.Path, verdicts: []string{
		"VERDICT: DISAGREE\nchưa xong việc X",
		"nên làm Y trước khi thử lại",
		"VERDICT: AGREE\nxong rồi",
	}}
	r := trigger.New(st, ex)
	a := goalAutomation(t, st, p, storage.AutomationGoal{Judge: true, Text: "làm xong việc X"})
	runOnce(t, r, st, a, "")
	if len(ex.prompts) != 2 || !strings.Contains(ex.prompts[1], "Lesson from last round: nên làm Y trước khi thử lại") {
		t.Fatalf("prompts = %q", ex.prompts)
	}
	if len(ex.judged) != 3 {
		t.Fatalf("judge calls = %d, want 3 (round1 judge, reflect, round2 judge)", len(ex.judged))
	}
	for _, c := range ex.judged {
		if c != "read" {
			t.Fatalf("judge/reflect must run read-only, got ceiling %q", c)
		}
	}
}

// A goal with only a check command (no judge) never pays for a reflection
// call: the check's own output is feedback enough.
func TestGoalCheckOnlyNoReflectCall(t *testing.T) {
	st, p := openStore(t)
	ex := &goalExec{dir: p.Path, doneAt: 2}
	r := trigger.New(st, ex)
	a := goalAutomation(t, st, p, storage.AutomationGoal{Check: "test -f done || { echo 'chưa có file done'; exit 3; }"})
	runOnce(t, r, st, a, "")
	if len(ex.judged) != 0 {
		t.Fatalf("a check-only goal must not call the judge/reflect agent, got %d calls", len(ex.judged))
	}
	if !strings.Contains(ex.prompts[1], "chưa có file done") {
		t.Fatalf("prompts = %q", ex.prompts)
	}
}

// A goal's run cut off by a restart and put back goes on with the round it
// was cut in: its rounds are counted once, not again from 1 (ADR-258).
func TestResumedGoalKeepsRounds(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &goalExec{dir: p.Path}
	r := trigger.New(st, ex)
	a := goalAutomation(t, st, p, storage.AutomationGoal{Check: "test -f done", MaxRounds: 3})
	now := time.Now().UTC()
	// it was in its round 3 of 3 when office went down
	j, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: a.ID, Trigger: "schedule",
		Status: "running", StartedAt: &now, ConversationID: "cnv_goal", GoalRounds: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = st.Jobs().FailRunning(ctx, "restart", "office khởi động lại", now)
	if back := trigger.RequeueCut(ctx, st, []storage.Job{j}, now); len(back) != 1 {
		t.Fatalf("put back: %+v", back)
	}
	r.StartReady(ctx, now)
	r.Wait()
	if len(ex.prompts) != 1 {
		t.Fatalf("a run in its last round runs that round only: %d turns %q", len(ex.prompts), ex.prompts)
	}
	got, _ := st.Jobs().Get(ctx, j.ID)
	if got.Status != "failed" || got.ErrorCode != "goal_not_met" || !strings.Contains(got.Output, "sau 3 vòng") {
		t.Fatalf("job = %s %s %q", got.Status, got.ErrorCode, got.Output)
	}
}

// Each round a goal starts is kept on its job: a restart goes on from it.
func TestGoalRoundsKept(t *testing.T) {
	st, p := openStore(t)
	ex := &goalExec{dir: p.Path, doneAt: 3}
	r := trigger.New(st, ex)
	a := goalAutomation(t, st, p, storage.AutomationGoal{Check: "test -f done"})
	runOnce(t, r, st, a, "")
	if j := mainJob(t, st, a); j.GoalRounds != 3 || j.Status != "done" {
		t.Fatalf("job: %s rounds %d", j.Status, j.GoalRounds)
	}
}

// A bot's /goal (ADR-271): the message's own goal makes its chat go on until
// the judge agrees, though the bot's rule has none.
func TestChannelMessageGoal(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ex := &goalExec{dir: p.Path, verdicts: []string{"VERDICT: DISAGREE\nchưa xong", "nên làm Y", "VERDICT: AGREE\nxong"}}
	r := trigger.New(st, ex)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "@bot", Source: "discord", Action: "chat", Enabled: true})
	payload, _ := json.Marshal(trigger.ChannelPayload{Message: "sửa test", UserID: "7", ChatID: "c1", ChannelID: "chn_1",
		Goal: &storage.AutomationGoal{Text: "sửa test", Judge: true}})
	if _, _, err := r.Enqueue(ctx, a, "discord", string(payload), "", ""); err != nil {
		t.Fatal(err)
	}
	r.StartReady(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.prompts) != 2 || !strings.Contains(ex.prompts[1], "round 2 of 5") {
		t.Fatalf("prompts = %q", ex.prompts)
	}
	if j := mainJob(t, st, a); !strings.HasPrefix(j.Output, "Mục tiêu: đạt sau 2 vòng") {
		t.Fatalf("job output = %q", j.Output)
	}
}
