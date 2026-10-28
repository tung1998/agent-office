package chat

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// Workflows (spec 2026-10-07-workflows-design): "#key …" (or "/key …") in a chat starts
// one in a chat of its own (purpose "workflow_run", not listed); the chat it
// was called from shows only the input and, when it ends, the output. In the
// run's chat the agent coordinates it with the workflow_* tools and the roles
// answer in the background like hand-offs; no person writes there. What the workflow's
// header says (roles, their permission, limits, the brief, gates, votes) is
// enforced here; the body only guides the coordinator.

// wfRun is a workflow running in a chat.
type wfRun struct {
	rec      storage.WorkflowRun
	def      workflow.Def
	bindings map[string]string // role → agent id the project chose
	coord    storage.Agent
	tier     string          // the model tier of the message that started it
	ceiling  string          // the most that message may have run (a bot's user…)
	tree     treeOpt         // the worktree that message ran in (a Burn piece's): its roles read there too
	deadline time.Time       // the run's expected end (past it the chat is told; it goes on)
	timer    *time.Timer     // fires at the deadline
	batch    map[string]bool // roles answering now; when none is left the coordinator is called back
	voting   *wfVoting       // a vote in progress
	notes    []string        // what the coordinator is told when called back (a role failed…)
	tries    int             // call-backs that found the chat busy
	owed     bool            // a role answered while another was asked and waited for: the coordinator is called back for it
	done     chan struct{}   // closed when the run ends
	// sub-workflows (ADR-102): how many levels may still go below this run,
	// and the deadline of the run that called it (zero: called from a chat)
	depthLeft int
	until     time.Time
	// the run whose role called it and that role (nil: called from a chat):
	// its cost goes up the tree as it is spent, the tree's budgets and
	// concurrency bound it (ADR-103)
	parent     *wfRun
	parentRole string
	// a graph of steps (ADR-108): the values its caller gave by key, and
	// what stops it when the run ends
	inputs map[string]string
	stop   context.CancelFunc
	// its supervisor (ADR-115): started, checking now, and the last message
	// it was shown
	watchOn, watching bool
	watchSeen         string
	// saveRun ordering: seq is bumped (with e.wf.mu held) each time a
	// snapshot is taken; savedSeq is the seq of the last write that actually
	// landed, guarded by saveMu, so a stale snapshot can never overwrite a
	// newer one that reached sqlite first
	seq      int
	savedSeq int
	saveMu   sync.Mutex
}

// snapshot copies the run's record and bumps its save sequence; call with
// e.wf.mu held, right where "rec := run.rec" used to be captured.
func (r *wfRun) snapshot() (storage.WorkflowRun, int) {
	r.seq++
	return r.rec, r.seq
}

// rootOf is the top of a run's tree (with the lock held).
func rootOf(r *wfRun) *wfRun {
	for r.parent != nil {
		r = r.parent
	}
	return r
}

// RunPurpose is the purpose of a workflow run's own chat.
const RunPurpose = "workflow_run"

// BurnReviewPurpose is the purpose of a Burn piece's review chat (ADR-114):
// hidden like a run's, nobody writes there; it may run a workflow.
const BurnReviewPurpose = "burn_review"

// BurnWorkPurpose is the purpose of the hidden chat a Burn piece is done in
// (ADR-116): the burn_* tools work there, for that piece only.
const BurnWorkPurpose = "burn_work"

// GoalJudgePurpose is the purpose of the hidden, read-only chat that judges
// whether an automation's goal is reached (ADR-132).
const GoalJudgePurpose = "goal_judge"

// prepared is a run the chat that called it starts in the run's own chat.
type prepared struct {
	conv   string // the run's chat
	run    *wfRun
	prompt string
}

type preparedKey struct{}

func withPrepared(ctx context.Context, p *prepared) context.Context {
	return context.WithValue(ctx, preparedKey{}, p)
}

func preparedOf(ctx context.Context, conversationID string) *prepared {
	if p, _ := ctx.Value(preparedKey{}).(*prepared); p != nil && p.conv == conversationID {
		return p
	}
	return nil
}

// calledFrom is the run a chat called that is still running (nil = none).
func (e *Engine) calledFrom(conversationID string) *wfRun {
	e.wf.mu.Lock()
	defer e.wf.mu.Unlock()
	for _, r := range e.wf.byConv {
		if r.rec.CallerConversationID == conversationID {
			return r
		}
	}
	return nil
}

// wfVoting is a vote waiting for its roles' answers.
type wfVoting struct {
	question string
	ballots  map[string]string // role → yes | no | unclear
}

// wfAsk is a turn the coordinator asked for, started once its answer is done.
type wfAsk struct {
	role   string
	agent  storage.Agent
	prompt string
	effort string // how hard it thinks ("" = the agent's own)
	vote   bool
	flow   bool              // the role is a sub-workflow: the turn is a run of it
	inputs map[string]string // a sub-workflow's inputs by key
}

// wfState is the engine's running workflows.
type wfState struct {
	mu      sync.Mutex
	byConv  map[string]*wfRun      // conversation id → its running workflow
	pending map[string][]wfAsk     // coordinator turn id → turns to start after it
	waiting map[string]chan string // run id/role → a coordinator waiting for that role's answer (workflow_ask)
}

func (s *wfState) init() {
	if s.byConv == nil {
		s.byConv, s.pending, s.waiting = map[string]*wfRun{}, map[string][]wfAsk{}, map[string]chan string{}
	}
}

// accessLevel is the permission a role's access caps its turns at ("" = no
// cap: the agent's own permission).
func accessLevel(access string) string {
	switch access {
	case workflow.AccessAgent:
		return ""
	case workflow.AccessEdit:
		return perm.Edit
	case workflow.AccessPropose:
		return perm.Propose
	}
	return perm.Read
}

func accessLabel(access string) string {
	switch access {
	case workflow.AccessAgent:
		return "its agent's own permission"
	case workflow.AccessEdit:
		return "edits in its own worktree"
	case workflow.AccessPropose:
		return "proposes, the person approves"
	}
	return "analyze only"
}

// family is the vendor family of a connection (workflow.Family).
func family(p storage.Provider) string { return workflow.Family(p) }

