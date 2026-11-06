package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
)

// A chat whose last answer died on a passing API failure and goes on by
// itself later is its own item ("retrying", not a failed job), its plan is in
// the chat's data, and Thôi calls it off (ADR-260).
func TestRetryingChat(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "Sửa giỏ hàng"})
	now := time.Now().UTC()
	j, _ := e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", ConversationID: c.ID, Title: "x", Status: "running", StartedAt: &now})
	e.st.Jobs().Finish(ctx, j.ID, "failed", chat.TransientCode, "claude: exit status 1 · lỗi API cuối: 529 overloaded_error", now)
	at := now.Add(10 * time.Minute)
	e.st.Jobs().SetRetry(ctx, j.ID, &at, 2)

	kinds := func() map[string]map[string]any {
		_, b := do(t, admin, "GET", e.srv.URL+"/api/incidents", nil, nil)
		out := map[string]map[string]any{}
		for _, x := range b["incidents"].([]any) {
			it := x.(map[string]any)
			out[it["kind"].(string)] = it
		}
		return out
	}
	got := kinds()
	if it := got["retrying"]; it == nil || it["id"] != c.ID || it["title"] != "Sửa giỏ hàng" {
		t.Fatalf("incidents = %v", got)
	}
	if _, ok := got["jobs"]; ok {
		t.Fatalf("counted as a failed job too: %v", got["jobs"])
	}
	_, conv := do(t, admin, "GET", e.srv.URL+"/api/conversations/"+c.ID, nil, nil)
	if r, _ := conv["retry"].(map[string]any); r == nil || r["job_id"] != j.ID || r["n"] != float64(2) || r["max"] != float64(3) {
		t.Fatalf("retry = %v", conv["retry"])
	}
	if res, _ := do(t, admin, "DELETE", e.srv.URL+"/api/conversations/"+c.ID+"/retry", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("cancel: %d", res.StatusCode)
	}
	if _, ok := kinds()["retrying"]; ok {
		t.Fatal("still retrying after Thôi")
	}
	if res, _ := do(t, admin, "POST", e.srv.URL+"/api/conversations/"+c.ID+"/retry-now", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("retry-now with nothing planned: %d", res.StatusCode)
	}
}
