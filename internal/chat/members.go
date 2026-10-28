package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/proctrack"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

// Group chat limits (ADR-044): hand-offs agents make per message, and replies
// in all. A person chatting (web, Discord, Telegram) is there to stop them, so
// only a safety cap against a loop; what runs unattended keeps the small one.
const (
	maxHops          = 2
	maxAnswers       = 4
	maxHopsPerson    = 10
	maxAnswersPerson = 20
)

// limitsFor is the hand-offs and replies a message of who allows.
func limitsFor(who string) (hops, answers int) {
	switch via, _, _ := strings.Cut(who, ":"); via {
	case "human", "discord", "telegram":
		return maxHopsPerson, maxAnswersPerson
	}
	return maxHops, maxAnswers
}

// overAnswers: a message of who already had its replies (maxAnswers). The
// report of a hand-off that is done never is: it only brings back work the
// hops cap already allowed, and dropping it loses that work.
func overAnswers(who string, total int32, report bool) (int, bool) {
	_, most := limitsFor(who)
	return most, !report && total >= int32(most)
}

// delegation is a task an agent gave another with the delegate tool.
type delegation struct {
	agent  storage.Agent
	task   string
	effort string // how hard it thinks ("" = the agent's own)
	board  string // the work board's task it is (ADR-210; "" = none)
}

// BoardEvent: a hand-off tied to a task of the work board started or ended
// (ADR-210); the board follows it.
type BoardEvent struct {
	ProjectID, ConversationID string
	Task                      string // the board's task id
	Agent                     string // who works on it
	Done                      bool   // false = started
	Failed                    bool   // ended without an answer
	Result                    string // its answer, when done
}

// SetBoardHook: fn hears of hand-offs tied to the work board.
func (e *Engine) SetBoardHook(fn func(ctx context.Context, ev BoardEvent)) { e.boardHook = fn }

// waitingHandoff is a hand-off given to an agent busy with an earlier one in
// the same chat (ADR-221): it starts once that one is done, not dropped.
type waitingHandoff struct {
	conv    storage.Conversation
	project storage.Repo
	spec    turnSpec
}

// waitFor queues spec behind its agent's hand-off running in the chat; false
// when the agent is free there.
func (e *Engine) waitFor(conv storage.Conversation, project storage.Repo, spec turnSpec) bool {
	k := conv.ID + "/" + spec.agent.ID
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, busy := e.bg[k]; !busy {
		return false
	}
	e.held[k] = append(e.held[k], waitingHandoff{conv, project, spec})
	return true
}

// startWaiting starts the first of the hand-offs that waited for an agent
// now free; the others wait behind it. One whose board task was closed
// meanwhile (dropped, done another way) is left out.
func (e *Engine) startWaiting(list []waitingHandoff) {
	for i, w := range list {
		if w.spec.board != "" && e.boardOpen != nil && !e.boardOpen(context.Background(), w.conv.ProjectID, w.spec.board) {
			continue
		}
		started, why := e.startTurn(w.conv, w.project, w.spec)
		if started == nil {
			e.note(w.conv, why)
			continue
		}
		e.board(BoardEvent{ProjectID: w.conv.ProjectID, ConversationID: w.conv.ID, Task: w.spec.board, Agent: w.spec.agent.Name})
		if rest := list[i+1:]; len(rest) > 0 {
			k := w.conv.ID + "/" + w.spec.agent.ID
			e.mu.Lock()
			e.held[k] = append(rest, e.held[k]...)
			e.mu.Unlock()
		}
		return
	}
}

// SetBoardOpen: fn tells whether a task of the work board is still to do.
func (e *Engine) SetBoardOpen(fn func(ctx context.Context, projectID, task string) bool) {
	e.boardOpen = fn
}

// NextWork is the next task of the work board an agent follows up (ADR-221).
type NextWork struct {
	Task, Title, Detail string
	Open                int    // the tasks waiting for it, this one included
	Sig                 string // the board's open tasks as they stand: it changes when one moves
}

// SetNextHook: fn gives an agent whose turn ended its next task of the work
// board (dflt: it is the project's default agent, the one following up a
// task with no owner).
func (e *Engine) SetNextHook(fn func(ctx context.Context, projectID string, agent storage.Agent, dflt bool) (NextWork, bool)) {
	e.nextHook = fn
}

