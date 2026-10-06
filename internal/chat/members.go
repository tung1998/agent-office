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

// delegation is a task an agent gave another with the delegate tool.
type delegation struct {
	agent storage.Agent
	task  string
}

// Delegate records a hand-off asked by the agent answering in sc (the
// delegate tool): it starts in the background once that answer is done.
func (e *Engine) Delegate(ctx context.Context, sc officetools.Scope, agentName, task string) (string, error) {
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
		return "", &OffError{Notice: storage.OffNotice(agents[i].Name) + " Hãy báo lại cho người dùng."}
	}
	e.mu.Lock()
	e.handed[sc.RunRef] = append(e.handed[sc.RunRef], delegation{agent: agents[i], task: task})
	e.mu.Unlock()
	return fmt.Sprintf("Đã giao cho %s; %s bắt đầu khi bạn trả lời xong lượt này và làm ở nền. Trả lời người dùng ngay, đừng chờ; khi %s xong bạn sẽ được gọi lại để báo kết quả.",
		agents[i].Name, agents[i].Name, agents[i].Name), nil
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
	ceiling    string        // the most the person's message may have run (ADR-081)
	limit      time.Duration // how long it may take (0: none; <0: a chat's 20 minutes) — the message's, ADR-082
}

// startTurn starts an agent's answer; nil and why when it cannot: the agent
// is already working in this chat, the chat is busy, the message had its
// replies (maxAnswers), or the budget is spent.
func (e *Engine) startTurn(conv storage.Conversation, project storage.Repo, s turnSpec) (*Turn, string) {
	if _, most := limitsFor(s.actor); s.total != nil && s.total.Load() >= int32(most) {
		return nil, fmt.Sprintf("Đã đủ %d lượt trả lời cho một tin nhắn nên %s không trả lời tiếp. Hãy nhắn lại nếu cần.", most, s.agent.Name)
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
	limit := s.limit
	if limit < 0 { // not said: a chat's turn
		limit = defaultTurnTimeout
	}
	runCtx, cancel := withTimeout(WithModelTier(base, s.tier), limit)
	t := &Turn{ID: turnID, ConversationID: conv.ID, wake: make(chan struct{}), cancel: cancel,
		queue: s.queue, hops: s.hops, answered: s.answered, actor: s.actor, total: s.total, tier: s.tier, ceiling: s.ceiling, limit: limit,
		agentID: s.agent.ID, agentName: s.agent.Name, background: s.background, delegator: s.delegator}
	e.mu.Lock()
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
	t.JobID = job.ID
	runCtx = usage.WithJob(runCtx, job.ID)
	go e.run(runCtx, t, conv, project, s.agent, history, s.prompt, nil)
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
			started, why := e.startTurn(conv, project, turnSpec{agent: d.agent, background: true, delegator: agent.ID, hops: hops + 1, actor: prev.actor, total: prev.total, tier: prev.tier, ceiling: prev.ceiling, limit: prev.limit,
				title:  agent.Name + " → " + d.agent.Name,
				prompt: fmt.Sprintf("%s giao việc cho bạn:\n%s\n\nLàm phần này rồi báo kết quả ngắn gọn.", agent.Name, d.task)})
			if started == nil {
				notes = append(notes, why)
				continue
			}
			hops++
		}
		for _, n := range notes {
			_, _ = e.store.Chat().AddMessage(context.Background(), storage.Message{ConversationID: conv.ID, Role: "error", Content: n})
		}
	}
	if prev.background {
		if i := slices.IndexFunc(agents, func(a storage.Agent) bool { return a.ID == prev.delegator }); i >= 0 {
			// the report only sums up: a strong agent writes it with the balanced model
			tier := prev.tier
			if tier == "" && agents[i].ModelTier == storage.TierStrong && agents[i].LLMModel == "" {
				tier = storage.TierBalanced
			}
			_, why := e.startTurn(conv, project, turnSpec{agent: agents[i], hops: hops, actor: prev.actor, total: prev.total, tier: tier, ceiling: prev.ceiling, limit: prev.limit, title: agent.Name + " → " + agents[i].Name,
				prompt: fmt.Sprintf("%s đã làm xong phần việc bạn giao (xem tin gần nhất). Báo lại kết quả cho người dùng ngắn gọn và làm tiếp nếu cần.", agent.Name)})
			e.note(conv, why)
		}
		return ""
	}
	// the next agent the person tagged; one that cannot answer is skipped with a note
	for i, q := range prev.queue {
		next, why := e.startTurn(conv, project, turnSpec{agent: q.agent, queue: prev.queue[i+1:], hops: hops, answered: prev.answered + 1, actor: prev.actor,
			replace: prev, total: prev.total, tier: prev.tier, ceiling: prev.ceiling, limit: prev.limit, title: q.from + " → " + q.agent.Name,
			prompt: fmt.Sprintf("%s vừa tag bạn trong cuộc chat. Trả lời phần dành cho bạn trong tin gần nhất.", q.from)})
		if next != nil {
			return next.ID
		}
		e.note(conv, why)
	}
	return ""
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
		who := "Người dùng"
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
	return "Trong cuộc chat, từ lượt trước của bạn đã có thêm:\n" + b.String() + "\n---\n"
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
	var b strings.Builder
	b.WriteString("\n## Cuộc chat có thể có nhiều agent\nNgười dùng tag @Tên để kéo agent vào. Các agent của project (quyền):\n") // i18n-ignore
	for _, a := range agents {
		mark := ""
		if a.ID == self.ID {
			mark = " (bạn)"
		} else if in[a.ID] {
			mark = " (đang trong cuộc chat)"
		}
		fmt.Fprintf(&b, "- @%s: %s, quyền %s%s\n", a.Name, firstNonEmpty(a.Role, string(a.Tier)), perm.Label(perm.Agent(a)), mark)
	}
	b.WriteString("Muốn agent khác làm một phần việc thì dùng công cụ delegate (agent, task), chỉ khi thật sự cần (việc cần quyền hay chuyên môn bạn không có); việc tự làm được thì tự làm. Viết @Tên trong câu trả lời chỉ là nhắc tên, không giao việc.\n")
	return b.String()
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
	e.mu.Lock()
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
