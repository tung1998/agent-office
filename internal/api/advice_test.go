package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// What advisors told a chat's stuck agents: its own list, on the answer it
// went to, summed on the overview; the setting is checked (ADR-262).
func TestAdviceAPI(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "Sửa giỏ hàng"})
	m, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Content: "xong", Author: "Dev"})
	cost := 0.12
	e.st.Advice().Create(ctx, storage.Advice{ProjectID: pid, ConversationID: c.ID, JobID: "job_1", MessageID: m.ID, AgentName: "Dev", AdvisorName: "Codex", Model: "gpt-x",
		Trigger: "repeat", Command: "go test ./x", Answer: "Sai đường dẫn", Status: storage.AdviceAnswered, CostUSD: &cost})
	e.st.Advice().Create(ctx, storage.Advice{ProjectID: pid, ConversationID: c.ID, Trigger: "repeat", Command: "make", Status: storage.AdviceSkipped, Reason: "no agent"})

	res, b := do(t, admin, "GET", e.srv.URL+"/api/conversations/"+c.ID+"/advice", nil, nil)
	list, _ := b["advice"].([]any)
	if res.StatusCode != http.StatusOK || len(list) != 2 || list[0].(map[string]any)["advisor_name"] != "Codex" {
		t.Fatalf("advice = %d %v", res.StatusCode, b)
	}
	_, conv := do(t, admin, "GET", e.srv.URL+"/api/conversations/"+c.ID, nil, nil)
	found := false
	for _, x := range conv["messages"].([]any) {
		msg := x.(map[string]any)
		if msg["id"] == m.ID {
			adv, _ := msg["advice"].([]any)
			found = len(adv) == 1 && adv[0].(map[string]any)["answer"] == "Sai đường dẫn"
		}
	}
	if !found {
		t.Fatalf("advice not on its answer: %v", conv["messages"])
	}
	_, b = do(t, admin, "GET", e.srv.URL+"/api/advice/stats", nil, nil)
	st, _ := b["stats"].(map[string]any)
	if st == nil || st["asked"] != float64(1) || st["skipped"] != float64(1) || b["mode"] != "repeat" || b["daily_usd"] != float64(2) {
		t.Fatalf("stats = %v", b)
	}
	if res, _ := do(t, admin, "PUT", e.srv.URL+"/api/usage/settings", map[string]any{"auto_advisor": "always"}, nil); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad mode saved: %d", res.StatusCode)
	}
	if res, b := do(t, admin, "PUT", e.srv.URL+"/api/usage/settings", map[string]any{"auto_advisor": "repeat_done", "advisor_daily_usd": 5}, nil); res.StatusCode != http.StatusOK || b["auto_advisor"] != "repeat_done" {
		t.Fatalf("settings: %d %v", res.StatusCode, b)
	}
	if _, b = do(t, admin, "GET", e.srv.URL+"/api/advice/stats", nil, nil); b["mode"] != "repeat_done" || b["daily_usd"] != float64(5) {
		t.Fatalf("stats after settings = %v", b)
	}
}
