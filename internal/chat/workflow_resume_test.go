package chat_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/busywork"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A graph of steps survives a restart (ADR-256).

// until polls cond for up to 15s.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// running waits for the run's checkpoint to have step id running.
func (g wfGroup) runningStep(t *testing.T, id string) storage.WorkflowRun {
	t.Helper()
	var r storage.WorkflowRun
	until(t, "step "+id+" running", func() bool {
		runs, _ := g.f.st.WorkflowRuns().List(g.context, g.f.project.ID, g.conv.ID, 1)
		if len(runs) == 0 {
			return false
		}
		r = runs[0]
		if r.Checkpoint == nil {
			return false
		}
		_, ok := r.Checkpoint.Running[id]
		return ok
	})
	return r
}

// restart is the office going down and the next one up on the same data,
// as cmd/office does it: the next engine goes on with what it can.
func (g wfGroup) restart(t *testing.T, why string) *chat.Engine {
	t.Helper()
	g.engine.Shutdown(2 * time.Second)
	ctx := context.Background()
	if _, err := g.f.st.Jobs().FailRunning(ctx, "restart", "office khởi động lại", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	next := chat.NewEngine(g.f.st, g.f.provs, usage.New(g.f.st, time.UTC))
	keep := next.ResumableRuns(ctx)
	if _, err := g.svc.EndInterrupted(ctx, why, keep); err != nil {
		t.Fatal(err)
	}
	next.ResumeWorkflows(ctx, why, keep)
	return next
}

func (g wfGroup) ended(t *testing.T, id string) storage.WorkflowRun {
	t.Helper()
	var r storage.WorkflowRun
	until(t, "run "+id+" ended", func() bool {
		r, _ = g.f.st.WorkflowRuns().Get(g.context, id)
		return r.Status != storage.RunRunning
	})
	return r
}

// A run office goes down under goes on in the next office from where it
// was: the steps that ended do not run again, a wait waits only for what
// is left of it, the chat that called it gets its output and the job that
// waited for it ends done.
func TestStepsResumeAfterRestart(t *testing.T) {
	g := newWFGroup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := `---
key: qua-dem
name: Qua đêm
roles:
  - { key: viet, name: Người viết, access: analyze }
steps:
  - id: mot
    type: code
    lang: bash
    script: echo x >> "$HOME/.mot"; echo mot
    next: cho
  - id: cho
    type: wait
    for: 2s
    next: hai
  - id: hai
    type: code
    lang: bash
    script: echo hai
    next: xong
  - id: xong
    type: end
    summary: "{{steps.mot.output}}-{{steps.hai.output}}"
---
`
	if _, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"viet": g.dev.ID}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "#qua-dem", nil); err != nil {
		t.Fatal(err)
	}
	var r storage.WorkflowRun
	until(t, "the wait's time saved", func() bool {
		r = g.runningStep(t, "cho")
		return r.Checkpoint.Marks["cho"].WaitUntil != nil
	})
	if r.Checkpoint.Steps["mot"].Output != "mot" || r.Checkpoint.CallerJob == "" {
		t.Fatalf("checkpoint = %+v", r.Checkpoint)
	}
	// the update waiting for idle does not wait for it: it only waits
	if rep := (busywork.Counter{Store: g.f.st, Chat: g.engine}).Count(g.context); rep.Total() != 0 || len(rep.Parked) != 1 {
		t.Fatalf("busy = %+v", rep)
	}
	next := g.restart(t, "cập nhật lên bản mới")
	if got, _ := g.f.st.WorkflowRuns().Get(g.context, r.ID); got.Status != storage.RunRunning && got.Status != storage.RunDone {
		t.Fatalf("after the restart = %s %q", got.Status, got.Error)
	}
	got := g.ended(t, r.ID)
	if got.Status != storage.RunDone || got.Result != "mot-hai" {
		t.Fatalf("run = %s %q %q", got.Status, got.Result, got.Error)
	}
	if took := time.Since(start); took > 3500*time.Millisecond {
		t.Fatalf("the wait started over: %v", took)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".mot")); string(b) != "x\n" {
		t.Fatalf("a step that ended ran again: %q", b)
	}
	if got.Checkpoint == nil || got.Checkpoint.Resumes != 0 {
		t.Fatalf("a wait is not cut: %+v", got.Checkpoint)
	}
	msgs, _ := g.f.st.Chat().ListMessages(g.context, g.conv.ID)
	var told, output bool
	for _, m := range msgs {
		told = told || strings.Contains(m.Content, "chạy tiếp từ bước cho") && strings.Contains(m.Content, "cập nhật lên bản mới")
		output = output || m.Role == "assistant" && m.Content == "mot-hai"
	}
	if !told || !output {
		t.Fatalf("the calling chat: %+v", msgs)
	}
	j, err := g.f.st.Jobs().Get(g.context, got.Checkpoint.CallerJob)
	if err != nil || j.Status != "done" {
		t.Fatalf("caller job = %+v, %v", j, err)
	}
	_ = next
}

