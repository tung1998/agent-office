package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
)

// The latest usage windows of each connection, for the sidebar and chat.
func TestProviderLimits(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/providers", map[string]any{"name": "CC", "kind": "claude_cli"}, nil)
	pid := body["provider"].(map[string]any)["id"].(string)
	reset := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	e.st.Settings().Set(context.Background(), chat.LimitsKey(pid), chat.Limits{Status: "allowed", UpdatedAt: time.Now().UTC(),
		Windows: map[string]chat.LimitWindow{"five_hour": {Utilization: 0.19, ResetsAt: reset}}})
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	resp, body := do(t, member, "GET", e.srv.URL+"/api/providers/limits", nil, nil)
	list, _ := body["limits"].([]any)
	if resp.StatusCode != 200 || len(list) != 1 {
		t.Fatalf("limits = %d %v", resp.StatusCode, body)
	}
	l := list[0].(map[string]any)
	w := l["windows"].(map[string]any)["five_hour"].(map[string]any)
	if l["provider_id"] != pid || l["provider_name"] != "CC" || l["is_default"] != true || w["utilization"] != 0.19 {
		t.Fatalf("entry = %v", l)
	}
}

// A chat lists its members (ADR-044): each agent with its rights and context.
func TestConversationMembers(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, err := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid})
	if err != nil {
		t.Fatal(err)
	}
	e.st.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: c.ID, AgentID: "agt_a", AgentName: "Lead", ContextTokens: 10, ContextWindow: 100})
	e.st.Chat().UpsertMember(ctx, storage.ChatMember{ConversationID: c.ID, AgentID: "agt_b", AgentName: "Dev"})
	resp, body := do(t, admin, "GET", e.srv.URL+"/api/conversations/"+c.ID, nil, nil)
	members, _ := body["members"].([]any)
	if resp.StatusCode != 200 || len(members) != 2 {
		t.Fatalf("get = %d %v", resp.StatusCode, body)
	}
	m := members[0].(map[string]any)
	if m["agent_name"] != "Lead" || m["context_window"] != float64(100) {
		t.Fatalf("member = %v", m)
	}
}
