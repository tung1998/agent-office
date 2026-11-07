package chat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A graph of steps survives a restart (ADR-256): it saves where it is as it
// goes (runStepsFrom), the next office goes on from there. The steps the
// restart cut start again (an agent in its session, told so; a wait for
// what is left of it; an approval or a check on the card it had opened; a
// sub-workflow goes on too), the chat that called it gets its output when
// it ends and the job that waited for it ends then. A coordinator's run
// still ends at a restart: its decisions live in its session.

// SetOnResumedJob hears the job of a chat that called a run end after the
// run went on past a restart: what would have followed it (the next link
// of an automation's chain) follows it now.
func (e *Engine) SetOnResumedJob(fn func(ctx context.Context, j storage.Job, text string)) {
	e.onResumedJob = fn
}

// runHeld is the run as this office runs it (nil: it does not).
func (e *Engine) runHeld(r storage.WorkflowRun) *wfRun {
	if x := e.runOf(r.ConversationID); x != nil && x.rec.ID == r.ID {
		return x
	}
	return nil
}

// markBefore is what step id waited on when the last office went down,
// read once: the step's next turn in a loop starts anew (a new card, a
// whole wait, a new sub-run).
func (e *Engine) markBefore(run *wfRun, id string) (storage.StepMark, bool) {
	e.wf.mu.Lock()
	defer e.wf.mu.Unlock()
	m, ok := run.before[id]
	delete(run.before, id)
	return m, ok
}

// wasCut says, once, whether the restart cut step id in the middle.
func (e *Engine) wasCut(run *wfRun, id string) bool {
	e.wf.mu.Lock()
	defer e.wf.mu.Unlock()
	cut := run.resumed[id]
	delete(run.resumed, id)
	return cut
}

// ResumableRuns are the runs left running that the next office goes on
// with (their ids): graphs of steps with a checkpoint whose workflow did
// not change, and their sub-runs that are such too. The others end.
func (e *Engine) ResumableRuns(ctx context.Context) map[string]bool {
	keep := map[string]bool{}
	runs, err := e.store.WorkflowRuns().ListRunning(ctx)
	if err != nil {
		return keep
	}
	for more := true; more; { // a sub-run goes on only under a parent that does
		more = false
		for _, r := range runs {
			if r.Status != storage.RunRunning || keep[r.ID] || r.ParentRunID != "" && !keep[r.ParentRunID] {
				continue
			}
			if _, _, err := workflow.CanResume(ctx, e.store, r); err == nil {
				keep[r.ID], more = true, true
			}
		}
	}
	return keep
}

// ResumeWorkflows goes on with the runs keep names (ResumableRuns) that a
// chat called (a sub-run goes on when its parent's step comes to it); why
// is why office went down. It returns how many went on.
func (e *Engine) ResumeWorkflows(ctx context.Context, why string, keep map[string]bool) int {
	runs, err := e.store.WorkflowRuns().ListRunning(ctx)
	if err != nil {
		return 0
	}
	slices.SortFunc(runs, func(a, b storage.WorkflowRun) int { return a.StartedAt.Compare(b.StartedAt) })
	n := 0
	for _, r := range runs {
		if !keep[r.ID] || r.ParentRunID != "" || r.Status != storage.RunRunning {
			continue
		}
		if _, err := e.resumeRun(ctx, r, nil, why); err != nil {
			slog.Warn("chat: resume workflow", "run", r.ID, "err", err)
			e.endOrphan(ctx, r, "không chạy tiếp được sau khi office khởi động lại: "+err.Error())
			continue
		}
		n++
	}
	return n
}

// ErrNotResumable: the run cannot go on from where it stopped.
var ErrNotResumable = errors.New("lần chạy này không chạy tiếp được")

// ResumeRun goes on with a run office restarted under and ended (the
// dashboard's "Chạy tiếp từ bước dở"): a graph of steps that kept where it
// was, whose workflow did not change since.
func (e *Engine) ResumeRun(ctx context.Context, id string) error {
	r, err := e.store.WorkflowRuns().Get(ctx, id)
	if err != nil {
		return err
	}
	if r.Status != storage.RunFailed || !strings.HasPrefix(r.Error, workflow.InterruptedDetail) || r.ParentRunID != "" {
		return fmt.Errorf("%w: chỉ lần chạy bị dừng vì office khởi động lại", ErrNotResumable)
	}
	if _, _, err := workflow.CanResume(ctx, e.store, r); err != nil {
		return fmt.Errorf("%w: %s", ErrNotResumable, err.Error())
	}
	if e.calledFrom(r.CallerConversationID) != nil || e.runOf(r.ConversationID) != nil {
		return ErrWorkflowRunning
	}
	_, err = e.resumeRun(ctx, r, nil, "chạy tiếp bằng tay")
	return err
}

