package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/agentskills"
	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A graph of steps (ADR-108): the office runs the workflow's steps in
// order in the run's own chat, without a coordinator: an agent's turn, a
// sub-workflow, code, an HTTP request, a condition, a person's approval, a
// check command, the end. Each step's result is a message of that chat and
// data for the steps after it.

// stepAuthor signs what the runner itself posts in the run's chat.
const stepAuthor = "⚙ Quy trình" // i18n-ignore

// launch starts a prepared run in its own chat: a coordinator's first turn,
// or the runner of its steps.
func (e *Engine) launch(ctx context.Context, own storage.Conversation, run *wfRun, text, prompt, pageContext string, attachmentIDs []string) error {
	if run.trusted {
		ctx = withTrustedRun(ctx)
	}
	if !run.def.StepMode() {
		_, _, err := e.SendWithContext(withPrepared(ctx, &prepared{conv: own.ID, run: run, prompt: prompt}), own.ID, text, pageContext, attachmentIDs)
		return err
	}
	if _, err := e.store.Chat().AddMessage(ctx, storage.Message{ConversationID: own.ID, Role: "user", Content: text, Author: actor.From(ctx)}); err != nil {
		return err
	}
	if run.inputs == nil {
		run.inputs = inputsFrom(run.def, run.rec.Input)
	}
	run.tree = treeOptOf(ctx)
	if err := e.beginRun(own, run, actor.From(ctx), ModelTierFrom(ctx), ceilingOf(ctx)); err != nil {
		return err
	}
	sctx, stop := context.WithCancel(context.Background())
	e.wf.mu.Lock()
	run.stop = stop
	e.wf.mu.Unlock()
	safeGo("runSteps", func() { e.runSteps(sctx, own, run) })
	return nil
}

// inputsFrom reads a chat's request as a workflow's inputs: a JSON object,
// or "key: value" lines for the keys it declares; the whole text is "text".
func inputsFrom(d workflow.Def, text string) map[string]string {
	out := map[string]string{"text": strings.TrimSpace(text)}
	var obj map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(text)), &obj) == nil {
		for k, v := range obj {
			if s, ok := v.(string); ok {
				out[k] = s
			} else {
				b, _ := json.Marshal(v)
				out[k] = string(b)
			}
		}
		return out
	}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, ":")
		k = strings.TrimSpace(k)
		for _, f := range d.Inputs {
			if ok && f.Key == k {
				out[k] = strings.TrimSpace(v)
			}
		}
	}
	// one declared input: the whole text is it, unless it is a number or a
	// boolean (free text there only fails the step: `--days "look at X"`)
	if len(d.Inputs) > 0 && out[d.Inputs[0].Key] == "" && (d.Inputs[0].Type == "" || d.Inputs[0].Type == "string") {
		out[d.Inputs[0].Key] = out["text"]
	}
	return out
}

// runSteps runs a graph of steps to its end (or until the run is stopped).
// The steps one output goes to run at once (up to limits.concurrency); a
// step waits while a running or waiting step can still reach it (a join),
// so it runs once with every branch's output. The run ends when no step is
// left; the end steps reached give its summary and outputs.
func (e *Engine) runSteps(ctx context.Context, own storage.Conversation, run *wfRun) {
	e.runStepsFrom(ctx, own, run, nil)
}

