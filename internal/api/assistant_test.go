package api_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-046: the office assistant's project is hidden from the projects, has
// its own endpoint, and each person sees only their own assistant chats.
func TestAssistantProject(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	id, err := assistant.Ensure(ctx, e.st, orgmodel.NewService(e.st), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, body := do(t, admin, "GET", e.srv.URL+"/api/projects", nil, nil)
	for _, p := range body["projects"].([]any) {
		if p.(map[string]any)["id"] == id {
			t.Fatal("the assistant's project is listed")
		}
	}
	resp, body := do(t, admin, "GET", e.srv.URL+"/api/assistant", nil, nil)
	if resp.StatusCode != 200 || body["project_id"] != id {
		t.Fatalf("assistant = %d %v", resp.StatusCode, body)
	}
	e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: id, Title: "của admin", CreatedBy: "human:admin@x.io"})
	e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: id, Title: "của member", CreatedBy: "human:member@x.io"})
	_, body = do(t, admin, "GET", e.srv.URL+"/api/projects/"+id+"/conversations", nil, nil)
	list := body["conversations"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["title"] != "của admin" {
		t.Fatalf("admin sees %v", list)
	}
}

// Review I1: another person's assistant chat cannot be opened, written or deleted.
func TestAssistantChatsArePrivate(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	id, _ := assistant.Ensure(ctx, e.st, orgmodel.NewService(e.st), t.TempDir())
	theirs, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: id, Title: "riêng", CreatedBy: "human:member@x.io"})
	if resp, _ := do(t, admin, "GET", e.srv.URL+"/api/conversations/"+theirs.ID, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("read another's = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/conversations/"+theirs.ID+"/messages", map[string]any{"text": "x"}, nil); resp.StatusCode != 404 {
		t.Fatalf("write another's = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "DELETE", e.srv.URL+"/api/conversations/"+theirs.ID, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("delete another's = %d", resp.StatusCode)
	}
	mine, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: id, Title: "của tôi", CreatedBy: "human:admin@x.io"})
	if resp, _ := do(t, admin, "GET", e.srv.URL+"/api/conversations/"+mine.ID, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("read mine = %d", resp.StatusCode)
	}
}
