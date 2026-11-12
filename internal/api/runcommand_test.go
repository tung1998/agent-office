package api_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// ▶ on a chat's one-line shell block: the person runs it in the project
// folder; a card on that message shows the output. A shell line or a message
// of another chat is refused.
func TestRunChatCommand(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("xin chào"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir, "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "lệnh"})
	m, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Content: "```sh\ncat hello.txt\n```", Author: "Dev"})
	other, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "khác"})
	om, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: other.ID, Role: "assistant", Content: "x", Author: "Dev"})
	url := e.srv.URL + "/api/conversations/" + c.ID + "/run"

	resp, b := do(t, admin, "POST", url, map[string]any{"command": "cat hello.txt", "message_id": m.ID}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("run = %d %v", resp.StatusCode, b)
	}
	a := b["action"].(map[string]any)
	if a["status"] != "done" || !strings.Contains(a["detail"].(string), "xin chào") || a["message_id"] != m.ID || a["decided_by"] != "admin@x.io" {
		t.Fatalf("card = %v", a)
	}
	if resp, b := do(t, admin, "POST", url, map[string]any{"command": "cat hello.txt | wc"}, nil); resp.StatusCode != 400 {
		t.Fatalf("shell line = %d %v", resp.StatusCode, b)
	}
	if resp, b := do(t, admin, "POST", url, map[string]any{"command": "ls\nrm -rf x"}, nil); resp.StatusCode != 400 {
		t.Fatalf("two lines = %d %v", resp.StatusCode, b)
	}
	if resp, b := do(t, admin, "POST", url, map[string]any{"command": "ls", "message_id": om.ID}, nil); resp.StatusCode != 400 {
		t.Fatalf("other chat's message = %d %v", resp.StatusCode, b)
	}
}