// runStepsFrom is runSteps from a checkpoint (nil: from its start): the
// steps it had running start again, those it had reached wait their turn
// (ADR-256). It saves where it is each time a step starts or ends.
func (e *Engine) runStepsFrom(ctx context.Context, own storage.Conversation, run *wfRun, from *storage.RunCheckpoint) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d := run.def
	vars := workflow.Vars{Input: run.inputs, Steps: map[string]workflow.StepResult{}}
	visits := map[string]int{}
	fail := func(why string) {
		// office going down cuts its steps: the run stays as saved, the next
		// office goes on with it (ADR-256)
		if ctx.Err() == nil && !e.stopping.Load() {
			e.note(own, "⚠️ "+why)
			e.finishRun(own.ID, run.rec.ID, storage.RunFailed, "", why)
		}
	}
	defer func() {
		// a panic in the orchestration loop itself (not a step, those are
		// caught below) must still end the run instead of leaving it
		// "running" forever with nothing left to drive it forward.
		if r := recover(); r != nil {
			slog.Error("chat: runSteps panic", "panic", r, "stack", string(debug.Stack()))
			fail(fmt.Sprintf("internal error: %v", r))
		}
	}()
	type result struct {
		s   workflow.Step
		res workflow.StepResult
		err error
	}
	results := make(chan result)
	running := map[string]bool{}
	var pending []string // reached, waiting their turn (no step twice)
	reach := func(ids []string) {
		for _, id := range ids {
			if !slices.Contains(pending, id) {
				pending = append(pending, id)
			}
		}
	}
	// free: no other running or waiting step can still reach id
	free := func(id string) bool {
		for _, other := range pending {
			if other != id && d.Reaches(other, id) {
				return false
			}
		}
		for other := range running {
			if other != id && d.Reaches(other, id) {
				return false
			}
		}
		return true
	}
	var (
		ended   bool
		summary string
		outputs = map[string]string{}
		last    string // the step that finished last: the summary when the end says none
		n       int    // steps started (MaxStepsRun)
	)
	// save writes where the run is: what ended, what runs, what waits
	save := func() {
		cp := &storage.RunCheckpoint{Input: maps.Clone(vars.Input), Steps: map[string]storage.StepState{}, Visits: maps.Clone(visits),
			Pending: slices.Clone(pending), Running: map[string]string{}, Ended: ended, Summary: summary, Outputs: maps.Clone(outputs), Last: last, N: n,
			Tier: run.tier, Ceiling: run.ceiling, Tree: run.tree.name, TreeNoPatch: run.tree.noPatch, TreePinned: run.tree.pinned,
			Trusted: run.trusted, DepthLeft: run.depthLeft}
		for id, r := range vars.Steps {
			cp.Steps[id] = storage.StepState{Output: r.Output, Status: r.Status, JSON: r.JSON}
		}
		for id := range running {
			s, _ := d.Step(id)
			cp.Running[id] = s.Type
		}
		e.wf.mu.Lock()
		run.cp = cp
		rec, seq := run.snapshot()
		e.wf.mu.Unlock()
		e.saveRun(run, rec, seq)
	}
	// start runs a step in the background; its result comes to results
	start := func(id string, s workflow.Step) {
		running[id] = true
		snap := workflow.Vars{Input: vars.Input, Steps: maps.Clone(vars.Steps)}
		go func() {
			var res workflow.StepResult
			var err error
			func() {
				// a panic here must still reach results — a bare
				// recover (e.g. via safeGo) would silently drop it
				// and runSteps would wait on this step forever.
				defer func() {
					if r := recover(); r != nil {
						slog.Error("chat: runStep panic", "where", "runStep:"+s.ID, "panic", r, "stack", string(debug.Stack()))
						err = fmt.Errorf("internal error: %v", r)
					}
				}()
				if e.testStepPanic != "" && e.testStepPanic == s.ID {
					panic("test panic: " + s.ID)
				}
				res, err = e.runStep(ctx, own, run, s, snap)
			}()
			select {
			case results <- result{s, res, err}:
			case <-ctx.Done():
			}
		}()
	}
	if from == nil {
		reach(d.Starts())
	} else {
		for id, r := range from.Steps {
			vars.Steps[id] = workflow.StepResult{Output: r.Output, Status: r.Status, JSON: r.JSON}
		}
		if from.Visits != nil {
			visits = maps.Clone(from.Visits)
		}
		pending = slices.Clone(from.Pending)
		ended, summary, last, n = from.Ended, from.Summary, from.Last, from.N
		if from.Outputs != nil {
			outputs = maps.Clone(from.Outputs)
		}
		// the steps the restart cut start again, in a steady order
		for _, id := range slices.Sorted(maps.Keys(from.Running)) {
			s, ok := d.Step(id)
			if !ok {
				fail("không có bước " + id)
				return
			}
			start(id, s)
		}
	}
	for {
		if ctx.Err() != nil {
			return
		}
		// start every step that may go now
		for progress := true; progress; {
			progress = false
			force := len(running) == 0 && !slices.ContainsFunc(pending, free) // a loop waiting on itself: its first step goes
			for i := 0; i < len(pending); i++ {
				id := pending[i]
				if !(force && i == 0) && !free(id) {
					continue
				}
				s, ok := d.Step(id)
				if ok && s.Type == workflow.StepAgent && slices.ContainsFunc(slices.Collect(maps.Keys(running)), func(r string) bool { o, _ := d.Step(r); return o.Type == workflow.StepAgent && o.Role == s.Role }) {
					continue // one role answers one step at a time
				}
				if len(running) >= d.Limits.Concurrency && ok && s.Type != workflow.StepEnd && s.Type != workflow.StepCondition && s.Type != workflow.StepSwitch && s.Type != workflow.StepWait {
					continue
				}
				pending = slices.Delete(pending, i, i+1)
				i--
				progress, force = true, false
				if n++; n > workflow.MaxStepsRun {
					fail(fmt.Sprintf("quá %d bước trong một lần chạy", workflow.MaxStepsRun))
					return
				}
				if !ok {
					fail("không có bước " + id)
					return
				}
				visits[id]++
				if most := cmp.Or(s.MaxLoops, workflow.DefaultMaxLoops); visits[id] > most+1 { // a way back is a loop: bounded
					fail(fmt.Sprintf("bước %s lặp quá %d lần (max_loops)", workflow.StepLabel(s), most))
					return
				}
				e.wf.mu.Lock()
				run.logf("Bước %s", workflow.StepLabel(s))
				rec, seq := run.snapshot()
				e.wf.mu.Unlock()
				e.saveRun(run, rec, seq)
				switch s.Type {
				case workflow.StepEnd:
					ended = true
					if v := strings.TrimSpace(workflow.Render(s.Summary, vars)); v != "" {
						summary = v
					}
					for k, v := range s.Outputs {
						outputs[k] = strings.TrimSpace(workflow.Render(v, vars))
					}
				case workflow.StepCondition:
					next := s.Else
					yes := workflow.Eval(s.If, vars)
					if yes {
						next = s.Then
					}
					vars.Steps[s.ID] = workflow.NewResult(strings.Join(next, ","), strconv.FormatBool(yes))
					reach(next)
				case workflow.StepSwitch:
					got, next := s.Pick(vars)
					vars.Steps[s.ID] = workflow.NewResult(strings.Join(next, ","), got)
					reach(next)
				default:
					start(id, s)
				}
			}
		}
		if len(running) == 0 && len(pending) == 0 {
			break
		}
		save()
		var r result
		select {
		case r = <-results:
		case <-ctx.Done():
			return
		}
		s, err := r.s, r.err
		if err != nil && (ctx.Err() != nil || e.stopping.Load()) {
			return // stopped, or cut by office going down: saved as running, the next office starts it again
		}
		delete(running, s.ID)
		e.wf.mu.Lock()
		delete(run.marks, s.ID)
		delete(run.before, s.ID) // its next turn in a loop starts anew
		delete(run.resumed, s.ID)
		e.wf.mu.Unlock()
		if err != nil { // a failure is an output too, for the steps after it
			r.res.Status = cmp.Or(r.res.Status, "error")
			r.res.Output = cmp.Or(r.res.Output, err.Error())
		}
		vars.Steps[s.ID], last = r.res, s.ID
		switch {
		case err == nil:
			reach(s.Next)
		case errors.Is(err, workflow.ErrStepFailed) && len(s.Else) > 0: // a check that failed, an approval refused
			reach(s.Else)
		case s.OnError == "continue":
			e.note(own, fmt.Sprintf("Bước %s lỗi, đi tiếp: %s", workflow.StepLabel(s), err))
			reach(s.Next)
		default:
			fail(fmt.Sprintf("bước %s: %s", workflow.StepLabel(s), err))
			return
		}
	}
	save()
	if !ended {
		fail("không nhánh nào tới bước end")
		return
	}
	if summary == "" { // the last step's output
		summary = strings.TrimSpace(vars.Steps[last].Output)
	}
	e.endSteps(own, run, summary, outputs)
}