// resumeRun goes on with a saved run from its checkpoint; parent: the run
// whose step it is (nil: a chat called it).
func (e *Engine) resumeRun(ctx context.Context, rec storage.WorkflowRun, parent *wfRun, why string) (*wfRun, error) {
	w, def, err := workflow.CanResume(ctx, e.store, rec)
	if err != nil {
		return nil, err
	}
	coord, err := e.store.Agents().Get(ctx, rec.CoordinatorID)
	if err != nil {
		return nil, fmt.Errorf("agent điều phối không còn: %w", err)
	}
	conv, err := e.store.Chat().GetConversation(ctx, rec.ConversationID)
	if err != nil {
		return nil, err
	}
	run, _, err := e.newRun(ctx, rec.ProjectID, w, def, coord, rec.Input)
	if err != nil {
		return nil, err
	}
	cp := rec.Checkpoint
	run.rec = rec
	run.rec.Status, run.rec.Error, run.rec.FinishedAt = storage.RunRunning, "", nil
	run.inputs = cp.Input
	if run.inputs == nil {
		run.inputs = inputsFrom(def, rec.Input)
	}
	run.tier, run.ceiling, run.trusted = cp.Tier, cp.Ceiling, cp.Trusted
	run.tree = treeOpt{name: cp.Tree, noPatch: cp.TreeNoPatch, pinned: cp.TreePinned}
	run.depthLeft, run.callerJob = cp.DepthLeft, cp.CallerJob
	run.resumes = cp.Resumes
	if workflow.Cuts(ctx, e.store, cp) { // a step cut in the middle: counted (a wait or a card is not)
		run.resumes++
	}
	run.cp = cp
	run.before = maps.Clone(cp.Marks)
	run.marks = maps.Clone(cp.Marks) // kept until each step marks again: a second restart loses none
	run.resumed = map[string]bool{}
	for id := range cp.Running {
		run.resumed[id] = true
	}
	if parent != nil {
		e.wf.mu.Lock()
		run.parent, run.until = parent, parent.deadline
		e.wf.mu.Unlock()
	}
	run.deadline = rec.StartedAt.Add(def.Timeout())
	if !run.until.IsZero() && run.until.Before(run.deadline) {
		run.deadline = run.until
	}
	where := stepsAt(def, cp)
	e.wf.mu.Lock()
	e.wf.init()
	if x := e.wf.byConv[conv.ID]; x != nil {
		e.wf.mu.Unlock()
		return nil, ErrWorkflowRunning
	}
	e.wf.byConv[conv.ID] = run
	if left := time.Until(run.deadline); left > 0 {
		run.timer = time.AfterFunc(left, func() { e.wfExpire(conv.ID, rec.ID) })
	}
	sctx, stop := context.WithCancel(context.Background())
	run.stop = stop
	run.logf("Chạy tiếp sau khi office khởi động lại%s, từ %s", whyNote(why), where)
	snap, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, snap, seq)
	note := "▶️ Quy trình " + def.Name + " chạy tiếp từ " + where + " sau khi office khởi động lại" + whyNote(why) + "."
	e.note(conv, note)
	if parent == nil && rec.CallerConversationID != "" && rec.CallerConversationID != conv.ID {
		if caller, err := e.store.Chat().GetConversation(ctx, rec.CallerConversationID); err == nil {
			e.note(caller, note)
		}
		if run.callerJob != "" { // the job that waited for it waits again
			if _, err := e.store.Jobs().Reopen(ctx, run.callerJob, "restart"); err != nil && !errors.Is(err, storage.ErrConflict) {
				slog.Warn("chat: reopen the caller's job", "job", run.callerJob, "err", err)
			}
		}
		safeGo("awaitResumed", func() { e.awaitResumed(run) })
	}
	safeGo("runSteps", func() { e.runStepsFrom(sctx, conv, run, cp) })
	return run, nil
}

func whyNote(why string) string {
	if why == "" {
		return ""
	}
	return " (" + why + ")"
}

