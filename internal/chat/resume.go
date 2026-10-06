package chat

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
)

// After a person decides what an agent proposed on the dashboard, the agent
// goes on (ADR-084): its answer was over, it would otherwise stand still
// until someone wrote again. Decisions a few seconds apart in one chat are
// one message; a chat answering now waits for its answer to end.
const decidedWait = 3 * time.Second

type decidedBatch struct {
	who   string
	lines []string
	timer *time.Timer
	tries int
}

type decisions struct {
	mu      sync.Mutex
	pending map[string]*decidedBatch
	wait    time.Duration
	// onBot goes on with a bot chat's conversation (its answer goes back to the chat)
	onBot func(ctx context.Context, conversationID, who string, lines []string)
}

// SetOnBotDecided hands a bot chat's decisions to its bot (ADR-084).
func (e *Engine) SetOnBotDecided(fn func(ctx context.Context, conversationID, who string, lines []string)) {
	e.decided.mu.Lock()
	e.decided.onBot = fn
	e.decided.mu.Unlock()
}

// Decided notes that who (an office account's email) decided a proposal of
// conversationID; line says what and how it went.
func (e *Engine) Decided(conversationID, who, line string) {
	if conversationID == "" || line == "" {
		return
	}
	d := &e.decided
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending == nil {
		d.pending = map[string]*decidedBatch{}
	}
	wait := d.wait
	if wait == 0 {
		wait = decidedWait
	}
	b, ok := d.pending[conversationID]
	if !ok {
		b = &decidedBatch{}
		d.pending[conversationID] = b
		b.timer = time.AfterFunc(wait, func() { e.goOn(conversationID) })
	} else {
		b.timer.Reset(wait)
	}
	b.who = who
	b.lines = append(b.lines, line)
}

func (e *Engine) goOn(conversationID string) {
	ctx := context.Background()
	d := &e.decided
	d.mu.Lock()
	b := d.pending[conversationID]
	d.mu.Unlock()
	if b == nil {
		return
	}
	conv, err := e.store.Chat().GetConversation(ctx, conversationID)
	if err == nil && conv.Purpose == "channel" { // a bot's: it answers in its chat
		who, lines := e.takeDecided(conversationID)
		d.mu.Lock()
		onBot := d.onBot
		d.mu.Unlock()
		if onBot != nil {
			onBot(ctx, conversationID, who, lines)
		}
		return
	}
	if err != nil || conv.Purpose != "" || conv.TaskID != "" { // the project's own chats
		e.dropDecided(conversationID)
		return
	}
	if _, busy := e.Active(conversationID); busy || len(e.Running(conversationID)) > 0 {
		d.mu.Lock()
		if b.tries++; b.tries < 120 { // up to 10 minutes: its answer ends first
			b.timer.Reset(5 * time.Second)
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
		e.dropDecided(conversationID)
		return
	}
	who, lines := e.takeDecided(conversationID)
	text := "[office] " + who + " đã quyết các đề xuất của bạn:\n" + strings.Join(lines, "\n") +
		"\n\nLàm tiếp việc đang dở theo kết quả trên (không đề xuất lại những gì đã duyệt); xong thì báo ngắn gọn."
	_, _, _ = e.Send(actor.With(ctx, "human:"+who), conversationID, text, nil)
}

// takeDecided snapshots and removes a chat's pending decisions atomically, so
// callers never read who/lines while Decided is still appending to them.
func (e *Engine) takeDecided(conversationID string) (who string, lines []string) {
	d := &e.decided
	d.mu.Lock()
	defer d.mu.Unlock()
	if b := d.pending[conversationID]; b != nil {
		who = b.who
		lines = append([]string(nil), b.lines...)
		delete(d.pending, conversationID)
	}
	return who, lines
}

func (e *Engine) dropDecided(conversationID string) {
	e.decided.mu.Lock()
	delete(e.decided.pending, conversationID)
	e.decided.mu.Unlock()
}

// SetDecidedWait is how long decisions of one chat are gathered before its
// agent goes on (3 seconds by default).
func (e *Engine) SetDecidedWait(d time.Duration) {
	e.decided.mu.Lock()
	e.decided.wait = d
	e.decided.mu.Unlock()
}
