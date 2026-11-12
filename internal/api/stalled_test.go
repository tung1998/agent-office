package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// A chat stopped because the AI ran out of tokens is its own item in "Cần xử
// lý" (not one of the failed jobs), with what Tiếp tục sends, until it goes on.
func TestStalledChatIncident(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "Sửa giỏ hàng"})
	now := time.Now().UTC()
	j, _ := e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", ConversationID: c.ID, Title: "x", Status: "running", StartedAt: &now})
	e.st.Jobs().Finish(ctx, j.ID, "failed", "out_of_tokens", "claude: Claude AI usage limit reached", now)

	list := func() map[string]map[string]any {
		_, b := do(t, admin, "GET", e.srv.URL+"/api/incidents", nil, nil)
		out := map[string]map[string]any{}
		for _, x := range b["incidents"].([]any) {
			it := x.(map[string]any)
			out[it["kind"].(string)] = it
		}
		return out
	}
	got := list()
	st := got["stalled"]
	if st == nil || st["id"] != c.ID || st["title"] != "Sửa giỏ hàng" || st["prompt"] == "" || st["link"] == "" {
		t.Fatalf("incidents = %v", got)
	}
	if _, ok := got["jobs"]; ok {
		t.Fatalf("counted as a failed job too: %v", got["jobs"])
	}
	// it went on: no longer waiting
	later := now.Add(time.Second)
	e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", ConversationID: c.ID, Status: "done", CreatedAt: later})
	if _, ok := list()["stalled"]; ok {
		t.Fatal("still stalled after it went on")
	}
}
