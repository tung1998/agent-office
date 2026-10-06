package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
)

// A big diff comes with the chat cut short (whole lines, counts of the whole);
// GET /api/patches/{id} gives all of it.
func TestBigPatchCutInHistory(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "big"})
	m, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Content: "xong"})
	diff := "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n" + strings.Repeat("+dòng thêm vào cho đủ dài\n", 20000)
	p, err := e.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: c.ID, MessageID: m.ID, Diff: diff, Files: []string{"x.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	hist, err := e.chat.History(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := hist[0].Patches[0]
	if !got.Truncated || len(got.Diff) > chat.PatchPreview || !strings.HasPrefix(diff, got.Diff+"\n") || got.Size != len(diff) || got.Add != 20000 {
		t.Fatalf("cut = %v len %d size %d add %d", got.Truncated, len(got.Diff), got.Size, got.Add)
	}
	_, b := do(t, admin, "GET", e.srv.URL+"/api/patches/"+p.ID, nil, nil)
	if full := b["patch"].(map[string]any)["diff"].(string); full != diff {
		t.Fatalf("full diff = %d bytes, want %d", len(full), len(diff))
	}
}
