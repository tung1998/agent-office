package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
)

// /btw (ADR-273): a side question while the chat's agent works. The chat's
// agent answers it apart, in a hidden read-only chat that sees the chat's
// last messages and what the running answer did so far; the running answer
// goes on untouched. Its answer is kept in the chat as a card of its own,
// outside the history the agents read.

// BtwPurpose is the purpose of the hidden chat a /btw is answered in.
const BtwPurpose = "btw"

const (
	btwRecent   = 12   // the chat's last messages it sees
	btwEach     = 1500 // bytes of each
	btwProgress = 3000 // bytes of what the running answer did so far
	btwLimit    = 10 * time.Minute
)

type btwInfo struct {
	Btw string `json:"btw"`
}

// BtwOf is the question a /btw answer answers ("" = not one).
func BtwOf(m storage.Message) string {
	var info btwInfo
	if m.Role != "assistant" || !strings.HasPrefix(m.Context, `{"btw"`) || json.Unmarshal([]byte(m.Context), &info) != nil {
		return ""
	}
	return info.Btw
}

// IsBtw: the message is a /btw answer (not part of the chat's history).
func IsBtw(m storage.Message) bool { return BtwOf(m) != "" }

// Btw answers a side question in the chat and keeps the answer there; it
// returns the answer (the bot's command says it).
func (e *Engine) Btw(ctx context.Context, conversationID, question string) (storage.Message, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return storage.Message{}, errors.New("hãy viết câu hỏi sau /btw, ví dụ: /btw file nào đang sửa?")
	}
	conv, err := e.store.Chat().GetConversation(ctx, conversationID)
	if err != nil {
		return storage.Message{}, err
	}
	if conv.Cleaned != "" {
		return storage.Message{}, ErrCleaned
	}
	agent, err := e.agentFor(ctx, conv)
	if err != nil {
		return storage.Message{}, err
	}
	if e.usage != nil {
		if err := e.usage.Check(ctx, conv.ProjectID); err != nil {
			return storage.Message{}, err
		}
	}
	msgs, _ := e.store.Chat().ListMessages(ctx, conv.ID)
	text := prompts.Render("chat/btw", map[string]any{"Agent": agent.Name, "Recent": btwRecentOf(msgs), "Running": e.btwRunning(conv.ID), "Question": question})
	answer, err := e.askAside(ctx, conv.ProjectID, agent.ID, BtwPurpose, text, btwLimit)
	if err != nil {
		return storage.Message{}, err
	}
	info, _ := json.Marshal(btwInfo{Btw: question})
	m, err := e.store.Chat().AddMessage(context.Background(), storage.Message{ConversationID: conv.ID, Role: "assistant", Author: agent.Name,
		Content: strings.TrimSpace(answer), Context: string(info)})
	if err == nil {
		e.running(conv.ID) // the dashboard shows it
	}
	return m, err
}

// btwRecentOf is the chat's last messages as the /btw prompt shows them.
func btwRecentOf(msgs []storage.Message) string {
	var parts []string
	for i := len(msgs) - 1; i >= 0 && len(parts) < btwRecent; i-- {
		m := msgs[i]
		if m.Role != "user" && m.Role != "assistant" || IsBtw(m) || strings.TrimSpace(m.Content) == "" {
			continue
		}
		who := "The person"
		if m.Role == "assistant" {
			who = firstNonEmpty(m.Author, "Agent")
		}
		parts = append(parts, who+": "+tailBytes(strings.TrimSpace(m.Content), btwEach))
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.ReplaceAll(strings.Join(parts, "\n\n"), "```", "'''")
}

// btwRunning is what the chat's running answer did so far ("" = none runs).
func (e *Engine) btwRunning(conversationID string) string {
	t, ok := e.Active(conversationID)
	if !ok {
		return ""
	}
	evs, _, _ := t.Since(0)
	var b strings.Builder
	for _, ev := range evs {
		switch {
		case ev.Type == "tool" && ev.Tool != nil && ev.Tool.Summary != "":
			b.WriteString("- " + ev.Tool.Summary + "\n")
		case ev.Type == "text" && ev.Text != "":
			b.WriteString(ev.Text)
		}
	}
	return strings.ReplaceAll(tailBytes(strings.TrimSpace(b.String()), btwProgress), "```", "'''")
}