// stepsAt names where a checkpoint is: the steps running, else those waiting.
func stepsAt(d workflow.Def, cp *storage.RunCheckpoint) string {
	ids := slices.Sorted(maps.Keys(cp.Running))
	if len(ids) == 0 {
		ids = cp.Pending
	}
	var names []string
	for _, id := range ids {
		if s, ok := d.Step(id); ok {
			names = append(names, workflow.StepLabel(s))
		}
	}
	if len(names) == 0 {
		return "chỗ đã dừng" // i18n-ignore
	}
	return "bước " + strings.Join(names, ", ") // i18n-ignore
}

// awaitResumed gives the chat that called a resumed run its output when it
// ends, and ends the job that waited for it (awaitRun's part, its turn gone).
func (e *Engine) awaitResumed(run *wfRun) {
	<-run.done
	bg := context.Background()
	rec, err := e.store.WorkflowRuns().Get(bg, run.rec.ID)
	if err != nil {
		e.wf.mu.Lock()
		rec = run.rec
		e.wf.mu.Unlock()
	}
	role, content, runErr, stopped := runOutcome(rec)
	author, agentID := "", ""
	if role == "assistant" {
		author, agentID = rec.CoordinatorName, rec.CoordinatorID
	}
	m, err := e.store.Chat().AddMessage(bg, storage.Message{ConversationID: rec.CallerConversationID, Role: role, Content: content, Author: author, AgentID: agentID})
	if err != nil {
		slog.Warn("chat: resumed run's output", "run", rec.ID, "err", err)
	}
	if run.callerJob == "" {
		return
	}
	j, ok := e.endJob(run.callerJob, m.ID, runErr, stopped)
	if ok && e.onResumedJob != nil {
		e.onResumedJob(bg, j, content)
	}
}

// runOutcome is what the chat that called a run is told when it ends.
func runOutcome(rec storage.WorkflowRun) (role, content string, runErr, stopped error) {
	role, content = "assistant", strings.TrimSpace(rec.Result)+outputsText(rec)
	switch rec.Status {
	case storage.RunDone:
		if content == "" {
			content = "Quy trình " + rec.WorkflowName + " đã xong."
		}
	case storage.RunStopped:
		role, content, stopped = "error", "⏹️ Đã dừng quy trình "+rec.WorkflowName+".", context.Canceled
	default:
		role, content = "error", "⚠️ Quy trình "+rec.WorkflowName+" không xong: "+firstNonEmpty(rec.Error, "lỗi không rõ")
		runErr = errors.New(content)
	}
	return role, content, runErr, stopped
}

// resumeChildStep goes on with the sub-run a step had started before the
// restart: it waits for it again (going on with it when nobody runs it), or
// takes its end when it ended meanwhile. ok false: start a new one (it was
// ended by the restart, or is gone).
func (e *Engine) resumeChildStep(ctx context.Context, own storage.Conversation, run *wfRun, s workflow.Step, childID string) (storage.WorkflowRun, bool, error) {
	rec, err := e.store.WorkflowRuns().Get(ctx, childID)
	if err != nil || rec.ParentRunID != run.rec.ID {
		return storage.WorkflowRun{}, false, nil
	}
	switch {
	case rec.Status == storage.RunRunning:
		child := e.runHeld(rec)
		if child == nil {
			if child, err = e.resumeRun(ctx, rec, run, ""); err != nil {
				e.endOrphan(ctx, rec, "không chạy tiếp được sau khi office khởi động lại: "+err.Error())
				return storage.WorkflowRun{}, false, nil
			}
		}
		e.wf.mu.Lock()
		run.mark(s.ID, storage.StepMark{ChildRunID: rec.ID})
		snap, seq := run.snapshot()
		e.wf.mu.Unlock()
		e.saveRun(run, snap, seq)
		got, err := e.awaitStepChild(ctx, own, s, child)
		return got, true, err
	case rec.Status == storage.RunFailed && strings.HasPrefix(rec.Error, workflow.InterruptedDetail):
		return storage.WorkflowRun{}, false, nil // ended by the restart: the step runs it again
	default: // it ended before its step heard of it
		got, err := e.childEnded(own, s, rec)
		return got, true, err
	}
}