// post puts a step's result in the run's chat.
func (e *Engine) post(own storage.Conversation, s workflow.Step, text string) {
	_, _ = e.store.Chat().AddMessage(context.Background(), storage.Message{ConversationID: own.ID, Role: "assistant", Author: stepAuthor,
		Content: "**" + workflow.StepLabel(s) + "**\n\n" + text})
}

func fence(s string) string {
	s = truncate(strings.TrimSpace(s), 4000)
	if s == "" {
		return "_(trống)_" // i18n-ignore
	}
	return "```\n" + strings.ReplaceAll(s, "```", "'''") + "\n```"
}

// runStep runs one step but a condition or the end.
func (e *Engine) runStep(ctx context.Context, own storage.Conversation, run *wfRun, s workflow.Step, vars workflow.Vars) (workflow.StepResult, error) {
	project, err := e.store.Repos().Get(ctx, run.rec.ProjectID)
	if err != nil {
		return workflow.StepResult{}, err
	}
	if readsProject(s) { // the roles' diffs reach the project first
		if err := e.waitMerged(ctx, own, s); err != nil {
			return workflow.StepResult{}, err
		}
	}
	switch s.Type {
	case workflow.StepAgent:
		out, err := e.stepAgent(ctx, own, project, run, s, workflow.Render(s.Prompt, vars))
		return workflow.NewResult(out, ""), err
	case workflow.StepWorkflow, workflow.StepCoordinate:
		var rec storage.WorkflowRun
		if m, ok := e.markBefore(run, s.ID); ok && m.ChildRunID != "" { // the sub-run the last office left (ADR-256)
			if got, ok, err := e.resumeChildStep(ctx, own, run, s, m.ChildRunID); ok {
				return childResult(got), err
			}
		}
		if s.Type == workflow.StepCoordinate {
			rec, err = e.stepCoordinate(ctx, own, project, run, s, workflow.Render(s.Prompt, vars))
		} else {
			inputs := map[string]string{}
			for k, v := range s.Inputs {
				inputs[k] = workflow.Render(v, vars)
			}
			rec, err = e.stepFlow(ctx, own, project, run, s, inputs)
		}
		return childResult(rec), err
	case workflow.StepCode:
		data, _ := json.Marshal(vars)
		env := []string{"OFFICE_RUN=" + run.rec.ID, "OFFICE_PROJECT=" + project.ID}
		for k, v := range vars.Input {
			env = append(env, "OFFICE_INPUT_"+strings.ToUpper(strings.ReplaceAll(k, "-", "_"))+"="+v)
		}
		out, code, timedOut, err := trigger.RunScript(ctx, project.Path, storage.AutomationScript{Lang: s.Lang, Body: s.Script, TimeoutS: s.TimeoutS}, env, string(data))
		e.post(own, s, fmt.Sprintf("Thoát mã %d\n%s", code, fence(out))) // i18n-ignore
		res := workflow.NewResult(strings.TrimSpace(out), strconv.Itoa(code))
		switch {
		case err != nil:
			return res, err
		case timedOut:
			return res, errors.New("hết thời gian")
		case code != 0:
			return res, fmt.Errorf("%w: thoát mã %d", workflow.ErrStepFailed, code)
		}
		return res, nil
	case workflow.StepHTTP:
		r := s
		r.URL, r.Body = workflow.Render(s.URL, vars), workflow.Render(s.Body, vars)
		r.Headers = map[string]string{}
		for k, v := range s.Headers {
			r.Headers[k] = workflow.Render(v, vars)
		}
		code, body, err := workflow.DoHTTP(ctx, r)
		e.post(own, s, fmt.Sprintf("%s %s → %d\n%s", cmp.Or(strings.ToUpper(r.Method), "GET"), r.URL, code, fence(body)))
		res := workflow.NewResult(body, strconv.Itoa(code))
		switch {
		case err != nil:
			return res, err
		case code >= 400:
			return res, fmt.Errorf("%w: HTTP %d", workflow.ErrStepFailed, code)
		}
		return res, nil
	case workflow.StepWait:
		d, err := workflow.WaitFor(workflow.Render(s.For, vars))
		if err != nil {
			return workflow.StepResult{}, err
		}
		// it waits until a time, kept with the run: the next office waits
		// only for what is left of it (ADR-256)
		until := time.Now().Add(d)
		if m, ok := e.markBefore(run, s.ID); ok && m.WaitUntil != nil {
			until = *m.WaitUntil
			e.post(own, s, "Chờ tiếp tới "+until.Local().Format("15:04")+" (office đã khởi động lại)") // i18n-ignore
		} else {
			e.post(own, s, "Chờ "+d.String()+", tới "+until.Local().Format("15:04")) // i18n-ignore
		}
		at := until.UTC()
		e.wf.mu.Lock()
		run.mark(s.ID, storage.StepMark{WaitUntil: &at})
		rec, seq := run.snapshot()
		e.wf.mu.Unlock()
		e.saveRun(run, rec, seq)
		t := time.NewTimer(max(time.Until(until), 0))
		defer t.Stop()
		select {
		case <-t.C:
			return workflow.NewResult(d.String(), "done"), nil
		case <-ctx.Done():
			return workflow.StepResult{}, ctx.Err()
		}
	case workflow.StepApprove:
		return e.stepDecide(ctx, own, run, s, "workflow_gate", run.def.Name+": "+workflow.StepLabel(s), workflow.Render(s.Note, vars))
	case workflow.StepCheck:
		return e.stepDecide(ctx, own, run, s, "run_command", workflow.Render(s.Command, vars), "Bước "+workflow.StepLabel(s)+" của quy trình "+run.def.Name)
	}
	return workflow.StepResult{}, fmt.Errorf("bước loại %q không chạy được", s.Type)
}

