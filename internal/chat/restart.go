package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
)

// ErrStopping: office is going down, the message waits for it to be back.
var ErrStopping = errors.New("office đang khởi động lại, gửi lại sau ít giây")

// Shutdown ends the answers being written as office goes down, within wait:
// their jobs stay running and their sessions keep what they did, so the
// next office goes on with them (ResumeInterrupted), as with the graphs of
// steps running (ResumeWorkflows).
func (e *Engine) Shutdown(wait time.Duration) {
	e.stopping.Store(true)
	e.mu.Lock()
	turns := make([]*Turn, 0, len(e.active)+len(e.bg))
	for _, t := range e.active {
		turns = append(turns, t)
	}
	for _, t := range e.bg {
		turns = append(turns, t)
	}
	e.mu.Unlock()
	for _, t := range turns {
		t.cancel()
	}
	// the graphs of steps stop where they are, as saved: the next office goes
	// on with them (ADR-256); they are not ended
	e.wf.mu.Lock()
	var stops []context.CancelFunc
	for _, r := range e.wf.byConv {
		if r.def.StepMode() && r.stop != nil {
			stops = append(stops, r.stop)
		}
	}
	e.wf.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	for end := time.Now().Add(wait); e.ActiveTurns() > 0 && time.Now().Before(end); {
		time.Sleep(100 * time.Millisecond)
	}
}

// ResumeInterrupted goes on with the answers office was writing when it went
// down (their jobs were still running): each chat gets a message tagging the
// agents cut off, in the name of who started them, and they go on in their
// sessions. It returns how many chats went on.
func (e *Engine) ResumeInterrupted(ctx context.Context, jobs []storage.Job) int {
	type cut struct {
		who    string
		agents []string
		over   []string // cut too many times in a row: not again (ADR-257)
	}
	var order []string
	chats := map[string]*cut{}
	for _, j := range jobs {
		if j.Kind != "chat_turn" || j.Origin != "user" || j.ConversationID == "" || j.AgentID == "" {
			continue // an automation's turn runs again by its own rules
		}
		c := chats[j.ConversationID]
		if c == nil {
			c = &cut{who: j.CreatedBy}
			chats[j.ConversationID] = c
			order = append(order, j.ConversationID)
		}
		a, err := e.store.Agents().Get(ctx, j.AgentID)
		if err != nil || slices.Contains(c.agents, a.Name) || slices.Contains(c.over, a.Name) {
			continue
		}
		if e.cutInARow(ctx, j.ConversationID, j.AgentID) > storage.MaxJobResumes { // its answer keeps bringing office down
			c.over = append(c.over, a.Name)
			continue
		}
		c.agents = append(c.agents, a.Name)
	}
	n := 0
	for _, id := range order {
		c := chats[id]
		conv, err := e.store.Chat().GetConversation(ctx, id)
		// the project's chats only: a bot's answers outsiders, a workflow's ended with it
		if err != nil || conv.Purpose != "" || conv.TaskID != "" || conv.Cleaned != "" {
			continue
		}
		if len(c.over) > 0 {
			e.note(conv, prompts.Render("chat/resume-over", map[string]any{"Agents": c.over, "Max": storage.MaxJobResumes}))
		}
		if len(c.agents) == 0 {
			continue
		}
		text := prompts.Render("chat/resumed", map[string]any{"Agents": c.agents})
		as, from := actor.With(ctx, c.who), e.lastAsked(ctx, id)
		// a message relayed from another chat or office goes on as it came:
		// where its answer goes, and no more rights than it had (ADR-176)
		var info relayInfo
		if json.Unmarshal([]byte(from.Context), &info) == nil && info.ConversationID != "" {
			if info.Peer != "" {
				as = withPeerFrom(as, info.Peer)
			}
		} else {
			from.Context = ""
		}
		if _, _, err := e.SendWithContext(as, id, text, from.Context, nil); err != nil {
			slog.Warn("chat: resume after restart", "conversation", id, "err", err)
			continue
		}
		n++
	}
	return n
}

// cutInARow is how many of agentID's latest turns in a chat in a row were
// cut off by office restarting (still running or ended "restart"): one that
// keeps bringing office down is not resumed for ever (ADR-257).
func (e *Engine) cutInARow(ctx context.Context, conversationID, agentID string) int {
	jobs, err := e.store.Jobs().List(ctx, storage.JobFilter{ConversationID: conversationID, Kind: "chat_turn", AgentID: agentID, Limit: storage.MaxJobResumes + 2})
	if err != nil {
		return 0
	}
	n := 0
	for _, j := range jobs {
		if j.Status != "running" && j.ErrorCode != "restart" {
			break
		}
		n++
	}
	return n
}

// lastAsked is the last message a person (or another chat) sent in it.
func (e *Engine) lastAsked(ctx context.Context, conversationID string) storage.Message {
	msgs, _ := e.store.Chat().ListMessages(ctx, conversationID)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i]
		}
	}
	return storage.Message{}
}