func (e *Engine) board(ev BoardEvent) {
	if e.boardHook != nil && ev.Task != "" {
		fn := e.boardHook
		safeGo("boardHook", func() { fn(context.Background(), ev) })
	}
}

// Delegate records a hand-off asked by the agent answering in sc (the
// delegate tool): it starts in the background once that answer is done.
func (e *Engine) Delegate(ctx context.Context, sc officetools.Scope, agentName, task, effort string) (string, error) {
	task = strings.TrimSpace(task)
	if sc.ConversationID == "" || sc.TaskID != "" {
		return "", errors.New("chỉ giao việc được trong Chat")
	}
	conv, err := e.store.Chat().GetConversation(ctx, sc.ConversationID)
	if err != nil || !teamChat(conv) {
		return "", errors.New("chỉ giao việc được trong Chat")
	}
	if task == "" {
		return "", errors.New("hãy ghi rõ việc cần làm")
	}
	if !storage.ValidEffort(effort) {
		return "", fmt.Errorf("effort %q không hợp lệ (có: %s)", effort, strings.Join(storage.Efforts, ", "))
	}
	if _, inRun := e.WorkflowScope(sc); inRun {
		return "", errors.New("đang trong một quy trình: giao việc bằng workflow_delegate / workflow_send (chỉ agent điều phối)")
	}
	agents, err := e.Agents(ctx, conv.ProjectID)
	if err != nil {
		return "", err
	}
	name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(agentName), "@")))
	i := slices.IndexFunc(agents, func(a storage.Agent) bool { return strings.ToLower(a.Name) == name || strings.ToLower(a.Key) == name })
	if i < 0 {
		names := make([]string, 0, len(agents))
		for _, a := range agents {
			names = append(names, a.Name)
		}
		return "", fmt.Errorf("không có agent %q trong project (có: %s)", agentName, strings.Join(names, ", "))
	}
	if agents[i].Name == sc.Agent {
		return "", errors.New("không tự giao việc cho chính mình")
	}
	if agents[i].Disabled {
		return "", &OffError{Notice: storage.OffNotice(agents[i].Name) + " Tell the person."}
	}
	e.mu.Lock()
	e.handed[sc.RunRef] = append(e.handed[sc.RunRef], delegation{agent: agents[i], task: task, effort: effort, board: officetools.BoardTaskOf(ctx)})
	e.mu.Unlock()
	return prompts.Render("handoff/handed", map[string]any{"Agent": agents[i].Name}), nil
}

// teamChat: a chat where agents give each other work — the project's chats
// and a bot's (Discord/Telegram, whose follow-ups go back to the channel).
func teamChat(c storage.Conversation) bool {
	return c.TaskID == "" && (c.Purpose == "" || c.Purpose == "channel")
}

// queued is an agent still to answer, and who tagged it.
type queued struct {
	agent storage.Agent
	from  string
}

func agentsOf(q []queued) []storage.Agent {
	out := make([]storage.Agent, 0, len(q))
	for _, x := range q {
		out = append(out, x.agent)
	}
	return out
}

// RunningTurn is an answer in progress in a chat.
type RunningTurn struct {
	TurnID     string `json:"turn_id"`
	AgentName  string `json:"agent_name"`
	Background bool   `json:"background"` // a hand-off the person does not wait for
}

// Running lists the answers in progress in a chat: the one the person waits
// for and the hand-offs working in the background.
func (e *Engine) Running(conversationID string) []RunningTurn {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []RunningTurn{}
	if t, ok := e.active[conversationID]; ok {
		out = append(out, RunningTurn{t.ID, t.agentName, false})
	}
	for k, t := range e.bg {
		if strings.HasPrefix(k, conversationID+"/") {
			out = append(out, RunningTurn{t.ID, t.agentName, true})
		}
	}
	return out
}

// RunningCount is how many answers run now, in every chat.
func (e *Engine) RunningCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.active) + len(e.bg)
}

