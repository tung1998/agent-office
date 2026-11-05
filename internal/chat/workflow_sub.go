package chat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// Sub-workflows (ADR-102): a role filled by another workflow of the project
// (its own key too: recursion, as deep as limits.depth lets it). Giving it
// work runs that workflow in a chat of its own, as a chat calling it would:
// the brief is its input; its output comes back as the role's answer. The
// agent bound to the role coordinates it (none: this run's coordinator).

// wfDelegateFlow gives a sub-workflow role its first input.
func (e *Engine) wfDelegateFlow(ctx context.Context, sc officetools.Scope, run *wfRun, d workflow.Role, agentName string, brief map[string]string) (string, error) {
	coord, err := e.flowCoordinator(ctx, run, d.Key, agentName)
	if err != nil {
		return "", err
	}
	// the sub-workflow's declared inputs, by key; else the brief this one asks
	var input string
	if w, err := e.store.Workflows().GetByKey(ctx, run.rec.ProjectID, d.Workflow); err == nil {
		if cd, err := workflow.Parse(w.Source); err == nil && len(cd.Inputs) > 0 {
			if input, err = cd.RenderInputs(brief); err != nil {
				return "", fmt.Errorf("%w; đầu vào của /%s: %s", err, cd.Key, fieldList(cd.Inputs))
			}
		}
	}
	if input == "" {
		if input, err = run.def.RenderBrief(d, brief); err != nil {
			return "", err
		}
	}
	return e.wfAskFlow(ctx, sc, run, d, coord.ID, input, brief, false)
}

// flowCoordinator: the agent named, else the one bound to the role, else
// this run's coordinator.
func (e *Engine) flowCoordinator(ctx context.Context, run *wfRun, role, name string) (storage.Agent, error) {
	e.wf.mu.Lock()
	bound := ""
	if r := run.role(role); r != nil {
		bound = r.AgentID
	}
	e.wf.mu.Unlock()
	if strings.TrimSpace(name) == "" && bound == "" {
		return run.coord, nil
	}
	agents, err := e.Agents(ctx, run.rec.ProjectID)
	if err != nil {
		return storage.Agent{}, err
	}
	name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "@")))
	i := slices.IndexFunc(agents, func(x storage.Agent) bool {
		if name != "" {
			return strings.ToLower(x.Name) == name || strings.ToLower(x.Key) == name
		}
		return x.ID == bound
	})
	if i < 0 {
		return storage.Agent{}, fmt.Errorf("không có agent %q trong project", cmp.Or(name, bound))
	}
	if agents[i].Disabled {
		return storage.Agent{}, errors.New(storage.OffNotice(agents[i].Name))
	}
	return agents[i], nil
}

// wfAskFlow queues a run of a sub-workflow role; again: a follow-up (a round).
func (e *Engine) wfAskFlow(ctx context.Context, sc officetools.Scope, run *wfRun, d workflow.Role, agentID, input string, inputs map[string]string, again bool) (string, error) {
	if run.depthLeft <= 0 {
		return "", fmt.Errorf("vai %s là quy trình con /%s nhưng đã tới giới hạn lồng (limits.depth); tự làm phần này hoặc tổng kết", d.Name, d.Workflow)
	}
	w, err := e.store.Workflows().GetByKey(ctx, run.rec.ProjectID, d.Workflow)
	if err != nil {
		return "", fmt.Errorf("project chưa cài quy trình /%s (vai %s)", d.Workflow, d.Name)
	}
	if cd, err := workflow.Parse(w.Source); err == nil && cd.Callable == workflow.CallableChat {
		return "", fmt.Errorf("quy trình /%s chỉ chạy từ chat (callable: chat), không làm quy trình con được", d.Workflow)
	}
	a, err := e.store.Agents().Get(ctx, agentID)
	if err != nil {
		return "", err
	}
	e.wf.mu.Lock()
	r := run.role(d.Key)
	if r.Status == "working" {
		e.wf.mu.Unlock()
		return "", fmt.Errorf("quy trình con của vai %s đang chạy; đợi xong", d.Name)
	}
	if again && r.Rounds >= run.def.Limits.Rounds {
		e.wf.mu.Unlock()
		return "", fmt.Errorf("đã chạy lại vai %s %d lần (giới hạn); tổng kết với kết quả hiện có", d.Name, r.Rounds)
	}
	if err := e.canAsk(run, sc.RunRef, d.Key, a); err != nil {
		e.wf.mu.Unlock()
		return "", err
	}
	if again {
		r.Rounds++
	}
	r.AgentID, r.AgentName = a.ID, a.Name
	e.wf.pending[sc.RunRef] = append(e.wf.pending[sc.RunRef], wfAsk{role: d.Key, agent: a, prompt: input, flow: true, inputs: inputs})
	run.logf("Giao vai %s: quy trình con /%s, %s điều phối", d.Name, d.Workflow, a.Name)
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	return fmt.Sprintf("Delegated role %s: the sub-workflow /%s (coordinated by %s) runs in a chat of its own when you finish this turn. Write a short note and stop your turn; when it is done, its output becomes a message in this chat and you are called back.", d.Name, d.Workflow, a.Name), nil
}

