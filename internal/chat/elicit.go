package chat

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/storage"
)

// elicitPoll: how often AskElicit checks the chat for the person's reply.
var elicitPoll = 500 * time.Millisecond

// AskElicit turns an MCP server's elicitation/create into a question in sc's
// own chat (ADR-264): a note with the server's message and its schema (for a
// person to answer free-form, there being no form renderer yet), then the
// next message a person writes there, in the original wait's own schedule —
// ctx ending (the server's elicitTimeout, the run dying, the process dying)
// declines without asking further. A quick, line-based answer: accept with
// the reply under the schema's one property if it has exactly one, else try
// to parse it as JSON, else under "value"; blank declines.
func (e *Engine) AskElicit(ctx context.Context, sc officetools.Scope, server, message string, schema json.RawMessage) (action string, content json.RawMessage, err error) {
	if sc.ConversationID == "" {
		return "decline", nil, nil
	}
	conv, err := e.store.Chat().GetConversation(context.Background(), sc.ConversationID)
	if err != nil || conv.Cleaned != "" {
		return "decline", nil, nil
	}
	text := "MCP " + server + " cần hỏi thêm trước khi làm tiếp: " + message
	if len(schema) > 0 && string(schema) != "null" {
		text += "\nTrả lời ngay trong chat (JSON theo gợi ý này nếu cần nhiều trường): " + string(schema)
	}
	asked, err := e.store.Chat().AddMessage(context.Background(), storage.Message{ConversationID: conv.ID, Role: "error", Content: text})
	if err != nil {
		return "decline", nil, nil
	}
	for {
		select {
		case <-ctx.Done():
			return "decline", nil, nil
		case <-time.After(elicitPoll):
		}
		msgs, err := e.store.Chat().ListMessages(context.Background(), conv.ID)
		if err != nil {
			continue
		}
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			if !m.CreatedAt.After(asked.CreatedAt) {
				break // nothing left past the question to check
			}
			if m.Role == "user" && strings.HasPrefix(m.Author, "human:") {
				a, c := elicitReply(m.Content, schema)
				return a, c, nil
			}
		}
	}
}

// elicitReply turns a person's free-text reply into an MCP elicitation
// result: JSON they typed is kept as is; otherwise it becomes the lone
// value of schema's one property, or "value" when schema has none or many.
func elicitReply(text string, schema json.RawMessage) (string, json.RawMessage) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "decline", nil
	}
	var parsed json.RawMessage
	if json.Unmarshal([]byte(text), &parsed) == nil {
		if t := strings.TrimSpace(string(parsed)); len(t) > 0 && (t[0] == '{' || t[0] == '[') {
			return "accept", parsed
		}
	}
	key := soleProperty(schema)
	if key == "" {
		key = "value"
	}
	b, _ := json.Marshal(map[string]string{key: text})
	return "accept", b
}

// soleProperty is schema's one property name, or "" when it has none or
// several (elicitReply then falls back to a generic "value" field).
func soleProperty(schema json.RawMessage) string {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(schema, &s) != nil || len(s.Properties) != 1 {
		return ""
	}
	for k := range s.Properties {
		return k
	}
	return ""
}