// turnSpec is a turn to start in a chat.
type turnSpec struct {
	agent      storage.Agent
	prompt     string
	title      string
	background bool
	delegator  string
	queue      []queued
	hops       int
	answered   int
	actor      string
	replace    *Turn         // the finishing turn it takes the chat from ("" = the chat must be free)
	total      *atomic.Int32 // replies to the person's message so far, hand-offs included
	tier       string        // model tier for this turn ("" = the agent's)
	effort     string        // how hard it thinks, as its delegator asked ("" = the agent's own)
	ceiling    string        // the most the person's message may have run (ADR-081)
	tree       treeOpt       // the worktree it runs in ("" = the chat's)
	limit      time.Duration // how long it may take (0: none; <0: a chat's default, none) — the message's, ADR-082
	report     bool          // the delegator hearing back from a hand-off: not held to maxAnswers
	// a workflow's turn: its run, the role ("" = the coordinator), a ballot
	wfRun, wfRole string
	wfVote        bool
	wfWatch       bool   // the run's supervisor checking it
	wfStep        string // a step of a graph of steps (ADR-108)
	board         string // a hand-off's work board task (ADR-210)
	fixes         int    // turns opened so far to fix failed checks (ADR-222)
}

// startTurn starts an agent's answer; nil and why when it cannot: the agent
// is already working in this chat, the chat is busy, the message had its
// replies (maxAnswers), or the budget is spent.
func (e *Engine) startTurn(conv storage.Conversation, project storage.Repo, s turnSpec) (*Turn, string) {
	if s.total != nil {
		if most, over := overAnswers(s.actor, s.total.Load(), s.report); over {
			return nil, fmt.Sprintf("Đã đủ %d lượt trả lời cho một tin nhắn nên %s không trả lời tiếp. Hãy nhắn lại nếu cần.", most, s.agent.Name)
		}
	}
	if e.usage != nil {
		if err := e.usage.Check(context.Background(), project.ID); err != nil {
			return nil, fmt.Sprintf("%s không trả lời: %v", s.agent.Name, err)
		}
	}
	turnID := fmt.Sprintf("%s-%d", conv.ID, time.Now().UnixNano())
	base := proctrack.With(actor.With(context.Background(), s.actor), proctrack.Info{Kind: "agent", TurnID: turnID, ConversationID: conv.ID, ProjectID: conv.ProjectID, Label: s.agent.Name})
	if s.ceiling != "" {
		base = WithCeiling(base, s.ceiling)
	}
	base = withTreeOpt(base, s.tree)
	limit := s.limit
	if limit < 0 { // not said: a chat's turn
		limit = defaultTurnTimeout
	}
	runCtx, cancel := withTimeout(WithModelTier(base, s.tier), limit)
	t := &Turn{ID: turnID, ConversationID: conv.ID, wake: make(chan struct{}), cancel: cancel,
		queue: s.queue, hops: s.hops, answered: s.answered, actor: s.actor, total: s.total, tier: s.tier, effort: s.effort, ceiling: s.ceiling, tree: s.tree, limit: limit,
		agentID: s.agent.ID, agentName: s.agent.Name, background: s.background, delegator: s.delegator,
		wfRun: s.wfRun, wfRole: s.wfRole, wfVote: s.wfVote, wfWatch: s.wfWatch, wfStep: s.wfStep, board: s.board, fixes: s.fixes}
	e.mu.Lock()
	if e.stopping.Load() { // under e.mu, as in SendWithContext
		e.mu.Unlock()
		cancel()
		return nil, ErrStopping.Error()
	}
	key := conv.ID + "/" + s.agent.ID
	if _, working := e.bg[key]; working { // one session, one worktree: never twice at once
		e.mu.Unlock()
		cancel()
		return nil, fmt.Sprintf("%s đang làm việc được giao trong cuộc chat nên chưa trả lời được.", s.agent.Name)
	}
	if s.background {
		e.bg[key] = t
	} else {
		if cur, busy := e.active[conv.ID]; busy && cur != s.replace {
			e.mu.Unlock()
			cancel()
			return nil, ""
		}
		e.active[conv.ID] = t // held before the previous turn lets go
	}
	if s.total != nil {
		s.total.Add(1)
	}
	e.turns[t.ID] = t
	e.mu.Unlock()
	e.running(conv.ID)
	release := func() {
		cancel()
		e.finish(t)
		if s.total != nil {
			s.total.Add(-1)
		}
	}
	history, err := e.store.Chat().ListMessages(runCtx, conv.ID)
	if err != nil {
		release()
		return nil, err.Error()
	}
	job, err := e.beginJob(runCtx, conv, s.agent.ID, truncate(s.title, 80))
	if err != nil {
		release()
		return nil, err.Error()
	}
	t.setJobID(job.ID)
	runCtx = usage.WithJob(runCtx, job.ID)
	safeGo("run", func() { e.run(runCtx, t, conv, project, s.agent, history, s.prompt, nil) })
	return t, ""
}

