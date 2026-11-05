package chat

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A run's supervisor and its roles' pushback (ADR-115, after the
// supervisor / lead / peer setup): every so often a read-only role reads the
// run's chat against what was asked and says whether the work drifts; drift
// is told to the coordinator when it is called back (no turn is cut into,
// ADR-104). A role that finds its brief wrong says so on its first line
// (PHẢN BIỆN) and the coordinator must answer it before the run ends.

// watchEvery is how often a run's supervisor checks (tests shorten it).
var watchEvery = workflow.Def.SuperviseEvery

// watchRun starts the run's supervisor once its first role works.
func (e *Engine) watchRun(run *wfRun, conv storage.Conversation, project storage.Repo) {
	every := watchEvery(run.def)
	e.wf.mu.Lock()
	on := run.watchOn
	run.watchOn = true
	e.wf.mu.Unlock()
	if every <= 0 || on {
		return
	}
	safeGo("watchRun", func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-run.done:
				return
			case <-t.C:
				if !e.superviseOnce(run, conv, project) {
					return
				}
			}
		}
	})
}

// superviseOnce starts one check by the supervisor; false: it never can
// (no agent fills its seat).
func (e *Engine) superviseOnce(run *wfRun, conv storage.Conversation, project storage.Repo) bool {
	ctx := context.Background()
	role := run.def.Supervise.Role
	e.wf.mu.Lock()
	if e.wf.byConv[conv.ID] != run || run.watching {
		e.wf.mu.Unlock()
		return true
	}
	if b := run.def.Limits.BudgetUSD; b > 0 && run.rec.CostUSD >= b {
		e.wf.mu.Unlock()
		return true
	}
	e.wf.mu.Unlock()
	a, err := e.pickAgent(ctx, run, role, "")
	if err != nil {
		e.wf.mu.Lock()
		if e.wf.byConv[conv.ID] != run {
			e.wf.mu.Unlock()
			return false
		}
		text := "Giám sát không chạy: " + err.Error() // i18n-ignore
		run.notes = append(run.notes, "The supervisor could not run: "+err.Error())
		run.logf("%s", text)
		e.wf.mu.Unlock()
		return false
	}
	msgs, err := e.store.Chat().ListMessages(ctx, conv.ID)
	if err != nil {
		return true
	}
	last := ""
	for i := len(msgs) - 1; i >= 0; i-- { // what others said last: its own answers are not news
		if m := msgs[i]; (m.Role == "user" || m.Role == "assistant") && m.Author != a.Name {
			last = m.ID
			break
		}
	}
	e.wf.mu.Lock()
	if e.wf.byConv[conv.ID] != run {
		e.wf.mu.Unlock()
		return false
	}
	if last == "" || last == run.watchSeen || run.watching {
		e.wf.mu.Unlock()
		return true
	}
	type roleView struct{ Name, Agent, Status string }
	var roles []roleView
	for _, r := range run.rec.Roles {
		if r.Role != role {
			roles = append(roles, roleView{r.Name, cmp.Or(r.AgentName, "unassigned"), roleStatus(r.Status)})
		}
	}
	d, _ := run.def.Role(role)
	input := cmp.Or(run.rec.Input, "(nothing more)")
	run.watching, run.watchSeen = true, last
	ceiling := perm.Read
	if run.ceiling != "" {
		ceiling = perm.Min(ceiling, run.ceiling)
	}
	e.wf.mu.Unlock()
	prompt := prompts.Render("workflow/supervise", struct {
		Workflow, Role, Input, Activity, OK, Drift string
		Roles                                      []roleView
	}{run.def.Name, d.Name, input, callerContext(msgs), watchOK, watchDrift, roles})
	t, _ := e.startTurn(conv, project, turnSpec{agent: a, background: true, delegator: run.coord.ID, actor: run.rec.Actor, ceiling: ceiling, tree: run.tree,
		title: "Giám sát " + run.def.Name + " → " + a.Name, prompt: prompt, wfRun: run.rec.ID, wfRole: role, wfWatch: true, trusted: run.trusted}) // i18n-ignore
	if t == nil { // busy: next time
		e.wf.mu.Lock()
		run.watching, run.watchSeen = false, ""
		e.wf.mu.Unlock()
	}
	return true
}

// The first line of a supervisor's answer.
const (
	watchOK    = "SUPERVISOR: OK"
	watchDrift = "SUPERVISOR: DRIFT"
)

// wfWatched takes a supervisor's answer: drift is told to the coordinator.
func (e *Engine) wfWatched(prev *Turn, conv storage.Conversation, reply string) {
	e.wf.mu.Lock()
	run := e.wf.byConv[conv.ID]
	if run == nil || run.rec.ID != prev.wfRun {
		e.wf.mu.Unlock()
		return
	}
	run.watching = false
	what, drift := driftOf(reply)
	if prev.err != "" || !drift {
		e.wf.mu.Unlock()
		return
	}
	text := fmt.Sprintf("👁 The supervisor (%s) sees drift: %s. Read its message in this chat; if it is right, correct the direction.", prev.agentName, what)
	run.notes = append(run.notes, text)
	run.logf("Giám sát: lệch hướng: %s", truncate(what, 200))
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
}

// driftOf reads a supervisor's verdict from its first line (anything but a
// clear drift is no drift: it is not to stop the run on a guess); the older
// GIÁM SÁT: LỆCH reads too.
func driftOf(text string) (string, bool) {
	line := firstLine(text)
	u := strings.ToUpper(line)
	if strings.HasPrefix(u, watchOK) {
		return "", false
	}
	i, n := strings.Index(u, "DRIFT"), len("DRIFT")
	if i < 0 {
		i, n = strings.Index(u, "LỆCH"), len("LỆCH")
	}
	if i < 0 {
		i, n = strings.Index(u, "LECH"), len("LECH")
	}
	if i < 0 {
		return "", false
	}
	what := line
	if len(u) == len(line) { // what follows the verdict
		what = strings.TrimSpace(strings.TrimLeft(line[i+n:], ":–- "))
	}
	return truncate(cmp.Or(what, "xem câu trả lời"), 300), true // i18n-ignore
}

// objectionMark starts the first line of a role pushing back on its brief
// (the older PHẢN BIỆN reads too).
const objectionMark = "OBJECTION"

// objectionOf is what a role pushes back on, from its first line
// ("PHẢN BIỆN: …"); "" = none.
func objectionOf(text string) string {
	line := firstLine(text)
	u := strings.ToUpper(line)
	for _, p := range []string{"PHẢN BIỆN", "PHAN BIEN", objectionMark} {
		if strings.HasPrefix(u, p) {
			rest := strings.TrimSpace(strings.TrimLeft(line[len(p):], ":–-*`_ "))
			return truncate(cmp.Or(rest, "xem câu trả lời"), 300) // i18n-ignore
		}
	}
	return ""
}

// firstLine is a reply's first line with no Markdown around it.
func firstLine(text string) string {
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if l = strings.Trim(strings.TrimSpace(l), "*`_#> "); l != "" {
			return l
		}
	}
	return ""
}

// superviseBrief is what a supervisor's turn is told besides its prompt
// (workflow/supervisor.md).
func superviseBrief(name, workflowName string) string {
	return "\n\n" + prompts.Render("workflow/supervisor", struct{ Role, Workflow string }{name, workflowName}) + "\n"
}