// An agent's step cut by the restart runs again in the next office, told
// it was cut (its session keeps what it did); it counts toward the cap.
func TestResumeAgentStepTold(t *testing.T) {
	g := newWFGroup(t)
	os.WriteFile(filepath.Join(g.dir, "sleep-dev"), []byte("3"), 0o644)
	src := `---
key: viet
name: Viết
roles:
  - { key: viet, name: Người viết, access: analyze }
steps:
  - id: nhap
    type: agent
    role: viet
    prompt: "Viết nháp"
    next: xong
  - id: xong
    type: end
    summary: "{{steps.nhap.output}}"
---
`
	if _, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"viet": g.dev.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "#viet", nil); err != nil {
		t.Fatal(err)
	}
	r := g.runningStep(t, "nhap")
	if rep := (busywork.Counter{Store: g.f.st, Chat: g.engine}).Count(g.context); len(rep.Parked) != 0 || len(rep.Workflows) != 1 {
		t.Fatalf("an agent's step is work: %+v", rep)
	}
	time.Sleep(300 * time.Millisecond) // the agent runs
	os.Remove(filepath.Join(g.dir, "sleep-dev"))
	g.restart(t, "")
	got := g.ended(t, r.ID)
	if got.Status != storage.RunDone || got.Result != "dev: ok" || got.Checkpoint.Resumes != 1 {
		t.Fatalf("run = %s %q %q %+v", got.Status, got.Result, got.Error, got.Checkpoint)
	}
	ins, _ := filepath.Glob(filepath.Join(g.dir, "call*.in"))
	told := false
	for _, f := range ins {
		b, _ := os.ReadFile(f)
		told = told || strings.Contains(string(b), "Office restarted while you were on this step") && strings.Contains(string(b), "Viết nháp")
	}
	if !told {
		t.Fatal("the agent was not told its step was cut")
	}
}

// An approval waiting on a person goes on with the same card: the next
// office waits on it, no second card; deciding it then goes on.
func TestResumeApproveReusesCard(t *testing.T) {
	g := newWFGroup(t)
	acts := actions.New(g.f.st, nil)
	g.engine.SetActions(acts)
	src := `---
key: duyet
name: Duyệt
roles:
  - { key: viet, name: Người viết, access: analyze }
steps:
  - id: hoi
    type: approve
    note: "Đăng bài?"
    next: xong
  - id: xong
    type: end
    summary: "đã đăng"
---
`
	if _, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"viet": g.dev.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "#duyet", nil); err != nil {
		t.Fatal(err)
	}
	var r storage.WorkflowRun
	until(t, "the card", func() bool {
		r = g.runningStep(t, "hoi")
		return r.Checkpoint.Marks["hoi"].ActionID != ""
	})
	card := r.Checkpoint.Marks["hoi"].ActionID
	if rep := (busywork.Counter{Store: g.f.st, Chat: g.engine}).Count(g.context); rep.Total() != 0 {
		t.Fatalf("a card waiting on a person is not work: %+v", rep)
	}
	g.engine.Shutdown(2 * time.Second)
	_, _ = g.f.st.Jobs().FailRunning(g.context, "restart", "x", time.Now().UTC())
	next := chat.NewEngine(g.f.st, g.f.provs, usage.New(g.f.st, time.UTC))
	next.SetActions(acts)
	keep := next.ResumableRuns(g.context)
	if !keep[r.ID] {
		t.Fatalf("not resumable: %v", keep)
	}
	if n := next.ResumeWorkflows(g.context, "", keep); n != 1 {
		t.Fatalf("resumed %d", n)
	}
	time.Sleep(300 * time.Millisecond)
	if list, _ := g.f.st.Actions().Pending(g.context, 50); len(list) != 1 || list[0].ID != card {
		t.Fatalf("cards = %d", len(list))
	}
	if _, err := acts.Decide(g.context, card, true, "admin"); err != nil {
		t.Fatal(err)
	}
	if got := g.ended(t, r.ID); got.Status != storage.RunDone || got.Result != "đã đăng" {
		t.Fatalf("run = %s %q %q", got.Status, got.Result, got.Error)
	}
}