// startChild starts a sub-workflow run a coordinator asked for.
func (e *Engine) startChild(run *wfRun, conv storage.Conversation, project storage.Repo, a wfAsk) bool {
	ctx := context.Background()
	d, _ := run.def.Role(a.role)
	failed := func(why string) bool {
		e.wf.mu.Lock()
		if r := run.role(a.role); r != nil {
			r.Status = "failed"
		}
		run.notes = append(run.notes, fmt.Sprintf("Role %s (sub-workflow /%s) could not run: %s", d.Name, d.Workflow, why))
		run.logf("%s: quy trình con không chạy được", d.Name)
		rec, seq := run.snapshot()
		e.wf.mu.Unlock()
		e.saveRun(run, rec, seq)
		return false
	}
	w, err := e.store.Workflows().GetByKey(ctx, project.ID, d.Workflow)
	if err != nil {
		return failed("project chưa cài quy trình này")
	}
	def, err := workflow.Parse(w.Source)
	if err != nil {
		return failed(err.Error())
	}
	child, prompt, err := e.newRun(ctx, project.ID, w, def, a.agent, a.prompt)
	if err != nil {
		return failed(err.Error())
	}
	if def.Callable == workflow.CallableChat {
		return failed("quy trình này chỉ chạy từ chat (callable: chat)")
	}
	child.depthLeft, child.until = min(run.depthLeft-1, def.Limits.Depth), run.deadline
	child.parent, child.parentRole = run, a.role
	child.trusted = run.trusted
	child.rec.CallerConversationID, child.rec.ParentRunID, child.rec.Depth = conv.ID, run.rec.ID, run.rec.Depth+1
	own, err := e.store.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: project.ID, AgentID: a.agent.ID, AgentName: a.agent.Name, CreatedBy: run.rec.Actor,
		Purpose: RunPurpose, Title: truncate(def.Name+": "+oneLine(a.prompt), 80), Mode: conv.Mode, EditMode: conv.EditMode})
	if err != nil {
		return failed(err.Error())
	}
	e.wf.mu.Lock()
	r := run.role(a.role)
	r.Status = "working"
	run.batch[a.role] = true
	run.rec.Turns++
	r.Turns++
	e.wf.mu.Unlock()
	// as the person who started the top run, no higher than this role's access lets it
	level := accessLevel(d.Access)
	if run.ceiling != "" {
		level = perm.Min(level, run.ceiling)
	}
	sctx := WithCeiling(WithModelTier(withTreeOpt(actor.With(ctx, run.rec.Actor), run.tree), run.tier), level)
	child.inputs = a.inputs
	if err := e.launch(sctx, own, child, "/"+def.Key+" "+a.prompt, prompt, "", nil); err != nil {
		_ = e.store.Chat().DeleteConversation(ctx, own.ID)
		e.wf.mu.Lock()
		delete(run.batch, a.role)
		run.rec.Turns--
		r.Turns--
		e.wf.mu.Unlock()
		return failed(err.Error())
	}
	e.wf.mu.Lock()
	r.RunID = child.rec.ID
	run.logf("%s: chạy quy trình con /%s", d.Name, def.Key)
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	e.watchIdle(run, conv, a.role)
	safeGo("awaitChild", func() { e.awaitChild(run.rec.ID, conv, project, a.role, child) })
	return true
}

