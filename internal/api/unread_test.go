package api_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// A chat the person wrote in, answered since they looked, is in "Cần xử lý"
// (theirs alone); looking takes it off; marking it unread brings it back.
func TestUnreadIncident(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "sửa X", CreatedBy: "human:admin@x.io"})
	e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Author: "human:admin@x.io", Content: "sửa X"})
	e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Author: "Dev", Content: "xong"})
	unread := func() bool {
		_, body := do(t, admin, "GET", e.srv.URL+"/api/incidents", nil, nil)
		for _, x := range body["incidents"].([]any) {
			if x := x.(map[string]any); x["kind"] == "unread" && x["id"] == c.ID {
				return true
			}
		}
		return false
	}
	if !unread() {
		t.Fatal("an answered chat is not unread")
	}
	if res, _ := do(t, admin, "POST", e.srv.URL+"/api/conversations/"+c.ID+"/seen", nil, nil); res.StatusCode != 204 || unread() {
		t.Fatalf("seen = %d, still unread %v", res.StatusCode, unread())
	}
	do(t, admin, "POST", e.srv.URL+"/api/conversations/"+c.ID+"/seen", map[string]any{"seen": false}, nil)
	if !unread() {
		t.Fatal("marked unread: not back")
	}
}

// ADR-085: a project's chats come 20 at a time, the next page from the last
// one's time.
func TestConversationPages(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	for i := 0; i < 25; i++ {
		e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: fmt.Sprint("c", i), CreatedBy: "human:admin@x.io"})
		time.Sleep(2 * time.Millisecond)
	}
	_, first := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/conversations?source=all", nil, nil)
	list := first["conversations"].([]any)
	if len(list) != 20 || first["has_more"] != true {
		t.Fatalf("first page = %d, more %v", len(list), first["has_more"])
	}
	last := list[19].(map[string]any)["updated_at"].(string)
	_, next := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/conversations?source=all&before="+url.QueryEscape(last), nil, nil)
	if n := len(next["conversations"].([]any)); n != 5 || next["has_more"] != false {
		t.Fatalf("next page = %d, more %v", n, next["has_more"])
	}
}