// childResult is a sub-run's end as its step's result.
func childResult(rec storage.WorkflowRun) workflow.StepResult {
	r := workflow.NewResult(strings.TrimSpace(rec.Result), rec.Status)
	if len(rec.Outputs) > 0 {
		obj := map[string]any{} // {{steps.<id>.json.key}} reads JSON objects
		for k, v := range rec.Outputs {
			obj[k] = v
		}
		r.JSON = obj
		b, _ := json.Marshal(rec.Outputs)
		if r.Output == "" {
			r.Output = string(b)
		}
	}
	return r
}

// stepAgent runs an agent's turn for a step and waits for its answer.
func (e *Engine) stepAgent(ctx context.Context, own storage.Conversation, project storage.Repo, run *wfRun, s workflow.Step, prompt string) (string, error) {
	d, _ := run.def.Role(s.Role)
	a, err := e.pickAgent(ctx, run, s.Role, "")
	if err != nil {
		return "", err
	}
	if s.Skill != "" { // the step's skill goes with its prompt, counted as the agent's use (ADR-185)
		skill, err := agentskills.Use(ctx, e.store, userHome(), project.Path, a.ID, s.Skill, workflow.StepLabel(s))
		switch {
		case errors.Is(err, automation.ErrUnknownSkill): // the step still runs, without it
			e.post(own, s, "Project chưa có skill `"+s.Skill+"`: bước chạy không có hướng dẫn của skill (cài ở trang Tự động hóa).") // i18n-ignore
		case err != nil:
			return "", err
		default:
			prompt = skill + "\n\n" + prompt
		}
	}
	level := accessLevel(d.Access)
	if run.ceiling != "" {
		level = perm.Min(level, run.ceiling)
	}
	if e.wasCut(run, s.ID) { // its session keeps what it did before office went down (ADR-256)
		prompt = prompts.Render("workflow/step-resumed", nil) + "\n\n" + prompt
	}
	base := prompt
	for try := 1; ; try++ {
		out, err := e.stepTurn(ctx, own, project, run, s, a, d, level, prompt)
		// a passing failure of the AI service: the step goes on in its
		// session a while later instead of failing the run (ADR-260)
		if err == nil || ctx.Err() != nil || !Transient(err) || try > len(StepTransientWaits) || e.stopping.Load() {
			return out, err
		}
		wait := StepTransientWaits[try-1]
		e.post(own, s, prompts.Render("workflow/step-api-wait", map[string]any{"Error": truncate(err.Error(), 300), "Wait": wait.String(), "N": try, "Max": len(StepTransientWaits)}))
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
		prompt = prompts.Render("workflow/step-api-resumed", nil) + "\n\n" + base
	}
}