// A run cut in the middle MaxResumes times, a coordinator's run (no
// checkpoint) and a run whose workflow changed end at the restart.
func TestResumeCappedAndCoordinatorEnded(t *testing.T) {
	g := newWFGroup(t)
	src := `---
key: ngan
name: Ngắn
roles:
  - { key: viet, name: Người viết, access: analyze }
steps:
  - { id: a, type: code, lang: bash, script: echo a, next: b }
  - { id: b, type: end }
---
`
	w, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"viet": g.dev.ID})
	if err != nil {
		t.Fatal(err)
	}
	own, _ := g.f.st.Chat().CreateConversation(g.context, storage.Conversation{ProjectID: g.f.project.ID, Purpose: chat.RunPurpose})
	mk := func(cp *storage.RunCheckpoint, hash string) storage.WorkflowRun {
		r, err := g.f.st.WorkflowRuns().Create(g.context, storage.WorkflowRun{ProjectID: g.f.project.ID, ConversationID: own.ID, CallerConversationID: g.conv.ID,
			WorkflowID: w.ID, WorkflowKey: "ngan", BodyHash: hash, CoordinatorID: g.lead, Status: storage.RunRunning, Checkpoint: cp})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	hash := workflow.RunHash(w.Source)
	capped := mk(&storage.RunCheckpoint{Running: map[string]string{"a": "code"}, Resumes: workflow.MaxResumes}, hash)
	coord := mk(nil, hash)
	changed := mk(&storage.RunCheckpoint{Running: map[string]string{"a": "code"}}, "khac")
	keep := g.engine.ResumableRuns(g.context)
	for _, r := range []storage.WorkflowRun{capped, coord, changed} {
		if keep[r.ID] {
			t.Fatalf("%s should end", r.ID)
		}
	}
	ok := mk(&storage.RunCheckpoint{Running: map[string]string{"a": "code"}, Resumes: workflow.MaxResumes - 1}, hash)
	if !g.engine.ResumableRuns(g.context)[ok.ID] {
		t.Fatal("a run under the cap should go on")
	}
	if _, _, err := workflow.CanResume(g.context, g.f.st, capped); err == nil || !strings.Contains(err.Error(), "3 lần") {
		t.Fatalf("CanResume capped = %v", err)
	}
}