// awaitChild gives a role its sub-workflow's output once that run ends: a
// message of the run's chat, then the role's answer as any other role's.
func (e *Engine) awaitChild(parentID string, conv storage.Conversation, project storage.Repo, role string, child *wfRun) {
	defer func() {
		// a panic here must still free the role's seat in the parent run,
		// not leave it stuck "running" forever.
		if r := recover(); r != nil {
			slog.Error("chat: awaitChild panic", "panic", r, "stack", string(debug.Stack()))
			e.wf.mu.Lock()
			run := e.wf.byConv[conv.ID]
			if run == nil || run.rec.ID != parentID {
				e.wf.mu.Unlock()
				return
			}
			if rr := run.role(role); rr != nil {
				rr.Status = "failed"
				run.notes = append(run.notes, fmt.Sprintf("Sub-workflow (role %s) panicked: %v", role, r))
			}
			delete(run.batch, role)
			last := len(run.batch) == 0
			id := run.rec.ID
			saved, seq := run.snapshot()
			e.wf.mu.Unlock()
			e.saveRun(run, saved, seq)
			if last {
				e.wfCallBack(id, conv, project)
			}
		}
	}()
	<-child.done
	bg := context.Background()
	rec, err := e.store.WorkflowRuns().Get(bg, child.rec.ID)
	if err != nil {
		rec = child.rec
	}
	head := fmt.Sprintf("**Quy trình con /%s** (vai %s): ", rec.WorkflowKey, role) // i18n-ignore
	msg := storage.Message{ConversationID: conv.ID, Role: "assistant", Author: rec.CoordinatorName, AgentID: rec.CoordinatorID, Content: head + "\n\n" + strings.TrimSpace(rec.Result) + outputsText(rec)}
	if rec.Status != storage.RunDone {
		msg = storage.Message{ConversationID: conv.ID, Role: "error", Content: head + cmp.Or(rec.Error, "đã dừng")}
	}
	_, _ = e.store.Chat().AddMessage(bg, msg)
	e.wf.mu.Lock()
	run := e.wf.byConv[conv.ID]
	if run == nil || run.rec.ID != parentID {
		e.wf.mu.Unlock()
		return // the run that called it ended meanwhile
	}
	r := run.role(role)
	if r == nil {
		e.wf.mu.Unlock()
		return
	}
	// its cost went up the tree as it was spent (wfCost)
	if rec.Status == storage.RunDone {
		r.Status, r.Result = "done", truncate(strings.TrimSpace(rec.Result)+outputsText(rec), 4000)
		run.logf("%s: quy trình con /%s xong", r.Name, rec.WorkflowKey)
	} else {
		r.Status = "failed"
		run.notes = append(run.notes, fmt.Sprintf("Sub-workflow /%s (role %s) did not finish: %s", rec.WorkflowKey, r.Name, cmp.Or(rec.Error, "stopped")))
		run.logf("%s: quy trình con /%s không xong", r.Name, rec.WorkflowKey)
	}
	delete(run.batch, role)
	last := len(run.batch) == 0
	id := run.rec.ID
	saved, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, saved, seq)
	if last {
		e.wfCallBack(id, conv, project)
	}
}

// rootCaller is the chat a run's chat was called from at the top (a run
// called by a run called by a chat: that chat; "" = not a run's chat).
func (e *Engine) rootCaller(conversationID string) string {
	out := ""
	for range workflow.MaxDepth + 2 {
		r := e.runOf(conversationID)
		if r == nil || r.rec.CallerConversationID == "" {
			break
		}
		out, conversationID = r.rec.CallerConversationID, r.rec.CallerConversationID
	}
	return out
}

// watchIdle notes a role still working once the workflow's limits.idle has
// passed (once per turn of it): in the run's chat and for its coordinator.
func (e *Engine) watchIdle(run *wfRun, conv storage.Conversation, role string) {
	after := run.def.IdleAfter()
	if after <= 0 {
		return
	}
	e.wf.mu.Lock()
	gen := 0
	if r := run.role(role); r != nil {
		gen = r.Turns
	}
	e.wf.mu.Unlock()
	time.AfterFunc(after, func() {
		e.wf.mu.Lock()
		r := run.role(role)
		if e.wf.byConv[conv.ID] != run || r == nil || r.Status != "working" || r.Turns != gen {
			e.wf.mu.Unlock()
			return
		}
		text := fmt.Sprintf("⏳ Vai %s (%s) đã làm quá %s mà chưa xong.", r.Name, cmp.Or(r.AgentName, r.Workflow), after)
		run.notes = append(run.notes, fmt.Sprintf("Role %s (%s) has worked for over %s and is not done yet.", r.Name, cmp.Or(r.AgentName, r.Workflow), after))
		run.logf("%s", text)
		rec, seq := run.snapshot()
		e.wf.mu.Unlock()
		e.saveRun(run, rec, seq)
		e.note(conv, text)
	})
}