// StepTransientWaits are the waits before a step cut by a passing API
// failure goes on, one per try (tests make them short).
var StepTransientWaits = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

// stepTurn runs one turn of a step's agent and waits for its answer.
func (e *Engine) stepTurn(ctx context.Context, own storage.Conversation, project storage.Repo, run *wfRun, s workflow.Step, a storage.Agent, d workflow.Role, level, prompt string) (string, error) {
	key := run.rec.ID + "#" + s.ID
	ch := make(chan string, 1)
	e.wf.mu.Lock()
	e.wf.waiting[key] = ch
	if r := run.role(s.Role); r != nil {
		r.Status, r.AgentID, r.AgentName = "working", a.ID, a.Name
		r.Turns++
	}
	run.rec.Turns++
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	done := func(status string) {
		e.wf.mu.Lock()
		delete(e.wf.waiting, key)
		if r := run.role(s.Role); r != nil {
			r.Status = status
		}
		rec, seq := run.snapshot()
		e.wf.mu.Unlock()
		e.saveRun(run, rec, seq)
	}
	t, why := e.startTurn(own, project, turnSpec{agent: a, background: true, actor: run.rec.Actor, tier: run.tier, ceiling: level, tree: run.tree,
		title: run.def.Name + " → " + a.Name + " (" + workflow.StepLabel(s) + ")", prompt: prompt + "\n\n" + workflow.AccessNote(d.Access),
		wfRun: run.rec.ID, wfStep: s.ID, trusted: run.trusted})
	if t == nil {
		done("failed")
		return "", errors.New(cmp.Or(why, "chat của lần chạy đang bận"))
	}
	select {
	case out := <-ch:
		if msg, bad := strings.CutPrefix(out, "\x00"); bad {
			done("failed")
			return "", errors.New(msg)
		}
		done("done")
		return out, nil
	case <-ctx.Done():
		done("failed")
		return "", ctx.Err()
	}
}

