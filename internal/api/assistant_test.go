package api_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-046: the office assistant's project is hidden from the projects, has
// its own endpoint, and each person sees only their own assistant chats.
func TestAssistantProject(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	id, err := assistant.Ensure(ctx, e.st, t.TempDir())
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
	id, _ := assistant.Ensure(ctx, e.st, t.TempDir())
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

// has_more/pagination must be computed after the per-user filter on assistant
// chats, or a page full of another person's chats starves "load more" even
// though the asking person still has older chats of their own.
func TestAssistantChatsPaginateOwnOnly(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	id, _ := assistant.Ensure(ctx, e.st, t.TempDir())
	// admin's chats first (oldest), then a pile of member's newer ones, so
	// naive limit+1-then-filter paging fetches only member chats at first and
	// never reaches admin's older chats.
	for i := 0; i < 3; i++ {
		if _, err := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: id, Title: "admin", CreatedBy: "human:admin@x.io"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 8; i++ {
		if _, err := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: id, Title: "member", CreatedBy: "human:member@x.io"}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	before := ""
	for page := 0; page < 10; page++ {
		url := e.srv.URL + "/api/projects/" + id + "/conversations?limit=2"
		if before != "" {
			url += "&before=" + before
		}
		resp, body := do(t, admin, "GET", url, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("page %d status = %d", page, resp.StatusCode)
		}
		list := body["conversations"].([]any)
		for _, x := range list {
			c := x.(map[string]any)
			if c["title"] != "admin" {
				t.Fatalf("saw another person's chat: %v", c)
			}
			seen[c["id"].(string)] = true
		}
		if body["has_more"] != true {
			break
		}
		if len(list) == 0 {
			t.Fatal("has_more=true but page came back empty: pagination can't advance")
		}
		before = list[len(list)-1].(map[string]any)["updated_at"].(string)
		if page == 9 {
			t.Fatal("did not terminate within 10 pages")
		}
	}
	if len(seen) != 3 {
		t.Fatalf("admin should see all 3 of their own chats across pages, got %d", len(seen))
	}
}

// ADR-240: a member (and a manager) has the assistant too: its own chats
// there, never another's, and none of the assistant's settings or notes.
func TestAssistantForEveryone(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id, _ := assistant.Ensure(ctx, e.st, t.TempDir())
	m := e.client(t)
	login(t, e, m, "member@x.io", "member-password")
	resp, body := do(t, m, "GET", e.srv.URL+"/api/assistant", nil, nil)
	if resp.StatusCode != 200 || body["project_id"] != id {
		t.Fatalf("assistant = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, m, "POST", e.srv.URL+"/api/projects/"+id+"/conversations", map[string]any{}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("member opens a chat = %d %v", resp.StatusCode, body)
	}
	mine := body["conversation"].(map[string]any)["id"].(string)
	theirs, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: id, Title: "của admin", CreatedBy: "human:admin@x.io"})
	_, body = do(t, m, "GET", e.srv.URL+"/api/projects/"+id+"/conversations", nil, nil)
	if list := body["conversations"].([]any); len(list) != 1 || list[0].(map[string]any)["id"] != mine {
		t.Fatalf("member's assistant chats = %v", list)
	}
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/conversations/" + mine, 200},
		{"GET", "/api/conversations/" + theirs.ID, 404},
		{"POST", "/api/conversations/" + theirs.ID + "/messages", 404},
		{"GET", "/api/projects/" + id, 404},             // its project page
		{"GET", "/api/projects/" + id + "/policy", 404}, // its settings
		{"GET", "/api/memories?project=" + id, 404},     // its notes, the whole office's
	} {
		if resp, _ := do(t, m, c.method, e.srv.URL+c.path, map[string]any{"text": "x"}, nil); resp.StatusCode != c.want && !(c.want == 404 && resp.StatusCode == 403) {
			t.Fatalf("%s %s = %d", c.method, c.path, resp.StatusCode)
		}
	}
	if resp, _ := do(t, m, "PUT", e.srv.URL+"/api/assistant/mode", map[string]any{"mode": "admin"}, nil); resp.StatusCode != 403 {
		t.Fatalf("member sets the assistant's mode = %d", resp.StatusCode)
	}
}