// note leaves a line in the chat (why an agent did not answer).
func (e *Engine) note(conv storage.Conversation, text string) {
	if text != "" {
		_, _ = e.store.Chat().AddMessage(context.Background(), storage.Message{ConversationID: conv.ID, Role: "error", Content: text})
	}
}

// nextTurn runs what follows an answer (ADR-044) and returns the turn the
// person keeps watching ("" = none):
//   - @tags in the reply hand off in the background, like subagents, within
//     maxHops per message of the person;
//   - a background agent that is done: the one who asked reports back, when
//     the chat is free (otherwise it sees the result on its next turn);
//   - the next agent the person tagged answers.
func (e *Engine) nextTurn(ctx context.Context, prev *Turn, conv storage.Conversation, project storage.Repo, agent storage.Agent, reply string) string {
	if conv.Purpose == consultPurpose { // an expert answered: back to the chat that asked, nothing else
		e.consultTurnDone(conv, reply, prev.err)
		return ""
	}
	// a workflow's turn: the coordinator's asks start, a role's answer is
	// counted (its coordinator is called back when its batch is done)
	if e.wfAfter(prev, conv, project, reply) {
		return ""
	}
	if ctx.Err() != nil {
		return ""
	}
	hops := prev.hops
	agents, _ := e.Agents(ctx, conv.ProjectID)
	e.mu.Lock()
	given := e.handed[prev.ID]
	delete(e.handed, prev.ID)
	e.mu.Unlock()
	if teamChat(conv) {
		var notes []string
		most, _ := limitsFor(prev.actor)
		for _, d := range given {
			if hops >= most {
				notes = append(notes, fmt.Sprintf("Đã dừng giao việc cho %s: quá %d lượt agent giao việc cho nhau trong một tin nhắn. Hãy tag lại nếu cần.", d.agent.Name, most))
				continue
			}
			spec := turnSpec{agent: d.agent, effort: d.effort, background: true, delegator: agent.ID, hops: hops + 1, actor: prev.actor, total: prev.total, tier: prev.tier, ceiling: prev.ceiling, tree: prev.tree, limit: prev.limit, board: d.board,
				title:  agent.Name + " → " + d.agent.Name,
				prompt: prompts.Handoff{From: agent.Name, Task: d.task}.String()}
			if e.waitFor(conv, project, spec) { // busy with an earlier hand-off here: this one goes once it is done
				notes = append(notes, fmt.Sprintf("%s đang làm việc được giao trước trong chat này; việc %s vừa giao xếp hàng và tự bắt đầu khi xong.", d.agent.Name, agent.Name))
				hops++
				continue
			}
			started, why := e.startTurn(conv, project, spec)
			if started == nil {
				notes = append(notes, why)
				continue
			}
			e.board(BoardEvent{ProjectID: conv.ProjectID, ConversationID: conv.ID, Task: d.board, Agent: d.agent.Name})
			hops++
		}
		for _, n := range notes {
			_, _ = e.store.Chat().AddMessage(context.Background(), storage.Message{ConversationID: conv.ID, Role: "error", Content: n})
		}
	}
	if prev.background {
		e.board(BoardEvent{ProjectID: conv.ProjectID, ConversationID: conv.ID, Task: prev.board, Agent: agent.Name, Done: true, Failed: prev.err != "", Result: reply})
		if i := slices.IndexFunc(agents, func(a storage.Agent) bool { return a.ID == prev.delegator }); i >= 0 {
			// the report only sums up: a strong agent writes it with the balanced model
			tier := prev.tier
			if tier == "" && agents[i].ModelTier == storage.TierStrong && agents[i].LLMModel == "" {
				tier = storage.TierBalanced
			}
			_, why := e.startTurn(conv, project, turnSpec{agent: agents[i], hops: hops, actor: prev.actor, total: prev.total, tier: tier, ceiling: prev.ceiling, tree: prev.tree, limit: prev.limit, report: true, title: agent.Name + " → " + agents[i].Name,
				prompt: prompts.Handoff{Kind: "report", From: agent.Name}.String()})
			e.note(conv, why)
		}
		return ""
	}
	// its diff failed the checks before merging: it fixes that first (ADR-222)
	if prev.held != "" && prev.fixes < maxCheckFixes && prev.err == "" && prev.wfRun == "" && prev.wfStep == "" && !prev.wfWatch {
		next, why := e.startTurn(conv, project, turnSpec{agent: agent, queue: prev.queue, hops: hops, answered: prev.answered + 1, actor: prev.actor,
			replace: prev, total: prev.total, tier: prev.tier, ceiling: prev.ceiling, tree: prev.tree, limit: prev.limit, fixes: prev.fixes + 1,
			title:  agent.Name + " → sửa cho qua kiểm tra",
			prompt: prompts.Render("handoff/checks", map[string]any{"Failed": prev.held, "Last": prev.fixes+1 == maxCheckFixes})})
		if next != nil {
			return next.ID
		}
		e.note(conv, why)
	}
	// the next agent the person tagged; one that cannot answer is skipped with a note
	for i, q := range prev.queue {
		next, why := e.startTurn(conv, project, turnSpec{agent: q.agent, queue: prev.queue[i+1:], hops: hops, answered: prev.answered + 1, actor: prev.actor,
			replace: prev, total: prev.total, tier: prev.tier, ceiling: prev.ceiling, tree: prev.tree, limit: prev.limit, title: q.from + " → " + q.agent.Name,
			prompt: prompts.Handoff{Kind: "tagged", From: promptName(q.from)}.String()})
		if next != nil {
			return next.ID
		}
		e.note(conv, why)
	}
	if teamChat(conv) && len(given) == 0 { // the chat's work is over: a linked project's hand-off hears back
		e.reportBack(conv, project, agent, reply)
		return e.nextWork(conv, project, agent, prev)
	}
	return ""
}