// stepFlow runs a sub-workflow for a step and waits for it to end.
func (e *Engine) stepFlow(ctx context.Context, own storage.Conversation, project storage.Repo, run *wfRun, s workflow.Step, inputs map[string]string) (storage.WorkflowRun, error) {
	if run.depthLeft <= 0 {
		return storage.WorkflowRun{}, errors.New("đã tới giới hạn lồng quy trình con (limits.depth)")
	}
	w, err := e.store.Workflows().GetByKey(ctx, project.ID, s.Workflow)
	if err != nil {
		return storage.WorkflowRun{}, fmt.Errorf("project chưa cài quy trình /%s", s.Workflow)
	}
	def, err := workflow.Parse(w.Source)
	if err != nil {
		return storage.WorkflowRun{}, err
	}
	if def.Callable == workflow.CallableChat {
		return storage.WorkflowRun{}, fmt.Errorf("quy trình /%s chỉ chạy từ chat", def.Key)
	}
	input := ""
	if len(def.Inputs) > 0 {
		if input, err = def.RenderInputs(inputs); err != nil {
			return storage.WorkflowRun{}, err
		}
	} else {
		input = cmp.Or(inputs["text"], fmt.Sprint(inputs))
	}
	child, prompt, err := e.newRun(ctx, project.ID, w, def, run.coord, input)
	if err != nil {
		return storage.WorkflowRun{}, err
	}
	child.inputs = inputs
	child.depthLeft = min(run.depthLeft-1, def.Limits.Depth)
	return e.runChild(ctx, own, project, run, s, child, "/"+def.Key+" "+input, prompt, def.Name+": "+oneLine(input))
}