// A run left running that nobody runs (a crash in between) is taken back:
// it goes on when it can, else it ends and its chats are told.
func TestOrphanRunResumedOrEnded(t *testing.T) {
	g := newWFGroup(t)
	src := `---
key: mo-coi
name: Mồ côi
roles:
  - { key: viet, name: Người viết, access: analyze }
steps:
  - { id: a, type: code, lang: bash, script: echo a, next: b }
  - { id: b, type: end, summary: "{{steps.a.output}}" }
---
`
	w, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"viet": g.dev.ID})
	if err != nil {
		t.Fatal(err)
	}
	own, _ := g.f.st.Chat().CreateConversation(g.context, storage.Conversation{ProjectID: g.f.project.ID, Purpose: chat.RunPurpose})
	other, _ := g.f.st.Chat().CreateConversation(g.context, storage.Conversation{ProjectID: g.f.project.ID, Purpose: chat.RunPurpose})
	old := time.Now().UTC().Add(-time.Hour)
	back, _ := g.f.st.WorkflowRuns().Create(g.context, storage.WorkflowRun{ProjectID: g.f.project.ID, ConversationID: own.ID, CallerConversationID: g.conv.ID,
		WorkflowID: w.ID, WorkflowKey: "mo-coi", WorkflowName: "Mồ côi", BodyHash: workflow.RunHash(w.Source), CoordinatorID: g.lead, Status: storage.RunRunning,
		StartedAt: old, Checkpoint: &storage.RunCheckpoint{Pending: []string{"a"}}})
	dead, _ := g.f.st.WorkflowRuns().Create(g.context, storage.WorkflowRun{ProjectID: g.f.project.ID, ConversationID: other.ID, CallerConversationID: g.conv.ID,
		WorkflowKey: "dieu-phoi", WorkflowName: "Điều phối", CoordinatorID: g.lead, Status: storage.RunRunning, StartedAt: old})
	young, _ := g.f.st.WorkflowRuns().Create(g.context, storage.WorkflowRun{ProjectID: g.f.project.ID, ConversationID: "cnv_x", WorkflowKey: "moi", Status: storage.RunRunning})
	if n := g.engine.SweepOrphans(g.context, chat.OrphanAge); n != 0 {
		t.Fatalf("swept on the first sight %d", n) // a run ending now is out of memory a moment before it is saved
	}
	if n := g.engine.SweepOrphans(g.context, chat.OrphanAge); n != 2 {
		t.Fatalf("swept %d", n)
	}
	if got := g.ended(t, back.ID); got.Status != storage.RunDone || got.Result != "a" {
		t.Fatalf("taken back = %s %q %q", got.Status, got.Result, got.Error)
	}
	if got, _ := g.f.st.WorkflowRuns().Get(g.context, dead.ID); got.Status != storage.RunFailed || !strings.Contains(got.Error, "không còn ai giữ") {
		t.Fatalf("ended = %s %q", got.Status, got.Error)
	}
	if got, _ := g.f.st.WorkflowRuns().Get(g.context, young.ID); got.Status != storage.RunRunning {
		t.Fatalf("a young run = %s", got.Status)
	}
	if n := g.engine.SweepOrphans(g.context, chat.OrphanAge); n != 0 {
		t.Fatalf("swept again %d", n)
	}
}

// The dashboard's "Chạy tiếp từ bước dở": a run the restart ended that kept
// where it was goes on; another run does not.
func TestResumeRunByHand(t *testing.T) {
	g := newWFGroup(t)
	src := `---
key: tay
name: Bằng tay
roles:
  - { key: viet, name: Người viết, access: analyze }
steps:
  - { id: a, type: code, lang: bash, script: echo a, next: b }
  - { id: b, type: code, lang: bash, script: echo b, next: c }
  - { id: c, type: end, summary: "{{steps.a.output}}{{steps.b.output}}" }
---
`
	w, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"viet": g.dev.ID})
	if err != nil {
		t.Fatal(err)
	}
	own, _ := g.f.st.Chat().CreateConversation(g.context, storage.Conversation{ProjectID: g.f.project.ID, Purpose: chat.RunPurpose})
	r, _ := g.f.st.WorkflowRuns().Create(g.context, storage.WorkflowRun{ProjectID: g.f.project.ID, ConversationID: own.ID, CallerConversationID: g.conv.ID,
		WorkflowID: w.ID, WorkflowKey: "tay", WorkflowName: "Bằng tay", BodyHash: workflow.RunHash(w.Source), CoordinatorID: g.lead, Status: storage.RunFailed,
		Error:      workflow.InterruptedDetail + ": cập nhật",
		Checkpoint: &storage.RunCheckpoint{Steps: map[string]storage.StepState{"a": {Output: "A"}}, Running: map[string]string{"b": "code"}, Visits: map[string]int{"a": 1, "b": 1}, N: 2}})
	if err := g.engine.ResumeRun(g.context, r.ID); err != nil {
		t.Fatal(err)
	}
	if got := g.ended(t, r.ID); got.Status != storage.RunDone || got.Result != "Ab" {
		t.Fatalf("run = %s %q %q", got.Status, got.Result, got.Error)
	}
	if err := g.engine.ResumeRun(g.context, r.ID); err == nil {
		t.Fatal("a done run went on again")
	}
}
