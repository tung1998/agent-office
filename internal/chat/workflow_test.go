package chat_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// wfGroup is a group chat with a third agent (QA) and the shipped workflows
// in a library.
type wfGroup struct {
	group
	qa  storage.Agent
	svc *workflow.Service
}

func newWFGroup(t *testing.T) wfGroup {
	t.Helper()
	g := newGroup(t)
	qa, err := g.f.st.Agents().Create(g.context, storage.Agent{ProjectID: g.f.project.ID, Key: "qa", Name: "QA", ModelTier: "fast"})
	if err != nil {
		t.Fatal(err)
	}
	lib := workflow.Library{Dir: filepath.Join(t.TempDir(), "workflows")}
	return wfGroup{group: g, qa: qa, svc: &workflow.Service{Store: g.f.st, Lib: lib}}
}

func (g wfGroup) install(t *testing.T, key string, bindings map[string]string) storage.Workflow {
	t.Helper()
	w, err := g.svc.Install(g.context, g.f.project.ID, key, bindings)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// own is the chat the workflow called from g.conv runs in.
func (g wfGroup) own(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if runs, _ := g.f.st.WorkflowRuns().List(g.context, g.f.project.ID, g.conv.ID, 1); len(runs) > 0 && runs[0].ConversationID != g.conv.ID {
			return runs[0].ConversationID
		}
		if time.Now().After(deadline) {
			t.Fatal("no run")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitIn waits for n answers in a chat with nothing running there.
func (g wfGroup) waitIn(t *testing.T, conv string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		msgs, _ := g.f.st.Chat().ListMessages(g.context, conv)
		var out []string
		for _, m := range msgs {
			switch m.Role {
			case "assistant":
				out = append(out, m.Author)
			case "error":
				out = append(out, "!")
			}
		}
		_, busy := g.engine.Active(conv)
		if len(out) >= n && !busy && len(g.engine.Running(conv)) == 0 {
			return out
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %v", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// during calls a workflow tool from inside the coordinator's answer in
// progress, in the run's own chat.
func (g wfGroup) during(t *testing.T, name string, args any) (string, error) {
	t.Helper()
	return g.duringIn(t, g.own(t), name, args)
}

// duringIn is during in a given run's chat (a sub-workflow's).
func (g wfGroup) duringIn(t *testing.T, own, name string, args any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if turn, ok := g.engine.Active(own); ok {
			sc := officetools.Scope{ProjectID: g.f.project.ID, ConversationID: own, RunRef: turn.ID, Agent: g.leadNm}
			if coord, _ := g.engine.WorkflowScope(sc); coord { // its run is set up (the real tools come once it runs)
				return g.engine.WorkflowCall(g.context, sc, name, raw)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no answer in progress")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (g wfGroup) run(t *testing.T) storage.WorkflowRun {
	t.Helper()
	runs, err := g.f.st.WorkflowRuns().List(g.context, g.f.project.ID, g.conv.ID, 1)
	if err != nil || len(runs) == 0 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	return runs[0]
}

func TestWorkflowCommitteeRunsInParallelAndCallsBack(t *testing.T) {
	g := newWFGroup(t)
	g.install(t, "council", map[string]string{"a": g.dev.ID, "b": g.qa.ID})
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	call0, _, err := g.engine.Send(g.context, g.conv.ID, "/council vì sao test chập chờn", nil)
	if err != nil {
		t.Fatal(err)
	}
	own := g.own(t)
	brief := map[string]string{"question": "Vì sao test chập chờn?", "context": "Đã thử tăng timeout"}
	if _, err := g.during(t, "workflow_delegate", map[string]any{"role": "a", "brief": brief, "effort": "high"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.during(t, "workflow_delegate", map[string]any{"role": "b", "brief": brief}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.during(t, "workflow_delegate", map[string]any{"role": "b", "brief": brief}); err == nil {
		t.Fatal("the same role was given out twice in one turn")
	}
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	got := g.waitIn(t, own, 4) // lead, Dev and QA (in either order), lead called back once
	if len(got) != 4 || got[0] != g.leadNm || got[3] != g.leadNm {
		t.Fatalf("authors = %v", got)
	}
	args1, in1 := call(t, g.dir, 1)
	if !strings.Contains(args1, "Running workflow: Hội đồng") || !strings.Contains(in1, "vì sao test chập chờn") {
		t.Fatalf("coordinator's turn:\n%s\n%s", args1, in1)
	}
	var devIn, back string
	for i := 2; i <= 4; i++ {
		a, in := call(t, g.dir, i)
		switch {
		case strings.Contains(a, "You are Dev"):
			devIn = in
			if !strings.Contains(a, "--effort high") {
				t.Fatalf("Dev did not run at the effort the coordinator asked: %s", a)
			}
		case strings.Contains(a, "You are QA") && strings.Contains(a, "--effort"):
			t.Fatalf("QA was given no effort, yet ran with one: %s", a)
		case strings.Contains(in, "[office · workflow Hội đồng]"):
			back = in
		}
	}
	if !strings.Contains(devIn, "Vì sao test chập chờn?") || !strings.Contains(devIn, "Work with your own permission") {
		t.Fatalf("Dev's brief:\n%s", devIn)
	}
	if !strings.Contains(back, "Thành viên A") || !strings.Contains(back, "Thành viên B") {
		t.Fatalf("call-back:\n%s", back)
	}
	r := g.run(t)
	if r.Status != storage.RunRunning || r.Turns != 2 || r.Roles[0].Status != "done" || r.Roles[1].Status != "done" || r.Roles[0].SessionID == "" {
		t.Fatalf("run = %+v", r)
	}
	// a follow-up resumes the role's own session (a coordinator's turn of
	// the run's chat, as office calls it back)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	turn, _, err := g.engine.Send(g.context, own, "hỏi lại A", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.during(t, "workflow_send", map[string]any{"role": "a", "message": "B nói do race, bạn thấy sao?"}); err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	g.waitIn(t, own, 7)
	var resumed bool
	for i := 5; i <= 7; i++ {
		if a, in := call(t, g.dir, i); strings.Contains(a, "You are Dev") && strings.Contains(a, "--resume sess-dev") && strings.Contains(a, "--effort high") && strings.Contains(in, "race") {
			resumed = true
		}
	}
	if !resumed {
		t.Fatal("the follow-up did not resume Dev's session")
	}
	// done
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	turn, _, err = g.engine.Send(g.context, own, "kết thúc đi", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.during(t, "workflow_done", map[string]any{"summary": "Do race.", "outputs": map[string]string{"plan": "sửa race", "agreed": "true"}}); err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	if r := g.run(t); r.Status != storage.RunDone || r.Result != "Do race." || r.Outputs["agreed"] != "true" || r.FinishedAt == nil {
		t.Fatalf("run after done = %+v", r)
	}
	if g.engine.RunningWorkflow(own) != "" {
		t.Fatal("still running")
	}
	// the chat that called it: only the input and the output
	if evs := collect(t, call0); evs[len(evs)-1].Message == nil || !strings.HasPrefix(evs[len(evs)-1].Message.Content, "Do race.") || !strings.Contains(evs[len(evs)-1].Message.Content, "`plan`: sửa race") {
		t.Fatalf("caller's answer = %+v", evs)
	}
	msgs, _ := g.f.st.Chat().ListMessages(g.context, g.conv.ID)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" || !strings.HasPrefix(msgs[1].Content, "Do race.") || msgs[1].Author != g.leadNm {
		t.Fatalf("caller's chat = %+v", msgs)
	}
	if c, _ := g.f.st.Chat().GetConversation(g.context, own); c.Purpose != chat.RunPurpose {
		t.Fatalf("own chat purpose = %q", c.Purpose)
	}
}

func TestWorkflowEnforcesItsRules(t *testing.T) {
	g := newWFGroup(t)
	g.install(t, "feature", map[string]string{"ke-hoach": g.dev.ID, "phan-bien": g.qa.ID, "thuc-thi": g.dev.ID, "review": g.qa.ID})
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("2"), 0o644)
	call0, _, err := g.engine.Send(g.context, g.conv.ID, "/feature đăng nhập bằng Google", nil)
	if err != nil {
		t.Fatal(err)
	}
	own := g.own(t)
	full := map[string]string{"outcome": "Đăng nhập Google", "constraints": "giữ API cũ", "done_when": "test xanh"}
	if _, err := g.during(t, "workflow_delegate", map[string]any{"role": "nope", "brief": full}); err == nil {
		t.Fatal("an unknown role was accepted")
	}
	if _, err := g.during(t, "workflow_delegate", map[string]any{"role": "ke-hoach", "brief": map[string]string{"outcome": "x"}}); err == nil || !strings.Contains(err.Error(), "constraints") {
		t.Fatalf("a brief missing parts: %v", err)
	}
	if _, err := g.during(t, "workflow_send", map[string]any{"role": "ke-hoach", "message": "x"}); err == nil {
		t.Fatal("a follow-up before the role was given out")
	}
	if _, err := g.during(t, "workflow_delegate", map[string]any{"role": "ke-hoach", "brief": full}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.during(t, "workflow_delegate", map[string]any{"role": "phan-bien", "brief": full}); err == nil || !strings.Contains(err.Error(), "parallel") {
		t.Fatalf("two roles not allowed at once: %v", err)
	}
	if _, err := g.during(t, "workflow_done", map[string]any{"summary": "x"}); err == nil || !strings.Contains(err.Error(), "cổng") {
		t.Fatalf("done before the required gates: %v", err)
	}
	if _, err := g.during(t, "workflow_vote", map[string]any{"question": "ok?"}); err == nil {
		t.Fatal("a vote in a workflow without one")
	}
	// the tools are the coordinator's only, and delegate is off in a run
	turn, _ := g.engine.Active(own)
	sc := officetools.Scope{ProjectID: g.f.project.ID, ConversationID: own, RunRef: turn.ID, Agent: g.leadNm}
	if coord, inRun := g.engine.WorkflowScope(sc); !coord || !inRun {
		t.Fatalf("scope = %v %v", coord, inRun)
	}
	if _, err := g.engine.Delegate(g.context, sc, "Dev", "x", ""); err == nil {
		t.Fatal("delegate inside a workflow")
	}
	collect(t, turn)
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	g.waitIn(t, own, 3)
	// another /workflow while it runs is refused
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "/feature lần nữa", nil); err != chat.ErrWorkflowRunning {
		t.Fatalf("second run: %v", err)
	}
	// Dừng in the chat that called it ends it
	g.engine.StopAll(g.conv.ID)
	if evs := collect(t, call0); evs[len(evs)-1].Type != "error" {
		t.Fatalf("caller's answer after stop = %+v", evs)
	}
	if r := g.run(t); r.Status != storage.RunStopped {
		t.Fatalf("after stop = %s", r.Status)
	}
}

func TestWorkflowVoteTallies(t *testing.T) {
	g := newWFGroup(t)
	// the 3-party council: the coordinator is the lead, its three roles Dev, QA and a third agent
	aud, _ := g.f.st.Agents().Create(g.context, storage.Agent{ProjectID: g.f.project.ID, Key: "aud", Name: "Aud", ModelTier: "fast"})
	g.install(t, "council-3", map[string]string{"lap-ke-hoach": g.dev.ID, "thuc-thi": g.qa.ID, "giam-sat": aud.ID})
	os.WriteFile(filepath.Join(g.dir, "reply-dev"), []byte("VOTE: AGREE"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "/council-3 làm X", nil); err != nil {
		t.Fatal(err)
	}
	own := g.own(t)
	if _, err := g.during(t, "workflow_vote", map[string]any{"question": "Thông qua kế hoạch X?"}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	g.waitIn(t, own, 5)
	// QA and Aud answer "lead: ok" (no ballot): 1 yes of 3, the auditor's unclear vote vetoes
	var back string
	for i := 2; i <= 5; i++ {
		if _, in := call(t, g.dir, i); strings.Contains(in, "Vote result") {
			back = in
		}
	}
	if !strings.Contains(back, "1/3 agree") || !strings.Contains(back, "NOT PASSED") || !strings.Contains(back, "Vetoed") {
		t.Fatalf("call-back:\n%s", back)
	}
}

// a role filled by another workflow: it runs in a chat of its own, its
// output is the role's answer in the run that called it (ADR-102)
func TestWorkflowCallsASubWorkflow(t *testing.T) {
	g := newWFGroup(t)
	g.install(t, "advisor", map[string]string{"co-van": g.dev.ID})
	parent := "---\nkey: cha\nname: Cha\ndescription: gọi con\nroles:\n  - key: con\n    name: Con\n    workflow: advisor\nlimits:\n  depth: 1\n---\nGiao vai con rồi tổng kết.\n"
	if _, err := g.svc.Create(g.context, g.f.project.ID, parent, map[string]string{"con": g.lead}); err != nil { // the coordinator runs the sub-workflow too
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("3"), 0o644)
	call0, _, err := g.engine.Send(g.context, g.conv.ID, "/cha nên làm gì", nil)
	if err != nil {
		t.Fatal(err)
	}
	own := g.own(t)
	if _, err := g.duringIn(t, own, "workflow_delegate", map[string]any{"role": "con", "brief": map[string]string{"question": "Q?"}}); err != nil {
		t.Fatal(err)
	}
	// the sub-workflow's own chat, called from the parent's
	var child storage.WorkflowRun
	deadline := time.Now().Add(10 * time.Second)
	for child.ID == "" {
		runs, _ := g.f.st.WorkflowRuns().List(g.context, g.f.project.ID, own, 5)
		for _, r := range runs {
			if r.CallerConversationID == own {
				child = r
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no sub-workflow run")
		}
		time.Sleep(20 * time.Millisecond)
	}
	parentRun := g.run(t)
	if child.ParentRunID != parentRun.ID || child.Depth != 1 || child.WorkflowKey != "advisor" || !strings.Contains(child.Input, "Q?") {
		t.Fatalf("child = %+v", child)
	}
	// limits.depth 1: the child cannot go deeper (advisor has no sub-workflow anyway)
	if _, err := g.duringIn(t, child.ConversationID, "workflow_done", map[string]any{"summary": "Ý kiến con", "outputs": map[string]string{"recommendation": "làm A"}}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	deadline = time.Now().Add(10 * time.Second)
	for {
		r := g.run(t)
		if len(r.Roles) == 1 && r.Roles[0].Status == "done" && strings.HasPrefix(r.Roles[0].Result, "Ý kiến con") && r.Roles[0].RunID == child.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("parent run = %+v", r)
		}
		time.Sleep(50 * time.Millisecond)
	}
	msgs, _ := g.f.st.Chat().ListMessages(g.context, own)
	if !slices.ContainsFunc(msgs, func(m storage.Message) bool {
		return strings.Contains(m.Content, "Quy trình con /advisor") && strings.Contains(m.Content, "Ý kiến con")
	}) {
		t.Fatalf("parent's chat = %+v", msgs)
	}
	g.engine.StopAll(g.conv.ID)
	collect(t, call0)
}

// declared inputs and outputs, and a workflow only other workflows call (ADR-103)
func TestSubWorkflowInputsOutputs(t *testing.T) {
	g := newWFGroup(t)
	child := "---\nkey: con-ra\nname: Con\ncallable: sub\ninputs:\n  - key: cau-hoi\n    required: true\noutputs:\n  - key: ket-luan\n    required: true\nroles:\n  - key: x\n---\nTrả lời câu hỏi.\n"
	parent := "---\nkey: cha2\nname: Cha\nroles:\n  - key: con\n    workflow: con-ra\n---\nGiao vai con.\n"
	if _, err := g.svc.Create(g.context, g.f.project.ID, child, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := g.svc.Create(g.context, g.f.project.ID, parent, map[string]string{"con": g.lead}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "/con-ra thử", nil); err == nil || !strings.Contains(err.Error(), "callable") {
		t.Fatalf("a sub-only workflow ran from the chat: %v", err)
	}
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("3"), 0o644)
	defer os.Remove(filepath.Join(g.dir, "sleep-lead"))
	call0, _, err := g.engine.Send(g.context, g.conv.ID, "/cha2 làm đi", nil)
	if err != nil {
		t.Fatal(err)
	}
	own := g.own(t)
	if _, err := g.duringIn(t, own, "workflow_delegate", map[string]any{"role": "con", "brief": map[string]string{}}); err == nil || !strings.Contains(err.Error(), "cau-hoi") {
		t.Fatalf("a missing input: %v", err)
	}
	if _, err := g.duringIn(t, own, "workflow_delegate", map[string]any{"role": "con", "brief": map[string]string{"cau-hoi": "Q?"}}); err != nil {
		t.Fatal(err)
	}
	var sub storage.WorkflowRun
	deadline := time.Now().Add(10 * time.Second)
	for sub.ID == "" {
		runs, _ := g.f.st.WorkflowRuns().List(g.context, g.f.project.ID, own, 5)
		for _, r := range runs {
			if r.CallerConversationID == own {
				sub = r
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no sub-run")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(sub.Input, "Q?") {
		t.Fatalf("sub input = %q", sub.Input)
	}
	if _, err := g.duringIn(t, sub.ConversationID, "workflow_done", map[string]any{"summary": "xong"}); err == nil || !strings.Contains(err.Error(), "ket-luan") {
		t.Fatalf("done without its outputs: %v", err)
	}
	if _, err := g.duringIn(t, sub.ConversationID, "workflow_done", map[string]any{"summary": "xong", "outputs": map[string]string{"ket-luan": "A"}}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		r := g.run(t)
		if r.Roles[0].Status == "done" && strings.Contains(r.Roles[0].Result, "`ket-luan`: A") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("parent run = %+v", r)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got, _ := g.f.st.WorkflowRuns().Get(g.context, sub.ID); got.Outputs["ket-luan"] != "A" {
		t.Fatalf("outputs = %v", got.Outputs)
	}
	g.engine.StopAll(g.conv.ID)
	collect(t, call0)
}

// a graph of steps runs in order without a coordinator: code, a condition
// that loops back, an HTTP request, an agent, the end (ADR-108)
func TestWorkflowStepsRunInOrder(t *testing.T) {
	g := newWFGroup(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		body, _ := io.ReadAll(r.Body)
		w.Write([]byte(`{"ok":true,"got":` + strconv.Quote(string(body)) + `}`))
	}))
	defer srv.Close()
	os.WriteFile(filepath.Join(g.dir, "reply-dev"), []byte("Tóm tắt: ổn"), 0o644)
	src := `---
key: buoc
name: Theo bước
inputs:
  - { key: ten, required: true }
outputs:
  - { key: ket-qua, required: true }
  - { key: so-lan, type: number, required: true }
roles:
  - { key: viet, name: Người viết, access: analyze }
steps:
  - id: dem
    type: code
    lang: bash
    script: |
      n=$(cat "$HOME/.dem" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$HOME/.dem"
      echo "{\"n\": $n, \"ten\": \"$OFFICE_INPUT_TEN\"}"
    next: du-chua
  - id: du-chua
    type: condition
    if: "{{steps.dem.json.n}} >= 2"
    then: goi
    else: dem
  - id: goi
    type: http
    method: POST
    url: ` + srv.URL + `
    body: '{"ten": "{{steps.dem.json.ten}}"}'
    next: tom-tat
  - id: tom-tat
    type: agent
    role: viet
    prompt: "Tóm tắt kết quả: {{steps.goi.output}}"
    next: xong
  - id: xong
    type: end
    summary: "{{steps.tom-tat.output}}"
    outputs:
      ket-qua: "{{steps.goi.json.ok}}"
      so-lan: "{{steps.dem.json.n}}"
---
`
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"viet": g.dev.ID}); err != nil {
		t.Fatal(err)
	}
	call0, _, err := g.engine.Send(g.context, g.conv.ID, "#buoc An", nil) // "#key": a workflow (ADR-109)
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, call0)
	last := evs[len(evs)-1]
	if last.Message == nil || !strings.Contains(last.Message.Content, "Tóm tắt: ổn") || !strings.Contains(last.Message.Content, "`so-lan`: 2") || !strings.Contains(last.Message.Content, "`ket-qua`: true") {
		t.Fatalf("caller's answer = %+v", last)
	}
	r := g.run(t)
	if r.Status != storage.RunDone || hits != 1 || r.Outputs["so-lan"] != "2" {
		t.Fatalf("run = %+v, hits %d", r, hits)
	}
	// the run's chat holds each step: the code twice, the request, the agent's answer
	msgs, _ := g.f.st.Chat().ListMessages(g.context, r.ConversationID)
	var code, req, dev int
	for _, m := range msgs {
		switch {
		case strings.Contains(m.Content, "dem (code)"):
			code++
		case strings.Contains(m.Content, "goi (http)") && strings.Contains(m.Content, `\"ten\": \"An\"`):
			req++
		case m.Author == "Dev":
			dev++
		}
	}
	if code != 2 || req != 1 || dev != 1 {
		t.Fatalf("run's chat: code %d, request %d, dev %d\n%+v", code, req, dev, msgs)
	}
}

// one output goes to several steps at once; a join waits for both
// branches and runs once; a switch takes the case its value matches; a
// failure is an output for the steps after it (on_error: continue)
func TestWorkflowStepsBranchAndJoin(t *testing.T) {
	g := newWFGroup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := `---
key: nhanh
name: Nhánh
inputs:
  - { key: a, required: true }
  - { key: b }
outputs:
  - { key: gom, required: true }
  - { key: loai, required: true }
  - { key: loi }
roles:
  - { key: viet, name: Người viết, access: analyze }
start: [mot, hai]
steps:
  - id: mot
    type: code
    lang: bash
    script: sleep 1; echo "1-$OFFICE_INPUT_A"
    next: gom
  - id: hai
    type: code
    lang: bash
    script: sleep 1; echo "2-$OFFICE_INPUT_B"
    next: [gom, hong]
  - id: hong
    type: code
    lang: bash
    script: echo boom; exit 3
    on_error: continue
    next: gom
  - id: gom
    type: code
    lang: bash
    script: echo "{\"kind\":\"bug\"}"; echo x >> "$HOME/.gom"
    next: chon
  - id: chon
    type: switch
    value: "{{steps.gom.json.kind}}"
    cases:
      - { when: feature, next: sai }
      - { when: BUG, next: xong }
    else: sai
  - id: sai
    type: end
    outputs: { gom: sai, loai: sai }
  - id: xong
    type: end
    summary: "{{steps.mot.output}} {{steps.hai.output}}"
    outputs:
      gom: "{{steps.mot.output}}+{{steps.hai.output}}"
      loai: "{{steps.chon.status}}"
      loi: "{{steps.hong.status}}:{{steps.hong.output}}"
---
`
	if _, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"viet": g.dev.ID}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	call0, _, err := g.engine.Send(g.context, g.conv.ID, "#nhanh {\"a\": \"x\", \"b\": \"y\"}", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, call0)
	r := g.run(t)
	if r.Status != storage.RunDone || r.Outputs["gom"] != "1-x+2-y" || r.Outputs["loai"] != "bug" || r.Outputs["loi"] != "3:boom" {
		t.Fatalf("run = %+v", r)
	}
	if took := time.Since(start); took > 1900*time.Millisecond {
		t.Fatalf("the two branches did not run at once: %v", took)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".gom")); string(b) != "x\n" {
		t.Fatalf("the join ran %q", b)
	}
}

// a wait step (ADR-237): check, wait, check again, as a check after a
// deploy at 30 minutes then an hour; no agent runs
func TestWorkflowWaitStepLoops(t *testing.T) {
	g := newWFGroup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := `---
key: cho
name: Chờ
roles:
  - { key: viet, name: Người viết, access: analyze }
steps:
  - id: kiem
    type: code
    lang: bash
    script: echo x >> "$HOME/.lan"; wc -l < "$HOME/.lan" | tr -d ' '
    next: du
  - id: du
    type: condition
    if: "{{steps.kiem.output}} >= 3"
    then: xong
    else: cho
  - id: cho
    type: wait
    for: 300ms
    next: kiem
  - id: xong
    type: end
    summary: "lần {{steps.kiem.output}}, chờ {{steps.cho.output}}"
---
`
	if _, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"viet": g.dev.ID}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	call0, _, err := g.engine.Send(g.context, g.conv.ID, "#cho", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, call0)
	r := g.run(t)
	if r.Status != storage.RunDone || r.Result != "lần 3, chờ 300ms" {
		t.Fatalf("run = %+v", r)
	}
	if took := time.Since(start); took < 600*time.Millisecond {
		t.Fatalf("it did not wait twice: %v", took)
	}
}

// a coordinate step (ADR-111): an agent decides inside the step, in a run
// of its own; its workflow_done is the step's output for the steps after it
func TestWorkflowCoordinateStep(t *testing.T) {
	g := newWFGroup(t)
	src := `---
key: dp
name: Điều phối trong bước
inputs:
  - { key: spec, required: true }
outputs:
  - { key: ket-luan, required: true }
roles:
  - { key: a, name: A, access: analyze }
  - { key: b, name: B, access: analyze }
steps:
  - id: review
    type: coordinate
    roles: [a]
    prompt: "Giao a review {{input.spec}}; kết luận dong-y | can-sua."
    next: chon
  - id: chon
    type: switch
    value: "{{steps.review.json.ket-luan}}"
    cases:
      - { when: dong-y, next: xong }
    else: sai
  - { id: sai, type: end, outputs: { ket-luan: sai } }
  - id: xong
    type: end
    summary: "{{steps.review.output}}"
    outputs: { ket-luan: "{{steps.chon.status}}" }
---
`
	if _, err := g.svc.Create(g.context, g.f.project.ID, src, map[string]string{"a": g.dev.ID, "b": g.qa.ID}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("3"), 0o644)
	call0, _, err := g.engine.Send(g.context, g.conv.ID, "#dp đặc tả X", nil)
	if err != nil {
		t.Fatal(err)
	}
	own := g.own(t)
	var child storage.WorkflowRun
	deadline := time.Now().Add(10 * time.Second)
	for child.ID == "" {
		runs, _ := g.f.st.WorkflowRuns().List(g.context, g.f.project.ID, own, 5)
		for _, r := range runs {
			if r.CallerConversationID == own {
				child = r
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no coordinate run")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(child.Roles) != 1 || child.Roles[0].Role != "a" || child.Depth != 1 || !strings.Contains(child.WorkflowName, "review") {
		t.Fatalf("child = %+v", child)
	}
	if _, err := g.duringIn(t, child.ConversationID, "workflow_done", map[string]any{"summary": "A thấy ổn", "outputs": map[string]string{"ket-luan": "dong-y"}}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	collect(t, call0)
	r := g.run(t)
	if r.Status != storage.RunDone || r.Outputs["ket-luan"] != "dong-y" || !strings.Contains(r.Result, "A thấy ổn") {
		t.Fatalf("run = %+v", r)
	}
}

// "#key" in a Burn piece's review chat (ADR-113, ADR-114) runs the workflow, as in
// a team chat, and its answer is the workflow's output: not a plain message.
func TestWorkflowRunsFromABurnChat(t *testing.T) {
	g := newWFGroup(t)
	src := `---
key: review-burn
name: Review Burn
inputs:
  - { key: yeu-cau, required: true }
steps:
  - id: xem
    type: code
    lang: bash
    script: 'echo "KẾT LUẬN: ĐỒNG Ý"'
    next: xong
  - id: xong
    type: end
    summary: "{{steps.xem.output}}"
---
`
	if _, err := g.svc.Create(g.context, g.f.project.ID, src, nil); err != nil {
		t.Fatal(err)
	}
	conv, err := g.engine.StartConversationPurpose(g.context, g.f.project.ID, "", chat.BurnReviewPurpose)
	if err != nil {
		t.Fatal(err)
	}
	turn, _, err := g.engine.Send(g.context, conv.ID, "#review-burn [Burn · Review kết quả] việc x", nil)
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, turn)
	last := evs[len(evs)-1]
	if last.Message == nil || !strings.Contains(last.Message.Content, "KẾT LUẬN: ĐỒNG Ý") {
		t.Fatalf("answer = %+v", last)
	}
	runs, _ := g.f.st.WorkflowRuns().List(g.context, g.f.project.ID, conv.ID, 1)
	if len(runs) != 1 || runs[0].Status != storage.RunDone {
		t.Fatalf("runs = %+v", runs)
	}
}

// An agent runs a workflow itself (run_workflow): "#key input" waits in its
// chat as the person and goes when the answer ends, then runs as if typed.
func TestAgentStartsWorkflowItself(t *testing.T) {
	g := newWFGroup(t)
	g.install(t, "council", map[string]string{"a": g.dev.ID, "b": g.qa.ID})
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("1"), 0o644)
	if _, _, err := g.engine.Send(g.context, g.conv.ID, "test này chập chờn, xem giúp", nil); err != nil {
		t.Fatal(err)
	}
	turn, ok := g.engine.Active(g.conv.ID)
	if !ok {
		t.Fatal("no answer in progress")
	}
	sc := officetools.Scope{ProjectID: g.f.project.ID, ConversationID: g.conv.ID, RunRef: turn.ID, Agent: g.leadNm}
	if _, err := g.engine.StartWorkflow(g.context, sc, "nope", "x"); err == nil || !strings.Contains(err.Error(), "#council") {
		t.Fatalf("unknown key: %v", err)
	}
	if _, err := g.engine.StartWorkflow(g.context, sc, "#council", "vì sao test chập chờn"); err != nil {
		t.Fatal(err)
	}
	if runs, _ := g.f.st.WorkflowRuns().List(g.context, g.f.project.ID, g.conv.ID, 1); len(runs) != 0 {
		t.Fatal("the run started before the answer ended")
	}
	if !g.engine.QueuedWorkflow(g.context, g.conv.ID) { // an automation waits for it before it notifies
		t.Fatal("the call does not wait in the chat")
	}
	os.Remove(filepath.Join(g.dir, "sleep-lead"))
	g.own(t)
	if r := g.run(t); r.WorkflowKey != "council" || r.Input != "vì sao test chập chờn" {
		t.Fatalf("run = %+v", r)
	}
	g.engine.StopAll(g.conv.ID)
}

// A run that ends while a role still answers leaves no role "working".
func TestWorkflowEndsItsWorkingRoles(t *testing.T) {
	g := newWFGroup(t)
	g.install(t, "feature", map[string]string{"ke-hoach": g.dev.ID, "phan-bien": g.qa.ID, "thuc-thi": g.dev.ID, "review": g.qa.ID})
	os.WriteFile(filepath.Join(g.dir, "sleep-lead"), []byte("2"), 0o644)
	os.WriteFile(filepath.Join(g.dir, "sleep-dev"), []byte("30"), 0o644)
	call, _, err := g.engine.Send(g.context, g.conv.ID, "/feature đăng nhập bằng Google", nil)
	if err != nil {
		t.Fatal(err)
	}
	full := map[string]string{"outcome": "Đăng nhập Google", "constraints": "giữ API cũ", "done_when": "test xanh"}
	if _, err := g.during(t, "workflow_delegate", map[string]any{"role": "ke-hoach", "brief": full}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !slices.ContainsFunc(g.run(t).Roles, func(r storage.RunRole) bool { return r.Status == "working" }) {
		if time.Now().After(deadline) {
			t.Fatal("the role never started")
		}
		time.Sleep(50 * time.Millisecond)
	}
	g.engine.StopAll(g.conv.ID)
	collect(t, call)
	for _, r := range g.run(t).Roles {
		if r.Status == "working" {
			t.Fatalf("role %s still working after the run ended", r.Name)
		}
	}
}
