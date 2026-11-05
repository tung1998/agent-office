package api_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/storage"
)

// POST /api/patches/{id}/deps asks again the install of a merged diff's
// dependencies (ADR-251): a card for each package; a diff not merged, or of
// code only, is refused (409).
func TestInstallPatchDepsAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "web"), 0o755)
	os.WriteFile(filepath.Join(dir, "web", "package.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "web", "pnpm-lock.yaml"), []byte(""), 0o644)
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir, "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	e.chat.SetActions(actions.New(e.st, nil))
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "x"})
	m, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Content: "xong"})
	merged, _ := e.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: c.ID, MessageID: m.ID, Diff: "x", Files: []string{"web/pnpm-lock.yaml"}, Status: "pending"})
	if err := e.st.Chat().DecidePatch(ctx, merged.ID, "applied", "", "admin@x.io", merged.CreatedAt); err != nil {
		t.Fatal(err)
	}
	pending, _ := e.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: c.ID, MessageID: m.ID, Diff: "x", Files: []string{"web/package.json"}, Status: "pending"})

	resp, b := do(t, admin, "POST", e.srv.URL+"/api/patches/"+merged.ID+"/deps", map[string]any{}, nil)
	acts, _ := b["actions"].([]any)
	if resp.StatusCode != 200 || len(acts) != 1 {
		t.Fatalf("install again = %d %v", resp.StatusCode, b)
	}
	a := acts[0].(map[string]any)
	if a["kind"] != "deps_install" || a["status"] != "pending" || a["target"] != "web (pnpm)" || a["patch"] != merged.ID {
		t.Fatalf("card = %v", a)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/patches/"+pending.ID+"/deps", map[string]any{}, nil); resp.StatusCode != 409 {
		t.Fatalf("a diff not merged = %d, want 409", resp.StatusCode)
	}
}