// agentFamily is the family of the connection an agent's turns run on first.
func (e *Engine) agentFamily(ctx context.Context, a storage.Agent) (string, string) {
	if e.providers == nil {
		return "", ""
	}
	c, err := e.choices(ctx, a)
	if err != nil || len(c) == 0 {
		return "", ""
	}
	return family(c[0].Provider), c[0].Provider.Name
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// ---- starting ----

// ErrWorkflowRunning: the chat already runs a workflow.
var ErrWorkflowRunning = errors.New("cuộc chat đang chạy một quy trình; đợi xong hoặc bấm Dừng")

// prepWorkflow reads "/key rest" as a workflow of the project. It is not one
// (handled false) when no enabled workflow has that key; the skill of that
// name, if any, then runs as before.
func (e *Engine) prepWorkflow(ctx context.Context, conv storage.Conversation, coord storage.Agent, text string) (*wfRun, string, bool, error) {
	name, rest, isCall := workflow.ParseCall(text) // "#key", or "/key" as before
	sign := workflow.Prefix
	if !isCall {
		name, rest, isCall = automation.ParseSkillCall(text)
		sign = "/"
	}
	if !isCall || !teamChat(conv) && conv.Purpose != BurnReviewPurpose { // a Burn's reviewer runs one too (ADR-113)
		return nil, "", false, nil
	}
	if conv.Purpose == "channel" && name != skillOf(ctx) {
		return nil, "", false, nil // a bot's chat runs only what its command names
	}
	w, err := e.store.Workflows().GetByKey(ctx, conv.ProjectID, name)
	if key, ok := workflow.Renamed[name]; ok && errors.Is(err, storage.ErrNotFound) {
		w, err = e.store.Workflows().GetByKey(ctx, conv.ProjectID, key) // the old Vietnamese command still works
	}
	if err != nil || !w.Enabled {
		return nil, "", false, nil
	}
	def, err := workflow.Parse(w.Source)
	if err != nil {
		return nil, "", true, fmt.Errorf("quy trình %s%s không hợp lệ: %w", sign, name, err)
	}
	if def.Callable == workflow.CallableSub {
		return nil, "", true, fmt.Errorf("quy trình %s%s chỉ để quy trình khác gọi (callable: sub)", sign, def.Key)
	}
	if e.calledFrom(conv.ID) != nil {
		return nil, "", true, ErrWorkflowRunning
	}
	run, prompt, err := e.newRun(ctx, conv.ProjectID, w, def, coord, rest)
	return run, prompt, true, err
}

// newRun prepares a run of workflow w coordinated by coord, asked input.
func (e *Engine) newRun(ctx context.Context, projectID string, w storage.Workflow, def workflow.Def, coord storage.Agent, rest string) (*wfRun, string, error) {
	agents, err := e.Agents(ctx, projectID)
	if err != nil {
		return nil, "", err
	}
	byID := map[string]storage.Agent{}
	for _, a := range agents {
		byID[a.ID] = a
	}
	run := &wfRun{def: def, bindings: map[string]string{}, coord: coord, batch: map[string]bool{}, done: make(chan struct{}), depthLeft: def.Limits.Depth}
	run.rec = storage.WorkflowRun{ProjectID: projectID, WorkflowID: w.ID, WorkflowKey: def.Key, WorkflowName: def.Name,
		BodyHash: hashOf(w.Source), CoordinatorID: coord.ID, CoordinatorName: coord.Name, Input: rest, Status: storage.RunRunning}
	for _, r := range def.Roles {
		rr := storage.RunRole{Role: r.Key, Name: r.Name, Access: r.Access, Status: "idle", Workflow: r.Workflow}
		if id := w.Bindings[r.Key]; id != "" { // a sub-workflow's: the agent coordinating it (the coordinator too)
			// the coordinator does not take a role it hands work to; a graph
			// of steps has no coordinator deciding, any agent fills a role
			if a, ok := byID[id]; ok && !a.Disabled && (a.ID != coord.ID || r.Workflow != "" || def.StepMode()) {
				run.bindings[r.Key] = a.ID
				rr.AgentID, rr.AgentName = a.ID, a.Name
			}
		}
		run.rec.Roles = append(run.rec.Roles, rr)
	}
	for _, g := range def.Gates {
		run.rec.Gates = append(run.rec.Gates, storage.RunGate{Key: g.Key, Name: g.Name, Kind: g.Kind, Required: g.Required, Status: "open"})
	}
	if warn := e.differWarnings(ctx, run, byID); len(warn) > 0 {
		if def.Strict {
			return nil, "", fmt.Errorf("quy trình /%s cần các vai dùng kết nối AI khác hãng: %s", def.Key, strings.Join(warn, "; "))
		}
		run.notes = append(run.notes, "Cảnh báo: "+strings.Join(warn, "; "))
	}
	input := strings.TrimSpace(rest)
	if input == "" {
		input = "(nothing more)"
	}
	prompt := prompts.Render("workflow/start", struct{ Name, Key, Input, Inputs string }{def.Name, def.Key, input, fieldList(def.Inputs)})
	return run, prompt, nil
}

// differWarnings: roles already filled whose agents share a vendor family
// they must not share.
func (e *Engine) differWarnings(ctx context.Context, run *wfRun, byID map[string]storage.Agent) []string {
	fam := func(role string) (string, string) {
		if role == workflow.Coordinator {
			return e.agentFamily(ctx, run.coord)
		}
		if a, ok := byID[run.bindings[role]]; ok {
			return e.agentFamily(ctx, a)
		}
		return "", ""
	}
	var out []string
	for _, r := range run.def.Roles {
		f, p := fam(r.Key)
		if f == "" || r.Workflow != "" {
			continue
		}
		for _, o := range r.DifferFrom {
			if g, q := fam(o); g != "" && g == f {
				out = append(out, fmt.Sprintf("vai %s (%s) và %s (%s) cùng hãng", r.Key, p, o, q))
			}
		}
	}
	return out
}

// startRun keeps a prepared run going once its first turn has started.
func (e *Engine) startRun(conv storage.Conversation, run *wfRun, turn *Turn) error {
	run.tree = turn.tree
	if err := e.beginRun(conv, run, turn.actor, turn.tier, turn.ceiling); err != nil {
		return err
	}
	e.mu.Lock()
	turn.wfRun = run.rec.ID // the chat shows the run's card where it started (the dashboard puts it by time)
	e.mu.Unlock()
	return nil
}

// beginRun records a run and keeps it going in its chat (with no turn: a
// graph of steps starts this way).
func (e *Engine) beginRun(conv storage.Conversation, run *wfRun, actorOf, tier, ceiling string) error {
	ctx := context.Background()
	run.tier, run.ceiling = tier, ceiling
	run.deadline = time.Now().Add(run.def.Timeout())
	if !run.until.IsZero() && run.until.Before(run.deadline) { // no later than the run that called it
		run.deadline = run.until
	}
	run.rec.ConversationID = conv.ID // its own chat (the caller's is CallerConversationID)
	run.rec.Actor, run.rec.StartedAt = actorOf, time.Now().UTC()
	start := "Bắt đầu, " + run.coord.Name + " điều phối"
	if run.def.StepMode() {
		start = "Bắt đầu, chạy theo các bước"
	}
	run.rec.Log = append(run.rec.Log, storage.RunLog{At: run.rec.StartedAt, Text: start})
	for _, n := range run.notes {
		run.rec.Log = append(run.rec.Log, storage.RunLog{At: run.rec.StartedAt, Text: n})
	}
	rec, err := e.store.WorkflowRuns().Create(ctx, run.rec)
	if err != nil {
		return fmt.Errorf("workflow: save run: %w", err)
	}
	run.rec = rec
	e.wf.mu.Lock()
	e.wf.init()
	e.wf.byConv[conv.ID] = run
	run.timer = time.AfterFunc(time.Until(run.deadline), func() { e.wfExpire(conv.ID, rec.ID) })
	e.wf.mu.Unlock()
	return nil
}

// runOf is the chat's running workflow (nil = none).
func (e *Engine) runOf(conversationID string) *wfRun {
	e.wf.mu.Lock()
	defer e.wf.mu.Unlock()
	return e.wf.byConv[conversationID]
}

// RunningWorkflow is the id of the workflow a chat runs ("" = none).
func (e *Engine) RunningWorkflow(conversationID string) string {
	if r := e.runOf(conversationID); r != nil {
		return r.rec.ID
	}
	return ""
}

// save writes the run as it is now (call without the lock, with a snapshot
// from run.snapshot()). Writes are serialized per run and a seq older than
// the last one actually saved is dropped, so an out-of-order write from a
// slower goroutine can never revert a newer state already persisted.
func (e *Engine) saveRun(run *wfRun, rec storage.WorkflowRun, seq int) {
	run.saveMu.Lock()
	defer run.saveMu.Unlock()
	if seq <= run.savedSeq {
		return
	}
	if err := e.store.WorkflowRuns().Update(context.Background(), rec); err != nil {
		slog.Warn("workflow: save run", "run", rec.ID, "err", err)
		return
	}
	run.savedSeq = seq
}

// logRun adds a line to the run's log (with the lock held).
func (r *wfRun) logf(format string, a ...any) {
	r.rec.Log = append(r.rec.Log, storage.RunLog{At: time.Now().UTC(), Text: fmt.Sprintf(format, a...)})
	if len(r.rec.Log) > 200 {
		r.rec.Log = r.rec.Log[len(r.rec.Log)-200:]
	}
}

func (r *wfRun) role(key string) *storage.RunRole {
	for i := range r.rec.Roles {
		if r.rec.Roles[i].Role == key {
			return &r.rec.Roles[i]
		}
	}
	return nil
}

func (r *wfRun) gate(key string) *storage.RunGate {
	for i := range r.rec.Gates {
		if r.rec.Gates[i].Key == key {
			return &r.rec.Gates[i]
		}
	}
	return nil
}

// finishRun ends a run (status done, failed or stopped).
func (e *Engine) finishRun(conversationID, runID, status, result, errText string) {
	e.wf.mu.Lock()
	run := e.wf.byConv[conversationID]
	if run == nil || run.rec.ID != runID {
		e.wf.mu.Unlock()
		return
	}
	delete(e.wf.byConv, conversationID)
	if run.timer != nil {
		run.timer.Stop()
	}
	defer close(run.done)
	if run.stop != nil {
		defer run.stop() // a graph of steps stops where it is
	}
	now := time.Now().UTC()
	run.rec.Status, run.rec.FinishedAt = status, &now
	run.rec.Result, run.rec.Error = result, errText
	// a role still answering (an ask gone on in the background) ends with
	// the run: its earlier answer stands, else it did not finish
	for i := range run.rec.Roles {
		if r := &run.rec.Roles[i]; r.Status == "working" {
			r.Status = "failed"
			if r.Rounds > 0 {
				r.Status = "done"
			}
		}
	}
	switch status {
	case storage.RunDone:
		run.logf("Xong")
	case storage.RunStopped:
		run.logf("Đã dừng")
	default:
		run.logf("Thất bại: %s", errText)
	}
	rec, seq := run.snapshot()
	var children []string // its sub-workflows still running end with it
	for conv, r := range e.wf.byConv {
		if r.rec.ParentRunID == runID {
			children = append(children, conv)
		}
	}
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	for _, c := range children {
		e.StopAll(c)
	}
	e.cancelRoleTurns(runID)
}

// cancelRoleTurns stops the roles' turns of a run still answering (not the
// coordinator's: it may be the one ending the run).
func (e *Engine) cancelRoleTurns(runID string) {
	e.mu.Lock()
	var stop []*Turn
	for _, t := range e.turns {
		if t.wfRun == runID && t.wfRole != "" {
			stop = append(stop, t)
		}
	}
	e.mu.Unlock()
	for _, t := range stop {
		t.Cancel()
	}
}

// wfExpire tells the chat a run went past its expected time. It does not
// stop it: a gate may wait on the person for hours; the person stops it.
func (e *Engine) wfExpire(conversationID, runID string) {
	e.wf.mu.Lock()
	run := e.wf.byConv[conversationID]
	if run == nil || run.rec.ID != runID {
		e.wf.mu.Unlock()
		return
	}
	run.logf("Quá thời gian dự kiến, vẫn chạy tiếp")
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	if conv, err := e.store.Chat().GetConversation(context.Background(), conversationID); err == nil {
		e.note(conv, "⏱️ Quy trình "+run.def.Name+" đã quá thời gian dự kiến nhưng vẫn chạy tiếp; dừng nó nếu không cần nữa.")
	}
}

// stopRun: the person stopped the chat.
func (e *Engine) stopRun(conversationID string) {
	if r := e.runOf(conversationID); r != nil {
		e.finishRun(conversationID, r.rec.ID, storage.RunStopped, "", "")
	}
}

// ---- the coordinator's turn ----

// coordinatorOf: turn is the coordinator answering in its chat's running
// workflow (the person's messages and the call-backs while it runs).
func (e *Engine) coordinatorOf(conversationID string, turn *Turn, agentID string) *wfRun {
	run := e.runOf(conversationID)
	if run == nil || turn.background || run.coord.ID != agentID {
		return nil
	}
	e.mu.Lock()
	turn.wfRun = run.rec.ID
	e.mu.Unlock()
	return run
}

// wfBrief is what the coordinator is told about its workflow
// (workflow/coordinator.md).
func (e *Engine) wfBrief(ctx context.Context, run *wfRun, projectID string) string {
	e.refreshGates(ctx, run)
	e.wf.mu.Lock()
	rec, def := run.rec, run.def
	left := def.Limits.Turns - rec.Turns
	deadline := run.deadline
	e.wf.mu.Unlock()
	type roleView struct {
		Role, Name, Workflow, Who, Access, Status, Every string
		DifferFrom, Hint, Objection, Prefer              string
		Rounds                                           int
		Flow, Supervisor                                 bool
	}
	type gateView struct {
		Key, Name, Kind, State string
		Required               bool
	}
	var roles []roleView
	for _, r := range rec.Roles {
		who := r.AgentName
		if who == "" {
			who = "unassigned: pick an agent with the agent parameter when delegating"
		}
		d, _ := def.Role(r.Role)
		v := roleView{Role: r.Role, Name: r.Name, Who: who, Access: accessLabel(r.Access), Status: roleStatus(r.Status), Rounds: r.Rounds}
		switch sv := def.Supervise; {
		case d.Workflow != "":
			v.Flow, v.Workflow = true, d.Workflow
			if r.AgentName == "" {
				v.Who = "you"
			}
		case sv != nil && sv.Role == r.Role:
			v.Supervisor, v.Who, v.Every = true, cmp.Or(r.AgentName, "unassigned"), def.SuperviseEvery().String()
		default:
			v.DifferFrom, v.Hint, v.Objection = strings.Join(d.DifferFrom, ", "), d.Hint, r.Objection
			if p := d.Prefer; p != nil {
				v.Prefer = strings.Trim(p.Tier+" "+p.Family, " ")
			}
		}
		roles = append(roles, v)
	}
	var parallel []string
	for _, p := range def.Parallel {
		parallel = append(parallel, strings.Join(p, " + "))
	}
	var gates []gateView
	for _, g := range rec.Gates {
		kind := "the person approves"
		if g.Kind == workflow.GateCheck {
			kind = "a check command must pass"
			if gd, _ := def.Gate(g.Key); gd.Command != "" {
				kind += ": " + gd.Command
			}
		}
		gates = append(gates, gateView{g.Key, g.Name, kind, gateState(g.Status), g.Required})
	}
	var agents []string
	all, err := e.Agents(ctx, projectID)
	if err == nil {
		for _, a := range storage.OnAgents(all) {
			if a.ID == run.coord.ID {
				continue
			}
			_, prov := e.agentFamily(ctx, a)
			agents = append(agents, fmt.Sprintf("%s (%s, %s, permission %s)", a.Name, cmp.Or(a.Role, "—"), cmp.Or(prov, "?"), perm.Label(perm.Agent(a))))
		}
	}
	outputs := ""
	if len(def.Outputs) > 0 {
		outputs = fieldList(def.Outputs)
	}
	return "\n\n" + prompts.Render("workflow/coordinator", struct {
		Name, Key, Description, Input, Body, Outputs, Ends, ObjectionMark string
		Roles                                                             []roleView
		Gates                                                             []gateView
		Parallel, Agents                                                  []string
		Vote                                                              *workflow.Vote
		HasSub, HasBudget, HasAgents                                      bool
		DepthLeft, Left, Rounds                                           int
		Budget, Spent                                                     float64
	}{
		Name: def.Name, Key: def.Key, Description: def.Description, Input: cmp.Or(rec.Input, "(nothing more)"), Body: def.Body,
		Outputs: outputs, Ends: deadline.Format("15:04"), ObjectionMark: objectionMark,
		Roles: roles, Gates: gates, Parallel: parallel, Agents: agents, Vote: def.Vote,
		HasSub:    slices.ContainsFunc(def.Roles, func(r workflow.Role) bool { return r.Workflow != "" }),
		HasBudget: def.Limits.BudgetUSD > 0, HasAgents: err == nil,
		DepthLeft: run.depthLeft, Left: left, Rounds: def.Limits.Rounds, Budget: def.Limits.BudgetUSD, Spent: rec.CostUSD,
	}) + "\n"
}

func roleStatus(s string) string {
	switch s {
	case "working":
		return "working"
	case "done":
		return "done"
	case "failed":
		return "failed"
	}
	return "not delegated"
}

func gateStatus(s string) string {
	switch s {
	case "waiting":
		return "đang chờ"
	case "passed":
		return "đã qua"
	case "failed":
		return "không đạt"
	}
	return "chưa mở"
}

// gateState is a gate's status as the coordinator is told.
func gateState(s string) string {
	switch s {
	case "waiting":
		return "waiting"
	case "passed":
		return "passed"
	case "failed":
		return "failed"
	}
	return "not opened"
}

// ---- the tools ----

// WorkflowScope tells the toolbox what a run of sc may use: coordinator =
// the workflow_* tools; inRun = a turn of a running workflow (no delegate).
func (e *Engine) WorkflowScope(sc officetools.Scope) (coordinator, inRun bool) {
	e.mu.Lock()
	t := e.turns[sc.RunRef]
	e.mu.Unlock()
	if t == nil || t.wfRun == "" {
		return false, false
	}
	return t.wfRole == "", true
}

// WorkflowCall runs a workflow_* tool for the coordinator in sc.
func (e *Engine) WorkflowCall(ctx context.Context, sc officetools.Scope, name string, raw json.RawMessage) (string, error) {
	e.mu.Lock()
	t := e.turns[sc.RunRef]
	e.mu.Unlock()
	if t == nil || t.wfRun == "" || t.wfRole != "" {
		return "", errors.New("chỉ agent điều phối của một quy trình đang chạy mới dùng được công cụ này")
	}
	run := e.runOf(sc.ConversationID)
	if run == nil || run.rec.ID != t.wfRun {
		return "", errors.New("quy trình đã kết thúc")
	}
	var in struct {
		Role     string            `json:"role"`
		Agent    string            `json:"agent"`
		Brief    map[string]string `json:"brief"`
		Effort   string            `json:"effort"`
		Message  string            `json:"message"`
		Question string            `json:"question"`
		Agents   map[string]string `json:"agents"`
		Gate     string            `json:"gate"`
		Note     string            `json:"note"`
		Command  string            `json:"command"`
		Summary  string            `json:"summary"`
		Outputs  map[string]string `json:"outputs"`
		Overrule string            `json:"overrule"`
		Wait     int               `json:"wait_seconds"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return "", fmt.Errorf("tham số không hợp lệ: %w", err)
		}
	}
	switch name {
	case "workflow_delegate":
		return e.wfDelegate(ctx, sc, run, in.Role, in.Agent, in.Effort, in.Brief)
	case "workflow_ask":
		return e.wfAskNow(ctx, sc, run, in.Role, in.Question, in.Wait)
	case "workflow_send":
		return e.wfSend(ctx, sc, run, in.Role, in.Message)
	case "workflow_vote":
		return e.wfVote(ctx, sc, run, in.Question, in.Agents)
	case "workflow_gate":
		return e.wfGate(ctx, sc, run, in.Gate, in.Note, in.Command, in.Role)
	case "workflow_done":
		return e.wfDone(ctx, sc, run, in.Summary, in.Outputs, in.Overrule)
	}
	return "", fmt.Errorf("không có công cụ %s", name)
}

// queued are the asks of this coordinator turn not yet started.
func (e *Engine) queuedAsks(turnID string) []wfAsk {
	return e.wf.pending[turnID]
}

// pickAgent fills a role: the name given, else the project's binding.
func (e *Engine) pickAgent(ctx context.Context, run *wfRun, role, name string) (storage.Agent, error) {
	agents, err := e.Agents(ctx, run.rec.ProjectID)
	if err != nil {
		return storage.Agent{}, err
	}
	var a storage.Agent
	if name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "@"))); name != "" {
		i := slices.IndexFunc(agents, func(x storage.Agent) bool { return strings.ToLower(x.Name) == name || strings.ToLower(x.Key) == name })
		if i < 0 {
			return a, fmt.Errorf("không có agent %q trong project", name)
		}
		a = agents[i]
	} else if r := run.role(role); r != nil && r.AgentID != "" {
		i := slices.IndexFunc(agents, func(x storage.Agent) bool { return x.ID == r.AgentID })
		if i < 0 {
			return a, fmt.Errorf("agent của vai %s không còn trong project", role)
		}
		a = agents[i]
	} else {
		var names []string
		for _, x := range storage.OnAgents(agents) {
			if x.ID != run.coord.ID || run.def.StepMode() {
				names = append(names, x.Name)
			}
		}
		return a, fmt.Errorf("vai %s chưa gán agent: gọi lại với tham số agent (một trong: %s)", role, strings.Join(names, ", "))
	}
	if a.Disabled {
		return a, errors.New(storage.OffNotice(a.Name))
	}
	if a.ID == run.coord.ID && !run.def.StepMode() {
		return a, errors.New("agent điều phối không tự nhận vai; chọn agent khác")
	}
	return a, nil
}

// checkDiffer: agent a in role must not share a vendor family with the
// roles it must differ from (or, for those, with role).
func (e *Engine) checkDiffer(ctx context.Context, run *wfRun, role string, a storage.Agent) error {
	return e.checkDifferAmong(ctx, run, role, a, nil)
}

// checkDifferAmong is checkDiffer, but also consults picked for roles being
// assigned in the same batch (e.g. a vote) whose AgentID isn't saved yet.
func (e *Engine) checkDifferAmong(ctx context.Context, run *wfRun, role string, a storage.Agent, picked map[string]storage.Agent) error {
	fam, prov := e.agentFamily(ctx, a)
	if fam == "" {
		return nil
	}
	against := func(other string) (storage.Agent, bool) {
		if other == workflow.Coordinator {
			return run.coord, true
		}
		if ag, ok := picked[other]; ok {
			return ag, true
		}
		if r := run.role(other); r != nil && r.AgentID != "" {
			ag, err := e.store.Agents().Get(ctx, r.AgentID)
			return ag, err == nil
		}
		return storage.Agent{}, false
	}
	d, _ := run.def.Role(role)
	if d.Workflow != "" {
		return nil
	}
	others := slices.Clone(d.DifferFrom)
	for _, r := range run.def.Roles { // roles that must differ from this one
		if slices.Contains(r.DifferFrom, role) {
			others = append(others, r.Key)
		}
	}
	for _, o := range others {
		if b, ok := against(o); ok && b.ID != "" {
			if f, q := e.agentFamily(ctx, b); f == fam {
				msg := fmt.Sprintf("vai %s (%s) phải khác hãng model với %s (%s, %s)", role, prov, o, b.Name, q)
				if run.def.Strict {
					return errors.New(msg + "; chọn agent khác")
				}
				run.notes = append(run.notes, fmt.Sprintf("Warning: role %s (%s) must use a different model vendor than %s (%s, %s)", role, prov, o, b.Name, q))
			}
		}
	}
	return nil
}

func (e *Engine) wfDelegate(ctx context.Context, sc officetools.Scope, run *wfRun, role, agentName, effort string, brief map[string]string) (string, error) {
	d, ok := run.def.Role(role)
	if !ok {
		return "", fmt.Errorf("quy trình không có vai %q (có: %s)", role, roleKeys(run.def))
	}
	if !storage.ValidEffort(effort) {
		return "", fmt.Errorf("effort %q không hợp lệ (có: %s)", effort, strings.Join(storage.Efforts, ", "))
	}
	if d.Workflow != "" {
		return e.wfDelegateFlow(ctx, sc, run, d, agentName, brief)
	}
	if sv := run.def.Supervise; sv != nil && sv.Role == role {
		return "", fmt.Errorf("vai %s là giám sát: office tự gọi nó định kỳ, không giao việc cho nó", role)
	}
	a, err := e.pickAgent(ctx, run, role, agentName)
	if err != nil {
		return "", err
	}
	prompt, err := run.def.RenderBrief(d, brief)
	if err != nil {
		return "", err
	}
	e.wf.mu.Lock()
	r := run.role(role)
	if r.Status == "working" {
		e.wf.mu.Unlock()
		return "", fmt.Errorf("vai %s đang làm; đợi xong rồi gửi tiếp bằng workflow_send", role)
	}
	if err := e.canAsk(run, sc.RunRef, role, a); err != nil {
		e.wf.mu.Unlock()
		return "", err
	}
	e.wf.mu.Unlock()
	if err := e.checkDiffer(ctx, run, role, a); err != nil {
		return "", err
	}
	e.wf.mu.Lock()
	r = run.role(role)
	r.AgentID, r.AgentName, r.Effort = a.ID, a.Name, effort
	e.wf.pending[sc.RunRef] = append(e.wf.pending[sc.RunRef], wfAsk{role: role, agent: a, prompt: prompt, effort: effort})
	run.logf("Giao vai %s cho %s", d.Name, a.Name)
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	return fmt.Sprintf("Delegated role %s to %s; it starts when you finish this turn and works in the background (access: %s). Write a short note and stop your turn; you are called back when the roles just delegated are done.",
		d.Name, a.Name, accessLabel(d.Access)), nil
}

// canAsk checks a new turn for role (with the lock held): the turn limit,
// the budget, and what may answer at once.
func (e *Engine) canAsk(run *wfRun, turnID, role string, a storage.Agent) error {
	queued := e.queuedAsks(turnID)
	if run.rec.Turns+len(run.batch)+len(queued) >= run.def.Limits.Turns {
		return fmt.Errorf("đã hết %d lượt của các vai trong quy trình; tổng kết kết quả hiện có rồi gọi workflow_done", run.def.Limits.Turns)
	}
	for r := run; r != nil; r = r.parent { // its own budget and those of the runs above it
		if b := r.def.Limits.BudgetUSD; b > 0 && r.rec.CostUSD >= b {
			if r == run {
				return fmt.Errorf("quy trình đã dùng hết ngân sách $%.2f; tổng kết rồi gọi workflow_done", b)
			}
			return fmt.Errorf("quy trình cấp trên /%s đã dùng hết ngân sách $%.2f; tổng kết rồi gọi workflow_done", r.def.Key, b)
		}
	}
	root := rootOf(run) // roles answering at once in the whole tree
	busyN := len(queued)
	for _, x := range e.wf.byConv {
		if rootOf(x) == root {
			busyN += len(x.batch)
		}
	}
	if limit := root.def.Limits.Concurrency; limit > 0 && busyN >= limit {
		return fmt.Errorf("đã có %d vai đang làm trong cả cây quy trình (limits.concurrency %d); đợi bớt rồi giao tiếp", busyN, limit)
	}
	busy := map[string]bool{}
	for k := range run.batch {
		busy[k] = true
	}
	for _, q := range queued {
		if q.role == role && !q.vote {
			return fmt.Errorf("vai %s đã được giao trong lượt này", role)
		}
		busy[q.role] = true
	}
	for k := range busy {
		if k == role {
			continue
		}
		if !run.def.SameBatch(role, k) {
			return fmt.Errorf("vai %s đang/sắp làm và không được làm cùng lúc với %s (parallel của quy trình); đợi xong rồi giao tiếp", k, role)
		}
		if r := run.role(k); r != nil && r.AgentID == a.ID {
			return fmt.Errorf("%s đang làm vai %s; hai vai làm cùng lúc phải là hai agent khác nhau", a.Name, k)
		}
	}
	return nil
}

func roleKeys(d workflow.Def) string {
	keys := make([]string, 0, len(d.Roles))
	for _, r := range d.Roles {
		keys = append(keys, r.Key)
	}
	return strings.Join(keys, ", ")
}

func (e *Engine) wfSend(ctx context.Context, sc officetools.Scope, run *wfRun, role, message string) (string, error) {
	d, ok := run.def.Role(role)
	if !ok {
		return "", fmt.Errorf("quy trình không có vai %q (có: %s)", role, roleKeys(run.def))
	}
	if strings.TrimSpace(message) == "" {
		return "", errors.New("hãy ghi nội dung gửi")
	}
	e.wf.mu.Lock()
	r := run.role(role)
	if r.AgentID == "" || r.Status == "idle" {
		e.wf.mu.Unlock()
		return "", fmt.Errorf("vai %s chưa được giao; dùng workflow_delegate trước", role)
	}
	if r.Status == "working" {
		e.wf.mu.Unlock()
		return "", fmt.Errorf("vai %s đang làm; đợi xong", role)
	}
	if r.Rounds >= run.def.Limits.Rounds {
		e.wf.mu.Unlock()
		return "", fmt.Errorf("đã gửi tiếp cho vai %s %d lần (giới hạn); tổng kết với kết quả hiện có", role, r.Rounds)
	}
	agentID, effort := r.AgentID, r.Effort
	e.wf.mu.Unlock()
	if d.Workflow != "" { // a sub-workflow: it runs again with this as its input
		return e.wfAskFlow(ctx, sc, run, d, agentID, message, map[string]string{"text": message}, true)
	}
	a, err := e.store.Agents().Get(ctx, agentID)
	if err != nil {
		return "", err
	}
	if a.Disabled {
		return "", errors.New(storage.OffNotice(a.Name))
	}
	e.wf.mu.Lock()
	if err := e.canAsk(run, sc.RunRef, role, a); err != nil {
		e.wf.mu.Unlock()
		return "", err
	}
	run.role(role).Rounds++
	prompt := prompts.Render("workflow/send", struct{ Coordinator, Workflow, Role, Message, AccessNote string }{run.coord.Name, run.def.Name, d.Name, message, workflow.AccessNote(d.Access)})
	e.wf.pending[sc.RunRef] = append(e.wf.pending[sc.RunRef], wfAsk{role: role, agent: a, prompt: prompt, effort: effort})
	run.logf("Gửi tiếp cho %s (%s)", d.Name, a.Name)
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	return fmt.Sprintf("Sent to %s (role %s); it starts when you finish this turn. Write a short note and stop your turn.", a.Name, d.Name), nil
}

// The first line of a vote (ballot reads it).
const (
	ballotLine = "VOTE: AGREE"
	ballotNo   = "VOTE: DISAGREE"
)

func (e *Engine) wfVote(ctx context.Context, sc officetools.Scope, run *wfRun, question string, names map[string]string) (string, error) {
	v := run.def.Vote
	if v == nil {
		return "", errors.New("quy trình này không có biểu quyết")
	}
	if strings.TrimSpace(question) == "" {
		return "", errors.New("hãy ghi điều cần biểu quyết (kèm đủ bối cảnh: các vai không đọc được suy nghĩ của bạn)")
	}
	var asks []wfAsk
	seen := map[string]string{}
	picked := map[string]storage.Agent{}
	for _, role := range v.Roles {
		a, err := e.pickAgent(ctx, run, role, names[role])
		if err != nil {
			return "", fmt.Errorf("%w (truyền agents: {\"%s\": \"tên agent\"})", err, role)
		}
		if other, dup := seen[a.ID]; dup {
			return "", fmt.Errorf("%s đang bỏ phiếu ở vai %s; mỗi vai bỏ phiếu phải là một agent khác", a.Name, other)
		}
		if err := e.checkDifferAmong(ctx, run, role, a, picked); err != nil {
			return "", err
		}
		seen[a.ID] = role
		picked[role] = a
		d, _ := run.def.Role(role)
		prompt := prompts.Render("workflow/vote", struct{ Coordinator, Workflow, Role, Question, Agree, Disagree string }{
			run.coord.Name, run.def.Name, d.Name, question, ballotLine, ballotNo})
		asks = append(asks, wfAsk{role: role, agent: a, prompt: prompt, vote: true})
	}
	e.wf.mu.Lock()
	if run.voting != nil || len(run.batch) > 0 || len(e.queuedAsks(sc.RunRef)) > 0 {
		e.wf.mu.Unlock()
		return "", errors.New("đang có vai làm việc hoặc cuộc biểu quyết khác; đợi xong rồi biểu quyết")
	}
	if run.rec.Turns+len(asks) > run.def.Limits.Turns {
		e.wf.mu.Unlock()
		return "", fmt.Errorf("không đủ lượt cho %d phiếu (còn %d)", len(asks), run.def.Limits.Turns-run.rec.Turns)
	}
	for _, a := range asks {
		if r := run.role(a.role); r != nil {
			r.AgentID, r.AgentName = a.agent.ID, a.agent.Name
		}
	}
	run.voting = &wfVoting{question: question, ballots: map[string]string{}}
	e.wf.pending[sc.RunRef] = append(e.wf.pending[sc.RunRef], asks...)
	run.logf("Biểu quyết: %s", truncate(oneLine(question), 120))
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	return fmt.Sprintf("Sent the vote to %d roles; it starts when you finish this turn. Office counts the votes (%d AGREE needed) and calls you back with the result.", len(asks), v.Quorum), nil
}

// ballot reads a vote from the first lines of an answer (VOTE: AGREE |
// DISAGREE; the older PHIẾU: ĐỒNG Ý | KHÔNG ĐỒNG Ý too).
func ballot(text string) string {
	for _, line := range strings.SplitN(strings.TrimSpace(text), "\n", 4) {
		l := strings.ToUpper(strings.Trim(strings.TrimSpace(line), "*`_# "))
		switch {
		case strings.Contains(l, "DISAGREE"), strings.Contains(l, "KHÔNG ĐỒNG Ý"), strings.Contains(l, "KHONG DONG Y"), strings.Contains(l, "PHẢN ĐỐI"):
			return "no"
		case strings.Contains(l, "AGREE"), strings.Contains(l, "ĐỒNG Ý"), strings.Contains(l, "DONG Y"), strings.Contains(l, "TÁN THÀNH"):
			return "yes"
		}
	}
	return "unclear"
}

// tally is the outcome of a vote, as the coordinator is told.
func tally(v *workflow.Vote, ballots map[string]string) (bool, string) {
	yes, vetoed := 0, []string{}
	var lines []string
	for _, role := range v.Roles {
		b := ballots[role]
		label := map[string]string{"yes": "agree", "no": "disagree"}[b]
		if label == "" {
			label = "unclear (counted as disagree)"
		}
		lines = append(lines, role+": "+label)
		if b == "yes" {
			yes++
		} else if slices.Contains(v.Veto, role) {
			vetoed = append(vetoed, role)
		}
	}
	passed := yes >= v.Quorum && len(vetoed) == 0
	out := fmt.Sprintf("Vote result: %d/%d agree (%d needed) — %s.", yes, len(v.Roles), v.Quorum, strings.Join(lines, ", "))
	if len(vetoed) > 0 {
		out += " Vetoed by " + strings.Join(vetoed, ", ") + "."
	}
	if passed {
		return true, out + " PASSED."
	}
	return false, out + " NOT PASSED."
}

func (e *Engine) wfGate(ctx context.Context, sc officetools.Scope, run *wfRun, key, note, command, role string) (string, error) {
	g, ok := run.def.Gate(key)
	if !ok {
		return "", fmt.Errorf("quy trình không có cổng %q", key)
	}
	if e.office == nil || e.acts == nil {
		return "", errors.New("office không mở cổng được ở đây")
	}
	switch g.Kind {
	case workflow.GateApprove:
		if strings.TrimSpace(note) == "" {
			return "", errors.New("hãy ghi điều người dùng cần duyệt (note), đủ để họ quyết mà không phải đọc lại cả cuộc chat")
		}
		a, err := e.acts.Propose(ctx, sc, "workflow_gate", run.def.Name+": "+g.Name, note)
		if err != nil {
			return "", err
		}
		e.wf.mu.Lock()
		if rg := run.gate(key); rg != nil {
			rg.Status, rg.ActionID, rg.Detail = "waiting", a.ID, ""
		}
		run.logf("Mở cổng %s, chờ người dùng duyệt", g.Name)
		rec, seq := run.snapshot()
		e.wf.mu.Unlock()
		e.saveRun(run, rec, seq)
		return "Opened gate " + g.Name + " (an approval card in the chat). Say what needs approving, then STOP your turn; you are called back once it is approved or rejected.", nil
	}
	cmdline := cmp.Or(strings.TrimSpace(g.Command), strings.TrimSpace(command))
	if cmdline == "" {
		return "", fmt.Errorf("cổng %s cần lệnh kiểm tra (command), ví dụ một trong các lệnh được tự chạy: %s", g.Name, strings.Join(sc.Access.Commands, ", "))
	}
	csc := sc
	csc.Dir = ""
	if role != "" { // in the worktree of the role that changed the code
		r := run.role(role)
		if r == nil || r.AgentID == "" {
			return "", fmt.Errorf("vai %q chưa làm gì để kiểm tra", role)
		}
		if dir := e.roleDir(ctx, run, r.AgentID); dir != "" {
			csc.Dir = dir
		}
	}
	a, err := e.acts.Propose(ctx, csc, "run_command", cmdline, "Cổng "+g.Name+" của quy trình "+run.def.Name)
	if err != nil {
		return "", err
	}
	status, out := "waiting", fmt.Sprintf("Command %q waits for the person's approval (id %s); say so, then stop your turn.", a.Target, a.ID)
	switch a.Status {
	case "done":
		status, out = "passed", "Gate "+g.Name+" PASSED.\n$ "+a.Target+"\n"+truncate(a.Detail, 4000)
	case "failed":
		status, out = "failed", "Gate "+g.Name+" FAILED.\n$ "+a.Target+"\n"+truncate(a.Detail, 4000)
	}
	e.wf.mu.Lock()
	if rg := run.gate(key); rg != nil {
		rg.Status, rg.ActionID, rg.Detail = status, a.ID, truncate(a.Detail, 500)
	}
	run.logf("Cổng %s: %s", g.Name, gateStatus(status))
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	return out, nil
}

// roleDir is the worktree an agent of the chat edits in ("" = none).
func (e *Engine) roleDir(ctx context.Context, run *wfRun, agentID string) string {
	if e.trees == nil {
		return ""
	}
	conv, err := e.store.Chat().GetConversation(ctx, run.rec.ConversationID)
	if err != nil {
		return ""
	}
	a, err := e.store.Agents().Get(ctx, agentID)
	if err != nil {
		return ""
	}
	name := e.chatTree(ctx, conv, a)
	if !e.trees.Exists(run.rec.ProjectID, name) {
		return ""
	}
	return e.trees.Path(run.rec.ProjectID, name)
}

// refreshGates reads the decisions of the gates waiting on a person.
func (e *Engine) refreshGates(ctx context.Context, run *wfRun) {
	e.wf.mu.Lock()
	var waiting []storage.RunGate
	for _, g := range run.rec.Gates {
		if g.Status == "waiting" && g.ActionID != "" {
			waiting = append(waiting, g)
		}
	}
	e.wf.mu.Unlock()
	if len(waiting) == 0 {
		return
	}
	changed := false
	for _, g := range waiting {
		a, err := e.store.Actions().Get(ctx, g.ActionID)
		if err != nil {
			continue
		}
		status := ""
		switch a.Status {
		case "done":
			status = "passed"
		case "failed", "rejected":
			status = "failed"
		}
		if status == "" {
			continue
		}
		e.wf.mu.Lock()
		if rg := run.gate(g.Key); rg != nil && rg.Status == "waiting" {
			rg.Status, rg.Detail = status, truncate(a.Detail, 500)
			run.logf("Cổng %s: %s", rg.Name, gateStatus(status))
			changed = true
		}
		e.wf.mu.Unlock()
	}
	if changed {
		e.wf.mu.Lock()
		rec, seq := run.snapshot()
		e.wf.mu.Unlock()
		e.saveRun(run, rec, seq)
	}
}

func (e *Engine) wfDone(ctx context.Context, sc officetools.Scope, run *wfRun, summary string, outputs map[string]string, overrule string) (string, error) {
	e.refreshGates(ctx, run)
	e.wf.mu.Lock()
	var missing, working, objected []string
	for _, r := range run.rec.Roles {
		if r.Objection != "" {
			objected = append(objected, fmt.Sprintf("%s: %s", r.Name, r.Objection))
		}
	}
	for _, g := range run.rec.Gates {
		if g.Required && g.Status != "passed" {
			missing = append(missing, g.Name+" ("+gateStatus(g.Status)+")")
		}
	}
	for _, r := range run.rec.Roles {
		if r.Status == "working" {
			working = append(working, r.Name)
		}
	}
	queued := len(e.queuedAsks(sc.RunRef))
	e.wf.mu.Unlock()
	if len(missing) > 0 {
		return "", errors.New("chưa qua cổng bắt buộc: " + strings.Join(missing, ", ") + ". Mở cổng bằng workflow_gate; không qua được thì báo người dùng và hỏi có dừng quy trình không")
	}
	if len(working) > 0 || queued > 0 {
		return "", errors.New("còn vai đang làm hoặc vừa giao: " + strings.Join(working, ", ") + "; đợi xong rồi mới kết thúc")
	}
	overrule = strings.TrimSpace(overrule)
	if len(objected) > 0 && overrule == "" {
		return "", errors.New("còn phản biện chưa trả lời (" + strings.Join(objected, "; ") + "). Gửi lại vai bằng workflow_send (sửa bản giao hoặc giải thích), hoặc giữ nguyên thì gọi lại workflow_done kèm overrule: vì sao")
	}
	if strings.TrimSpace(summary) == "" {
		return "", errors.New("summary là kết quả gửi người gọi quy trình (họ chỉ thấy phần này): hãy viết đầy đủ")
	}
	if err := run.def.CheckOutputs(outputs); err != nil {
		return "", fmt.Errorf("%w (%s); gọi lại workflow_done kèm outputs", err, fieldList(run.def.Outputs))
	}
	summary = strings.TrimSpace(summary)
	e.wf.mu.Lock()
	run.rec.Outputs = outputs
	if len(objected) > 0 { // the caller sees what was pushed back on and why it stood
		run.logf("Giữ nguyên dù có phản biện: %s", truncate(overrule, 200))
		summary += "\n\n**Phản biện không theo:** " + strings.Join(objected, "; ") + "\n**Lý do:** " + overrule // i18n-ignore
	}
	e.wf.mu.Unlock()
	e.finishRun(run.rec.ConversationID, run.rec.ID, storage.RunDone, summary, "")
	return "The workflow is finished; the summary went to the chat that called it. End your turn with one short line.", nil
}

// ---- after a turn ----

// wfAfter runs what follows a turn of a workflow: a coordinator's turn
// starts what it asked; a role's turn is counted, and when the last role it
// waited on is done, the coordinator is called back. role: whether prev was a
// role's turn (nothing else follows it).
func (e *Engine) wfAfter(prev *Turn, conv storage.Conversation, project storage.Repo, reply string) (role bool) {
	if prev.wfRun == "" {
		return false
	}
	if prev.wfStep != "" { // a step's turn: its answer goes to the runner waiting for it
		e.wf.mu.Lock()
		key := prev.wfRun + "#" + prev.wfStep
		ch, ok := e.wf.waiting[key]
		delete(e.wf.waiting, key)
		e.wf.mu.Unlock()
		if ok {
			if prev.err != "" {
				ch <- "\x00" + prev.err
			} else {
				ch <- reply
			}
		}
		return true
	}
	if prev.wfWatch {
		e.wfWatched(prev, conv, reply)
		return true
	}
	if prev.wfRole != "" {
		e.wfRoleDone(prev, conv, project, reply)
		return true
	}
	e.wf.mu.Lock()
	asks := e.wf.pending[prev.ID]
	delete(e.wf.pending, prev.ID)
	run := e.wf.byConv[conv.ID]
	if run == nil || run.rec.ID != prev.wfRun {
		e.wf.mu.Unlock()
		return false
	}
	e.wf.mu.Unlock()
	var started int
	for _, a := range asks {
		if e.startRole(run, conv, project, a) {
			started++
		}
	}
	if len(asks) > 0 && started == 0 {
		e.wfCallBack(run.rec.ID, conv, project)
	}
	return false
}

// startRole starts one turn a coordinator asked for.
func (e *Engine) startRole(run *wfRun, conv storage.Conversation, project storage.Repo, a wfAsk) bool {
	if a.flow {
		return e.startChild(run, conv, project, a)
	}
	d, _ := run.def.Role(a.role)
	level := accessLevel(d.Access)
	if a.vote {
		level = perm.Read
	}
	if run.ceiling != "" {
		level = perm.Min(level, run.ceiling)
	}
	e.wf.mu.Lock()
	r := run.role(a.role)
	r.Status = "working"
	run.batch[a.role] = true
	run.rec.Turns++
	r.Turns++
	e.wf.mu.Unlock()
	t, why := e.startTurn(conv, project, turnSpec{agent: a.agent, effort: a.effort, background: true, delegator: run.coord.ID, actor: run.rec.Actor,
		tier: run.tier, ceiling: level, tree: run.tree, title: run.coord.Name + " → " + a.agent.Name + " (" + d.Name + ")", prompt: a.prompt,
		wfRun: run.rec.ID, wfRole: a.role, wfVote: a.vote})
	if t == nil {
		e.wf.mu.Lock()
		r := run.role(a.role)
		r.Status = "failed"
		delete(run.batch, a.role)
		run.rec.Turns--
		r.Turns--
		run.notes = append(run.notes, fmt.Sprintf("Role %s (%s) could not start: %s", d.Name, a.agent.Name, cmp.Or(why, "the chat is busy")))
		if a.vote && run.voting != nil {
			run.voting.ballots[a.role] = "unclear"
		}
		rec, seq := run.snapshot()
		e.wf.mu.Unlock()
		e.saveRun(run, rec, seq)
		e.note(conv, why)
		return false
	}
	e.wf.mu.Lock()
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	e.watchIdle(run, conv, a.role)
	e.watchRun(run, conv, project)
	return true
}

// wfRoleDone records a role's answer; the last one of the batch calls the
// coordinator back.
func (e *Engine) wfRoleDone(prev *Turn, conv storage.Conversation, project storage.Repo, reply string) {
	e.wf.mu.Lock()
	run := e.wf.byConv[conv.ID]
	if run == nil || run.rec.ID != prev.wfRun {
		e.wf.mu.Unlock()
		return // stopped, timed out or done meanwhile
	}
	r := run.role(prev.wfRole)
	if r == nil {
		e.wf.mu.Unlock()
		return
	}
	d, _ := run.def.Role(prev.wfRole)
	if prev.err != "" {
		r.Status = "failed"
		run.notes = append(run.notes, fmt.Sprintf("Role %s (%s) failed: %s", d.Name, r.AgentName, truncate(prev.err, 300)))
		run.logf("%s (%s) lỗi", d.Name, r.AgentName)
	} else {
		r.Status = "done"
		r.Result = truncate(reply, 4000)
		run.logf("%s (%s) xong", d.Name, r.AgentName)
		if r.Objection = ""; !prev.wfVote {
			if what := objectionOf(reply); what != "" { // the coordinator answers it before the run ends
				r.Objection = what
				run.notes = append(run.notes, fmt.Sprintf("Role %s (%s) OBJECTS to the brief: %s. Handle it before going on: fix the brief or explain, then send it again with workflow_send; to keep it as is, give the reason in the overrule of workflow_done.", d.Name, r.AgentName, what))
				run.logf("%s phản biện: %s", d.Name, truncate(what, 200))
			}
		}
	}
	if prev.wfVote && run.voting != nil {
		b := "unclear"
		if prev.err == "" {
			b = ballot(reply)
		}
		run.voting.ballots[prev.wfRole] = b
	}
	delete(run.batch, prev.wfRole)
	last := len(run.batch) == 0
	key := run.rec.ID + "/" + prev.wfRole
	if ch, ok := e.wf.waiting[key]; ok { // its coordinator waits for it (workflow_ask): the answer goes there
		delete(e.wf.waiting, key)
		answer := reply
		if prev.err != "" {
			answer = "(error) " + prev.err
		}
		ch <- answer
		last = last && run.owed // others answered meanwhile: they are told by a call-back
	} else if !last {
		run.owed = true
	}
	id := run.rec.ID
	rec, seq := run.snapshot()
	e.wf.mu.Unlock()
	e.saveRun(run, rec, seq)
	if last {
		e.wfCallBack(id, conv, project)
	}
}

// wfCallBack calls the coordinator back once its roles are done; a chat
// busy with another answer is tried again a little later.
func (e *Engine) wfCallBack(runID string, conv storage.Conversation, project storage.Repo) {
	e.wf.mu.Lock()
	run := e.wf.byConv[conv.ID]
	if run == nil || run.rec.ID != runID || len(run.batch) > 0 {
		e.wf.mu.Unlock()
		return
	}
	var lines []string
	for _, r := range run.rec.Roles {
		if r.Status == "done" || r.Status == "failed" {
			lines = append(lines, fmt.Sprintf("%s (%s): %s", r.Name, r.AgentName, roleStatus(r.Status)))
		}
	}
	notes := slices.Clone(run.notes)
	var vote string
	if run.voting != nil && run.def.Vote != nil {
		var passed bool
		passed, vote = tally(run.def.Vote, run.voting.ballots)
		if passed {
			run.logf("Biểu quyết thông qua")
		} else {
			run.logf("Biểu quyết không thông qua")
		}
	}
	coord, tier, tree, actorOf := run.coord, run.tier, run.tree, run.rec.Actor
	e.wf.mu.Unlock()
	prompt := prompts.Render("workflow/callback", struct {
		Workflow, Vote string
		Done, Notes    []string
	}{run.def.Name, vote, lines, notes})
	t, why := e.startTurn(conv, project, turnSpec{agent: coord, actor: actorOf, tier: tier, ceiling: run.ceiling, tree: tree,
		title: "Quy trình " + run.def.Name + " → " + coord.Name, prompt: prompt, wfRun: runID})
	if t == nil {
		if why == "" { // the chat is busy: again in a moment
			e.wf.mu.Lock()
			run.tries++
			tries := run.tries
			e.wf.mu.Unlock()
			if tries < 240 {
				time.AfterFunc(5*time.Second, func() { e.wfCallBack(runID, conv, project) })
			}
			return
		}
		e.note(conv, why)
		e.finishRun(conv.ID, runID, storage.RunFailed, "", why)
		return
	}
	e.wf.mu.Lock()
	run.tries, run.notes, run.voting, run.owed = 0, nil, nil, false
	e.wf.mu.Unlock()
}

// wfCost adds a turn's cost to its run.
func (e *Engine) wfCost(turn *Turn, conversationID string, cost *float64) {
	if turn.wfRun == "" || cost == nil {
		return
	}
	e.wf.mu.Lock()
	run := e.wf.byConv[conversationID]
	if run == nil || run.rec.ID != turn.wfRun {
		e.wf.mu.Unlock()
		return
	}
	run.rec.CostUSD += *cost
	if r := run.role(turn.wfRole); r != nil {
		r.CostUSD += *cost
	}
	for r := run; r.parent != nil; r = r.parent { // the runs above it pay for it too, as it spends
		r.parent.rec.CostUSD += *cost
		if pr := r.parent.role(r.parentRole); pr != nil {
			pr.CostUSD += *cost
		}
	}
	e.wf.mu.Unlock()
}

// wfSession is the session a role's turn resumes ("" = a new one).
func (e *Engine) wfSession(turn *Turn, conversationID string) (session, runtime string) {
	if turn.wfRole == "" || turn.wfVote || turn.wfWatch {
		return "", ""
	}
	e.wf.mu.Lock()
	defer e.wf.mu.Unlock()
	if run := e.wf.byConv[conversationID]; run != nil && run.rec.ID == turn.wfRun {
		if r := run.role(turn.wfRole); r != nil {
			return r.SessionID, r.Runtime
		}
	}
	return "", ""
}

// wfKeepSession keeps a role's session for its follow-ups.
func (e *Engine) wfKeepSession(turn *Turn, conversationID, session, runtime string) {
	if turn.wfRole == "" || turn.wfVote || turn.wfWatch || session == "" {
		return
	}
	e.wf.mu.Lock()
	defer e.wf.mu.Unlock()
	if run := e.wf.byConv[conversationID]; run != nil && run.rec.ID == turn.wfRun {
		if r := run.role(turn.wfRole); r != nil {
			r.SessionID, r.Runtime = session, runtime
		}
	}
}

// roleBrief is what a role's turn is told besides its brief
// (workflow/role.md).
func roleBrief(name, workflowName string) string {
	return "\n\n" + prompts.Render("workflow/role", struct{ Role, Workflow, ObjectionMark string }{name, workflowName, objectionMark}) + "\n"
}

// actionsProposer is what the gates propose through.
type actionsProposer interface {
	Propose(ctx context.Context, sc actions.Scope, kind, target, reason string, args ...storage.ActionArgs) (storage.Action, error)
	// RunDeferred: a commit held for its turn's diff, once that is merged (ADR-189)
	RunDeferred(ctx context.Context, sc actions.Scope, id string, blocked bool) (storage.Action, error)
}

// SetActions lets workflows open gates (approval cards, check commands).
func (e *Engine) SetActions(a actionsProposer) { e.acts = a }

// fieldList names declared inputs or outputs for a prompt.
func fieldList(fs []workflow.Field) string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		x := "`" + f.Key + "`"
		if f.Type != "" && f.Type != "string" {
			x += " [" + f.Type + "]"
		}
		if f.Description != "" {
			x += " (" + f.Description + ")"
		}
		if f.Required {
			x += " required"
		}
		out = append(out, x)
	}
	return strings.Join(out, ", ")
}

