package busywork_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/busywork"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/workflow"
)

type turns int

func (t turns) ActiveTurns() int { return int(t) }

func open(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// Every kind of work a restart cuts off is counted, with a name and a link
// (ADR-255): before, only the answers being written were.
func TestCountEveryKind(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/p"})
	c := busywork.Counter{Store: st, Chat: turns(0)}
	if r := c.Count(ctx); r.Total() != 0 || r.Summary() != "" {
		t.Fatalf("empty office = %+v", r)
	}

	if _, err := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: "cnv_w", WorkflowKey: "daily-upgrade", WorkflowName: "Nâng cấp hằng ngày", Status: storage.RunRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: "cnv_d", WorkflowKey: "done", Status: storage.RunDone}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "script", Origin: "automation", OriginID: "aut_1", Status: "running", Title: "Sao lưu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", ConversationID: "cnv_c", Status: "running", Title: "Sửa lỗi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", Status: "done", Title: "xong"}); err != nil {
		t.Fatal(err)
	}
	task, err := st.Tasks().Create(ctx, storage.Task{ProjectID: p.ID, Title: "Viết docs", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID, ModelTier: "fast", State: "running", RunBranch: "burn/x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "Sửa test", Status: "doing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "chưa làm", Status: "queued"}); err != nil {
		t.Fatal(err)
	}

	c.Chat = turns(1)
	r := c.Count(ctx)
	if r.Chats != 1 || len(r.ChatItems) != 1 || r.ChatItems[0].Link != "/projects/"+p.ID+"?tab=chat&c=cnv_c" {
		t.Errorf("chats = %d %+v", r.Chats, r.ChatItems)
	}
	if len(r.Workflows) != 1 || r.Workflows[0].Name != "Nâng cấp hằng ngày" || !strings.Contains(r.Workflows[0].Link, "c=cnv_w") {
		t.Errorf("workflows = %+v", r.Workflows)
	}
	if len(r.Automations) != 1 || r.Automations[0].Link != "/projects/"+p.ID+"/automations/aut_1" {
		t.Errorf("automations = %+v", r.Automations)
	}
	if len(r.Tasks) != 1 || r.Tasks[0].ID != task.ID {
		t.Errorf("tasks = %+v", r.Tasks)
	}
	if len(r.Burn) != 1 || r.Burn[0].Name != "Sửa test" {
		t.Errorf("burn = %+v", r.Burn)
	}
	if r.Total() != 5 {
		t.Errorf("total = %d", r.Total())
	}
	if s := r.Summary(); !strings.Contains(s, "Nâng cấp hằng ngày") || !strings.Contains(s, "1 lượt chat") {
		t.Errorf("summary = %q", s)
	}
}

// A graph of steps that only waits (until a time, on a card a person has not
// decided) and the job waiting for it do not hold an update: the next office
// goes on with them (ADR-256). A step in the middle, a card whose command
// runs, a run whose workflow changed still do.
func TestBusyworkSkipsResumableWait(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/p"})
	src := "---\nkey: w\nname: W\nsteps:\n  - { id: a, type: wait, for: 1h, next: b }\n  - { id: b, type: end }\n---\n"
	w, err := st.Workflows().Create(ctx, storage.Workflow{ProjectID: p.ID, Key: "w", Name: "W", Source: src, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	conv, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Purpose: "workflow_run"})
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: "aut_1", Status: "running", Title: "Nâng cấp"})
	at := time.Now().Add(time.Hour)
	cp := &storage.RunCheckpoint{Running: map[string]string{"a": "wait"}, Marks: map[string]storage.StepMark{"a": {WaitUntil: &at}}, CallerJob: job.ID}
	run, _ := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: conv.ID, WorkflowID: w.ID, WorkflowName: "W",
		BodyHash: workflow.RunHash(src), Status: storage.RunRunning, Checkpoint: cp})
	c := busywork.Counter{Store: st}
	if r := c.Count(ctx); r.Total() != 0 || len(r.Parked) != 1 || r.Parked[0].ID != run.ID {
		t.Fatalf("a waiting run = %+v", r)
	}
	// a step in the middle is work
	run.Checkpoint = &storage.RunCheckpoint{Running: map[string]string{"a": "wait", "c": "code"}, Marks: cp.Marks, CallerJob: job.ID}
	_ = st.WorkflowRuns().Update(ctx, run)
	if r := c.Count(ctx); len(r.Workflows) != 1 || len(r.Automations) != 1 || len(r.Parked) != 0 {
		t.Fatalf("a step running = %+v", r)
	}
	// a card waiting on a person is not, once decided it is
	act, _ := st.Actions().Create(ctx, storage.Action{ProjectID: p.ID, ConversationID: conv.ID, Kind: "workflow_gate", Status: "pending"})
	run.Checkpoint = &storage.RunCheckpoint{Running: map[string]string{"g": "approve"}, Marks: map[string]storage.StepMark{"g": {ActionID: act.ID}}}
	_ = st.WorkflowRuns().Update(ctx, run)
	if r := c.Count(ctx); len(r.Parked) != 1 {
		t.Fatalf("a card waiting = %+v", r)
	}
	act.Status = "done"
	_ = st.Actions().Update(ctx, act)
	if r := c.Count(ctx); len(r.Parked) != 0 || len(r.Workflows) != 1 {
		t.Fatalf("a card decided = %+v", r)
	}
	// a workflow changed since: it will not go on, so it is work
	run.Checkpoint = cp
	_ = st.WorkflowRuns().Update(ctx, run)
	w.Source = strings.Replace(src, "1h", "2h", 1)
	_ = st.Workflows().Update(ctx, w)
	if r := c.Count(ctx); len(r.Parked) != 0 || len(r.Workflows) != 1 {
		t.Fatalf("a changed workflow = %+v", r)
	}
}

// The update page says what a restart only cuts and goes on with (ADR-257):
// a person's answer, an automation's agent run (not a script by default,
// not one put back too often), a Burn piece.
func TestCountSaysWhatGoesOn(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/p"})
	chat, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "kiểm tra", Source: "schedule", Action: "chat", Enabled: true})
	script, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "sao lưu", Source: "schedule", Action: "script", Enabled: true})
	for _, j := range []storage.Job{
		{Kind: "chat_turn", Origin: "automation", OriginID: chat.ID, Title: "agent"},
		{Kind: "chat_turn", Origin: "automation", OriginID: chat.ID, Title: "quá lần", Resumes: storage.MaxJobResumes},
		{Kind: "script", Origin: "automation", OriginID: script.ID, Title: "script"},
		{Kind: "chat_turn", Origin: "user", ConversationID: "cnv_c", Title: "người"},
	} {
		j.ProjectID, j.Status = p.ID, "running"
		if _, err := st.Jobs().Create(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID, State: "running", RunBranch: "burn/x"})
	_, _ = st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "mảnh", Status: "doing"})
	r := busywork.Counter{Store: st}.Count(ctx)
	got := map[string]bool{}
	for _, it := range append(append(r.Automations, r.ChatItems...), r.Burn...) {
		got[it.Name] = it.GoesOn
	}
	want := map[string]bool{"agent": true, "quá lần": false, "script": false, "người": true, "mảnh": true}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s: goes on %v (listed %v), want %v", k, g, ok, v)
		}
	}
}
