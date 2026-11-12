package chat

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/attach"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// Messages written while a chat answers wait in office (chat_queued), not in
// the browser: leaving the page loses nothing. Once the chat is free they go
// together as its next message, as the person who wrote them.

// queueLocks: one sender of a chat's waiting messages at a time.
var queueLocks sync.Map // conversation id → *sync.Mutex

// Queue keeps a message for when the chat is free (sent at once if it is
// free now); opts are the chat settings it changes, applied when it goes.
func (e *Engine) Queue(ctx context.Context, conversationID, text, pageContext string, attachmentIDs []string, opts storage.QueuedOptions) (storage.QueuedMessage, error) {
	text = strings.TrimSpace(text)
	if text == "" && len(attachmentIDs) == 0 {
		return storage.QueuedMessage{}, errors.New("tin nhắn trống")
	}
	conv, err := e.store.Chat().GetConversation(ctx, conversationID)
	if err != nil {
		return storage.QueuedMessage{}, err
	}
	if conv.Cleaned != "" {
		return storage.QueuedMessage{}, ErrCleaned
	}
	files, err := e.files.Resolve(conv.ProjectID, attachmentIDs)
	if err != nil {
		return storage.QueuedMessage{}, err
	}
	q, err := e.store.Chat().QueueMessage(ctx, storage.QueuedMessage{ConversationID: conv.ID, Author: actor.From(ctx), Text: text,
		Context: pageData(pageContext), Attachments: attach.Refs(files), Options: opts})
	if err != nil {
		return q, err
	}
	safeGo("SendQueued", func() { e.SendQueued(conv.ID) }) // the answer may have ended meanwhile
	return q, nil
}

// HasQueued: messages wait in the chat (a new one waits behind them).
func (e *Engine) HasQueued(ctx context.Context, conversationID string) bool {
	list, err := e.store.Chat().QueuedMessages(ctx, conversationID)
	return err == nil && len(list) > 0
}

// QueuedWorkflow: a workflow call waits in the chat (an agent's run_workflow
// runs once its turn ends).
func (e *Engine) QueuedWorkflow(ctx context.Context, conversationID string) bool {
	list, err := e.store.Chat().QueuedMessages(ctx, conversationID)
	if err != nil {
		return false
	}
	for _, q := range list {
		if _, _, ok := workflow.ParseCall(q.Text); ok {
			return true
		}
	}
	return false
}

// SendQueued sends a free chat's waiting messages as one. One that cannot
// go (budget, agent busy elsewhere…) is said in the chat with its text, not
// lost; a chat answering again keeps them for its next end.
func (e *Engine) SendQueued(conversationID string) {
	mu, _ := queueLocks.LoadOrStore(conversationID, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if _, busy := e.Active(conversationID); busy {
		return
	}
	ctx := context.Background()
	list, err := e.store.Chat().QueuedMessages(ctx, conversationID)
	if err != nil || len(list) == 0 {
		return
	}
	var (
		texts, files, ids []string
		opts              storage.QueuedOptions
	)
	for _, q := range list {
		if q.Text != "" {
			texts = append(texts, q.Text)
		}
		for _, a := range q.Attachments {
			files = append(files, a.ID)
		}
		ids = append(ids, q.ID)
		// the latest settings win
		opts.Mode = firstNonEmpty(q.Options.Mode, opts.Mode)
		opts.EditMode = firstNonEmpty(q.Options.EditMode, opts.EditMode)
		opts.AgentID = firstNonEmpty(q.Options.AgentID, opts.AgentID)
		if q.Options.Effort != nil {
			opts.Effort = q.Options.Effort
		}
	}
	text := strings.Join(texts, "\n\n")
	ctx = actor.With(ctx, list[0].Author)
	err = e.applyQueued(ctx, conversationID, opts)
	if err == nil {
		if opts.AgentID == "" { // the person's own messages, no agent picked: may be auto-routed (ADR-277)
			ctx = WithAutoRoute(ctx)
		}
		_, _, err = e.SendWithContext(ctx, conversationID, text, list[len(list)-1].Context, files)
	}
	var off *OffError
	switch {
	case errors.Is(err, ErrBusy) || errors.Is(err, ErrAgentBusy):
		return // its next end sends them
	case errors.Is(err, ErrStopping):
		return // the next office sends them (SendAllQueued)
	case err != nil && !errors.As(err, &off): // off: the message and the notice are in the chat
		if conv, cerr := e.store.Chat().GetConversation(ctx, conversationID); cerr == nil {
			e.note(conv, "Không gửi được tin nhắn chờ ("+err.Error()+"):\n\n"+text) // i18n-ignore
		}
	}
	_ = e.store.Chat().DeleteQueued(context.Background(), conversationID, ids...)
}

// SendAllQueued sends what waited in the chats when office stopped.
func (e *Engine) SendAllQueued(ctx context.Context) {
	convs, err := e.store.Chat().QueuedConversations(ctx)
	if err != nil {
		return
	}
	for _, id := range convs {
		safeGo("SendQueued", func() { e.SendQueued(id) })
	}
}

func (e *Engine) applyQueued(ctx context.Context, conversationID string, opts storage.QueuedOptions) error {
	if opts.Effort != nil {
		if err := e.SetEffort(ctx, conversationID, *opts.Effort); err != nil {
			return err
		}
	}
	if opts.AgentID != "" {
		if err := e.SetAgent(ctx, conversationID, opts.AgentID); err != nil {
			return err
		}
	}
	if opts.Mode != "" {
		if err := e.SetMode(ctx, conversationID, opts.Mode); err != nil {
			return err
		}
	}
	if opts.EditMode != "" {
		return e.SetEditMode(ctx, conversationID, opts.EditMode)
	}
	return nil
}