// endOrphan ends a run nobody runs any more, failed, and tells its chats.
func (e *Engine) endOrphan(ctx context.Context, r storage.WorkflowRun, why string) {
	now := time.Now().UTC()
	r.Status, r.Error, r.FinishedAt = storage.RunFailed, why, &now
	r.Log = append(r.Log, storage.RunLog{At: now, Text: "Thất bại: " + why})
	if err := e.store.WorkflowRuns().Update(ctx, r); err != nil {
		slog.Warn("chat: end an orphan run", "run", r.ID, "err", err)
		return
	}
	note := "⚠️ Quy trình " + cmp.Or(r.WorkflowName, r.WorkflowKey) + " đã dừng: " + why
	for _, id := range []string{r.ConversationID, r.CallerConversationID} {
		if id != "" {
			_, _ = e.store.Chat().AddMessage(ctx, storage.Message{ConversationID: id, Role: "error", Content: note})
		}
	}
	if r.ParentRunID == "" {
		e.runEnded(r)
	}
}

// OrphanAge is how long a run left running that this office does not run
// waits before it is taken back (or ended).
const OrphanAge = 10 * time.Minute

// WatchOrphans looks, every so often, for runs left "running" that nobody
// runs (a crash in between, a bug): they go on when they can, else end, so
// they do not hold an update waiting for idle for hours (ADR-256).
func (e *Engine) WatchOrphans(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !e.stopping.Load() {
				e.sweepOrphans(ctx, OrphanAge)
			}
		}
	}
}

// sweepOrphans takes back the runs older than age that nobody runs, seen so
// by the sweep before too: a run a chat called goes on when it can, the
// others end. It returns how many it handled.
func (e *Engine) sweepOrphans(ctx context.Context, age time.Duration) int {
	runs, err := e.store.WorkflowRuns().ListRunning(ctx)
	if err != nil {
		return 0
	}
	byID := map[string]storage.WorkflowRun{}
	for _, r := range runs {
		byID[r.ID] = r
	}
	// a run is taken back only when the sweep before saw it orphaned too: a
	// run ending right now is out of memory a moment before it is saved
	e.wf.mu.Lock()
	seen := e.wf.orphans
	e.wf.orphans = map[string]bool{}
	e.wf.mu.Unlock()
	n := 0
	for _, r := range runs {
		if r.Status != storage.RunRunning || time.Since(r.StartedAt) < age || e.runHeld(r) != nil {
			continue
		}
		e.wf.mu.Lock()
		e.wf.orphans[r.ID] = true
		e.wf.mu.Unlock()
		if !seen[r.ID] {
			continue
		}
		if cur, err := e.store.WorkflowRuns().Get(ctx, r.ID); err != nil || cur.Status != storage.RunRunning || e.runHeld(cur) != nil {
			continue
		} else {
			r = cur
		}
		if r.ParentRunID != "" {
			if p, ok := byID[r.ParentRunID]; ok && p.Status == storage.RunRunning && e.runHeld(p) != nil {
				continue // its parent's step waits for it, or runs it again
			}
			e.endOrphan(ctx, r, "không còn quy trình cha nào chạy nó")
			n++
			continue
		}
		if _, err := e.resumeRun(ctx, r, nil, "lần chạy không còn ai giữ"); err != nil {
			e.endOrphan(ctx, r, "lần chạy không còn ai giữ, không chạy tiếp được: "+err.Error())
		}
		n++
	}
	return n
}

// ParkedTurns counts the answers in progress that only wait for a run a
// restart would not hurt (workflow.Parked): an update waiting for idle does
// not wait for them (ADR-256).
func (e *Engine) ParkedTurns() int {
	e.mu.Lock()
	convs := make([]string, 0, len(e.active))
	for id := range e.active {
		convs = append(convs, id)
	}
	e.mu.Unlock()
	n := 0
	ctx := context.Background()
	for _, id := range convs {
		run := e.calledFrom(id)
		if run == nil {
			continue
		}
		e.wf.mu.Lock()
		runID := run.rec.ID
		e.wf.mu.Unlock()
		if r, err := e.store.WorkflowRuns().Get(ctx, runID); err == nil && workflow.Parked(ctx, e.store, r) {
			n++
		}
	}
	return n
}

// WithoutRunCallers drops from jobs those that waited for a run that goes
// on (keep): their chats get the run's output when it ends, not a new
// answer (ResumeInterrupted).
func WithoutRunCallers(ctx context.Context, st storage.Store, jobs []storage.Job, keep map[string]bool) []storage.Job {
	callers := map[string]bool{}
	for id := range keep {
		if r, err := st.WorkflowRuns().Get(ctx, id); err == nil && r.Checkpoint != nil && r.Checkpoint.CallerJob != "" {
			callers[r.Checkpoint.CallerJob] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(jobs), func(j storage.Job) bool { return callers[j.ID] })
}
