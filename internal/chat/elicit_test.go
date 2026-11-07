package chat_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// An MCP server's elicitation/create lands as a question in the run's own
// chat, and the next thing a person writes there becomes its JSON-RPC
// answer (ADR-264) — the integration the mcpgateway package cannot cover on
// its own, since asking in a chat is the chat package's job.
func TestAskElicit(t *testing.T) {
	f := setup(t, func(provs *provider.Service) storage.Provider { return storage.Provider{} })
	ctx := actor.With(context.Background(), "human:a@x")
	conv, err := f.engine.StartConversation(ctx, f.project.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	schema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`)

	type res struct {
		action  string
		content json.RawMessage
		err     error
	}
	out := make(chan res, 1)
	go func() {
		a, c, err := f.engine.AskElicit(context.Background(), officetools.Scope{ConversationID: conv.ID}, "myserver", "cần xác nhận địa chỉ giao hàng", schema)
		out <- res{a, c, err}
	}()

	// the question appears in the chat before the person answers
	var asked bool
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		msgs, _ := f.st.Chat().ListMessages(context.Background(), conv.ID)
		for _, m := range msgs {
			if strings.Contains(m.Content, "myserver") && strings.Contains(m.Content, "cần xác nhận địa chỉ giao hàng") {
				asked = true
			}
		}
		if asked {
			break
		}
	}
	if !asked {
		t.Fatal("question never appeared in the chat")
	}

	if _, err := f.st.Chat().AddMessage(context.Background(), storage.Message{
		ConversationID: conv.ID, Role: "user", Author: "human:a@x", Content: `{"ok":true}`}); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-out:
		if r.err != nil {
			t.Fatalf("err = %v", r.err)
		}
		if r.action != "accept" {
			t.Fatalf("action = %q", r.action)
		}
		var content struct {
			OK bool `json:"ok"`
		}
		if json.Unmarshal(r.content, &content) != nil || !content.OK {
			t.Fatalf("content = %s", r.content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskElicit never returned after the reply")
	}
}

// No reply before ctx ends: AskElicit declines rather than waiting forever.
func TestAskElicitTimeout(t *testing.T) {
	f := setup(t, func(provs *provider.Service) storage.Provider { return storage.Provider{} })
	ctx := actor.With(context.Background(), "human:a@x")
	conv, err := f.engine.StartConversation(ctx, f.project.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	action, content, err := f.engine.AskElicit(waitCtx, officetools.Scope{ConversationID: conv.ID}, "myserver", "hỏi gì đó", nil)
	if err != nil || action != "decline" || content != nil {
		t.Fatalf("action=%q content=%s err=%v", action, content, err)
	}
}