// nextWork starts the agent's next task of the work board when its turn ends and
// nothing else goes on in the chat (ADR-221): the work moves on by itself, not
// on a schedule's next run. An agent is sent once on a board as it stands: one
// that leaves it as it was (on hold, waiting on someone) is not sent again
// until a task on it moves. The turn it starts ("" = none) takes the chat
// from prev, as the next agent tagged does.
func (e *Engine) nextWork(conv storage.Conversation, project storage.Repo, agent storage.Agent, prev *Turn) string {
	if e.nextHook == nil || prev.err != "" {
		return ""
	}
	e.mu.Lock()
	busy := false
	for k := range e.bg {
		busy = busy || strings.HasPrefix(k, conv.ID+"/")
	}
	e.mu.Unlock()
	if busy { // a hand-off still runs: its report ends the chain, and looks again
		return ""
	}
	ctx := context.Background()
	d, _ := e.DefaultAgent(ctx, conv.ProjectID)
	next, ok := e.nextHook(ctx, conv.ProjectID, agent, d.ID == agent.ID)
	if !ok {
		return ""
	}
	key := conv.ProjectID + "/" + agent.ID
	e.mu.Lock()
	seen := e.nextSeen[key] == next.Sig
	e.nextSeen[key] = next.Sig
	e.mu.Unlock()
	if seen {
		return ""
	}
	started, _ := e.startTurn(conv, project, turnSpec{agent: agent, actor: prev.actor, replace: prev, total: &atomic.Int32{}, tier: prev.tier, ceiling: prev.ceiling, tree: prev.tree, limit: prev.limit,
		title:  agent.Name + " → việc tiếp theo",
		prompt: prompts.Render("handoff/next", next)})
	if started == nil {
		e.mu.Lock()
		delete(e.nextSeen, key) // the chat was busy: its next turn's end tries again
		e.mu.Unlock()
		return ""
	}
	return started.ID
}