// stepCoordinate runs a coordinate step (ADR-111): the step's roles under an
// agent that decides, as a workflow without steps, in a chat of its own;
// the run is a child of this one at the same depth.
func (e *Engine) stepCoordinate(ctx context.Context, own storage.Conversation, project storage.Repo, run *wfRun, s workflow.Step, body string) (storage.WorkflowRun, error) {
	coord := run.coord
	if s.Role != "" {
		a, err := e.pickAgent(ctx, run, s.Role, "")
		if err != nil {
			return storage.WorkflowRun{}, err
		}
		coord = a
	}
	def := run.def.CoordinateDef(s, body)
	e.wf.mu.Lock()
	w := storage.Workflow{ID: run.rec.WorkflowID, Bindings: maps.Clone(run.bindings)}
	e.wf.mu.Unlock()
	child, prompt, err := e.newRun(ctx, project.ID, w, def, coord, run.rec.Input)
	if err != nil {
		return storage.WorkflowRun{}, err
	}
	child.inputs = run.inputs
	child.depthLeft = run.depthLeft
	child.rec.WorkflowName = def.Name + " · " + workflow.StepLabel(s)
	return e.runChild(ctx, own, project, run, s, child, "/"+def.Key+" "+run.rec.Input, prompt, child.rec.WorkflowName)
}

// runChild runs a prepared child run in a chat of its own and waits for it to end.
func (e *Engine) runChild(ctx context.Context, own storage.Conversation, project storage.Repo, run *wfRun, s workflow.Step, child *wfRun, text, prompt, title string) (storage.WorkflowRun, error) {
	def := child.def
	child.until = run.deadline
	child.parent = run
	child.trusted = run.trusted
	child.rec.CallerConversationID, child.rec.ParentRunID, child.rec.Depth = own.ID, run.rec.ID, run.rec.Depth+1
	sub, err := e.store.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: project.ID, AgentID: run.coord.ID, AgentName: run.coord.Name, CreatedBy: run.rec.Actor,
		Purpose: RunPurpose, Title: truncate(title, 80), Mode: own.Mode, EditMode: own.EditMode})
	if err != nil {
		return storage.WorkflowRun{}, err
	}
	sctx := withTreeOpt(actor.With(context.Background(), run.rec.Actor), run.tree)
	if run.tier != "" {
		sctx = WithModelTier(sctx, run.tier)
	}
	if run.ceiling != "" {
		sctx = WithCeiling(sctx, run.ceiling)
	}
	if err := e.launch(sctx, sub, child, text, prompt, "", nil); err != nil {
		_ = e.store.Chat().DeleteConversation(ctx, sub.ID)
		return storage.WorkflowRun{}, err
	}
	e.wf.mu.Lock()
	run.logf("%s: chạy %s", workflow.StepLabel(s), cmp.Or(map[bool]string{true: "điều phối"}[s.Type == workflow.StepCoordinate], "quy trình con /"+def.Key))
	run.mark(s.ID, storage.StepMark{ChildRunID: child.rec.ID}) // the next office goes on with it (ADR-256)
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	return e.awaitStepChild(ctx, own, s, child)
}

// awaitStepChild waits for a step's sub-run to end and posts its result.
func (e *Engine) awaitStepChild(ctx context.Context, own storage.Conversation, s workflow.Step, child *wfRun) (storage.WorkflowRun, error) {
	select {
	case <-child.done:
	case <-ctx.Done():
		if !e.stopping.Load() { // office going down: the sub-run stays, the next office goes on with it
			e.StopAll(child.rec.ConversationID)
		}
		return storage.WorkflowRun{}, ctx.Err()
	}
	rec, err := e.store.WorkflowRuns().Get(context.Background(), child.rec.ID)
	if err != nil {
		rec = child.rec
	}
	return e.childEnded(own, s, rec)
}