// outputsText is a run's outputs as Markdown, after its summary. An output
// the summary already holds (a "summary" output that is the result itself)
// is not said twice.
func outputsText(rec storage.WorkflowRun) string {
	result := strings.TrimSpace(rec.Result)
	keys := make([]string, 0, len(rec.Outputs))
	for k, v := range rec.Outputs {
		if v = strings.TrimSpace(v); v != "" && result != "" && strings.Contains(result, v) {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString("\n\n**Đầu ra**\n") // i18n-ignore
	for _, k := range keys {
		fmt.Fprintf(&b, "- `%s`: %s\n", k, rec.Outputs[k])
	}
	return strings.TrimRight(b.String(), "\n")
}

// wfAskNow asks an analyze role a question and waits for its answer within
// the coordinator's turn (workflow_ask); past the wait it goes on in the
// background and the coordinator is called back as for workflow_delegate.
func (e *Engine) wfAskNow(ctx context.Context, sc officetools.Scope, run *wfRun, role, question string, waitSec int) (string, error) {
	d, ok := run.def.Role(role)
	if !ok {
		return "", fmt.Errorf("quy trình không có vai %q (có: %s)", role, roleKeys(run.def))
	}
	if d.Workflow != "" || (d.Access != workflow.AccessAgent && d.Access != workflow.AccessAnalyze) {
		return "", fmt.Errorf("workflow_ask không dùng cho vai propose/edit hay quy trình con; vai %s dùng workflow_delegate / workflow_send", d.Name)
	}
	if strings.TrimSpace(question) == "" {
		return "", errors.New("hãy ghi câu hỏi")
	}
	a, err := e.pickAgent(ctx, run, role, "")
	if err != nil {
		return "", err
	}
	conv, err := e.store.Chat().GetConversation(ctx, run.rec.ConversationID)
	if err != nil {
		return "", err
	}
	project, err := e.store.Repos().Get(ctx, run.rec.ProjectID)
	if err != nil {
		return "", err
	}
	e.wf.mu.Lock()
	r := run.role(role)
	if r.Status == "working" {
		e.wf.mu.Unlock()
		return "", fmt.Errorf("vai %s đang làm; đợi xong", d.Name)
	}
	again := r.Status != "idle"
	if again && r.Rounds >= run.def.Limits.Rounds {
		e.wf.mu.Unlock()
		return "", fmt.Errorf("đã hỏi vai %s %d lần (giới hạn); tổng kết với kết quả hiện có", d.Name, r.Rounds)
	}
	if err := e.canAsk(run, sc.RunRef, role, a); err != nil {
		e.wf.mu.Unlock()
		return "", err
	}
	e.wf.mu.Unlock()
	if !again {
		if err := e.checkDiffer(ctx, run, role, a); err != nil {
			return "", err
		}
	}
	e.wf.mu.Lock()
	r = run.role(role)
	if again {
		r.Rounds++
	}
	r.AgentID, r.AgentName = a.ID, a.Name
	key := run.rec.ID + "/" + role
	ch := make(chan string, 1)
	e.wf.waiting[key] = ch
	run.logf("Hỏi %s (%s) và chờ", d.Name, a.Name)
	e.wf.mu.Unlock()
	prompt := prompts.Render("workflow/ask", struct{ Coordinator, Workflow, Role, Question, AccessNote string }{
		run.coord.Name, run.def.Name, d.Name, strings.TrimSpace(question), workflow.AccessNote(d.Access)})
	if !e.startRole(run, conv, project, wfAsk{role: role, agent: a, prompt: prompt}) {
		e.wf.mu.Lock()
		delete(e.wf.waiting, key)
		e.wf.mu.Unlock()
		return "", fmt.Errorf("vai %s không bắt đầu được (chat bận hoặc hết giờ)", d.Name)
	}
	wait := time.Duration(min(max(cmp.Or(waitSec, 60), 10), 120)) * time.Second
	select {
	case answer := <-ch:
		return fmt.Sprintf("%s (role %s) answers:\n%s", a.Name, d.Name, answer), nil
	case <-time.After(wait):
	case <-ctx.Done():
	}
	e.wf.mu.Lock()
	if _, still := e.wf.waiting[key]; still { // not answered yet: as a hand-off from here on
		delete(e.wf.waiting, key)
		e.wf.mu.Unlock()
		return fmt.Sprintf("%s has not answered after %s; the role goes on in the background and you are called back when it is done (as with workflow_delegate). Write a short note and stop your turn.", a.Name, wait), nil
	}
	e.wf.mu.Unlock()
	return fmt.Sprintf("%s (role %s) answers:\n%s", a.Name, d.Name, <-ch), nil
}