// member is the agent's place in a chat (ADR-044): its own session there and
// the last message it has seen; a new one when it has not answered yet.
func (e *Engine) member(ctx context.Context, conv storage.Conversation, agent storage.Agent) storage.ChatMember {
	if list, err := e.store.Chat().Members(ctx, conv.ID); err == nil {
		for _, m := range list {
			if m.AgentID == agent.ID {
				m.AgentName = agent.Name
				return m
			}
		}
	}
	return storage.ChatMember{ConversationID: conv.ID, AgentID: agent.ID, AgentName: agent.Name}
}

// newSince is what was said after the message an agent last saw, by others
// (the person or other agents), labelled with who said it; "" = nothing.
func newSince(history []storage.Message, lastID, self string) string {
	start := -1
	for i, m := range history {
		if m.ID == lastID {
			start = i
		}
	}
	if start < 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range history[start+1:] {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		who := "The person"
		if m.Role == "assistant" {
			if m.Author == self {
				continue
			}
			who = m.Author
		}
		fmt.Fprintf(&b, "\n[%s]\n%s\n", who, truncate(m.Content, 6000))
	}
	if b.Len() == 0 {
		return ""
	}
	return "New in the chat since your last turn:\n" + b.String() + "\n---\n"
}

// groupBrief tells an agent who is in the chat and when to tag another.
func (e *Engine) groupBrief(ctx context.Context, conv storage.Conversation, self storage.Agent) string {
	agents, err := e.Agents(ctx, conv.ProjectID)
	agents = storage.OnAgents(agents) // a paused agent is not offered
	if err != nil || len(agents) < 2 {
		return ""
	}
	in := map[string]bool{}
	if list, err := e.store.Chat().Members(ctx, conv.ID); err == nil {
		for _, m := range list {
			in[m.AgentID] = true
		}
	}
	return "\n" + teamBrief(agents, self.ID, in) + "\n"
}

// teamBrief lists the project's agents for one of them (self), marking those
// already in the chat (in).
func teamBrief(agents []storage.Agent, self string, in map[string]bool) string {
	type member struct {
		Name, Role, Permission string
		You, In                bool
	}
	list := make([]member, 0, len(agents))
	for _, a := range agents {
		list = append(list, member{a.Name, firstNonEmpty(a.Role, "agent"), perm.Label(perm.Agent(a)), a.ID == self, in[a.ID]})
	}
	return prompts.Render("chat/team", map[string]any{"Agents": list})
}

// chatTree: the chat's first agent keeps the chat's worktree; agents that
// joined later get their own, so hand-offs running at once never collide.
func (e *Engine) chatTree(ctx context.Context, conv storage.Conversation, agent storage.Agent) string {
	if list, err := e.store.Chat().Members(ctx, conv.ID); err == nil && len(list) > 0 && list[0].AgentID != agent.ID {
		return ChatAgentTree(conv.ID, agent.ID)
	}
	return ChatTree(conv.ID)
}

// StopAll stops everything in a chat: the answer the person waits for and the
// hand-offs in the background (none reports back); how many were stopped.
func (e *Engine) StopAll(conversationID string) int {
	e.stopRun(conversationID) // its workflow ends first: nothing calls the coordinator back
	e.mu.Lock()
	for k := range e.held { // hand-offs waiting their turn end too
		if strings.HasPrefix(k, conversationID+"/") {
			delete(e.held, k)
		}
	}
	var stop []*Turn
	if t, ok := e.active[conversationID]; ok {
		stop = append(stop, t)
	}
	for k, t := range e.bg {
		if strings.HasPrefix(k, conversationID+"/") {
			stop = append(stop, t)
		}
	}
	e.mu.Unlock()
	for _, t := range stop {
		t.Cancel()
	}
	return len(stop)
}

// promptName is who said something, as an agent's prompt names them: the
// person (queued as "Người dùng" for the chat's titles) or an agent's name.
func promptName(from string) string {
	if from == "Người dùng" { // i18n-ignore
		return "The person"
	}
	return from
}