// childEnded posts a sub-run's end in its step's chat: an error unless it is done.
func (e *Engine) childEnded(own storage.Conversation, s workflow.Step, rec storage.WorkflowRun) (storage.WorkflowRun, error) {
	what := "Quy trình con /" + rec.WorkflowKey // i18n-ignore
	if s.Type == workflow.StepCoordinate {
		what = "Điều phối" // i18n-ignore
	}
	e.post(own, s, fmt.Sprintf("%s: %s\n\n%s", what, rec.Status, strings.TrimSpace(rec.Result)+outputsText(rec)))
	if rec.Status != storage.RunDone {
		return rec, fmt.Errorf("%w: %s %s %s", workflow.ErrStepFailed, what, rec.Status, rec.Error)
	}
	return rec, nil
}

// stepDecide opens a card (an approval, a check command) and waits until
// it is decided; a command the project lets run goes at once.
func (e *Engine) stepDecide(ctx context.Context, own storage.Conversation, run *wfRun, s workflow.Step, kind, target, reason string) (workflow.StepResult, error) {
	if e.acts == nil {
		return workflow.StepResult{}, errors.New("office không mở thẻ duyệt được ở đây")
	}
	sc := actions.Scope{ProjectID: run.rec.ProjectID, ConversationID: own.ID, Agent: stepAuthor}
	if kind == "run_command" { // what the coordinator's agent may run without asking, in this chat's way
		policy := perm.LoadPolicy(ctx, e.store, run.rec.ProjectID)
		acc := perm.Resolve(run.coord, cmp.Or(run.ceiling, own.Mode), policy)
		sc.Level, sc.Access, sc.Agent = acc.Level, acc, run.coord.Name
	}
	var a storage.Action
	var err error
	if m, ok := e.markBefore(run, s.ID); ok && m.ActionID != "" { // the card the last office opened (ADR-256)
		if a, err = e.store.Actions().Get(ctx, m.ActionID); err != nil || a.ConversationID != own.ID {
			a = storage.Action{}
		}
	}
	if a.ID == "" {
		if a, err = e.acts.Propose(ctx, sc, kind, target, reason); err != nil {
			return workflow.StepResult{}, err
		}
	}
	e.wf.mu.Lock()
	run.mark(s.ID, storage.StepMark{ActionID: a.ID})
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	for a.Status == "pending" || a.Status == "queued" {
		select {
		case <-ctx.Done():
			return workflow.StepResult{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		if a, err = e.store.Actions().Get(ctx, a.ID); err != nil {
			return workflow.StepResult{}, err
		}
	}
	res := workflow.NewResult(a.Detail, "passed")
	if a.Status != "done" {
		res.Status = "failed"
		e.post(own, s, "Không qua: "+a.Status+"\n"+fence(a.Detail)) // i18n-ignore
		return res, fmt.Errorf("%w: %s", workflow.ErrStepFailed, a.Status)
	}
	if kind == "run_command" {
		e.post(own, s, "Đạt\n"+fence(a.Detail)) // i18n-ignore
	}
	return res, nil
}

// endSteps ends a graph of steps with its summary and outputs.
func (e *Engine) endSteps(own storage.Conversation, run *wfRun, summary string, outputs map[string]string) {
	if err := run.def.CheckOutputs(outputs); err != nil {
		e.note(own, "⚠️ "+err.Error())
		e.finishRun(own.ID, run.rec.ID, storage.RunFailed, summary, err.Error())
		return
	}
	e.wf.mu.Lock()
	run.rec.Outputs = outputs
	e.wf.mu.Unlock()
	e.finishRun(own.ID, run.rec.ID, storage.RunDone, cmp.Or(summary, "Xong."), "")
}
