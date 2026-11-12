package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/storage"
)

// DecidePatch applying a diff straight to project.Path must not race with a
// turn that is writing directly into the same folder (EditDirect): it should
// see the same per-project busy flag run() sets and refuse, instead of
// running `git apply` on the folder while the other turn is still using it.
func TestDecidePatchBusyWhileDirectEditRunning(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	st, p, own := mergeStore(t)
	ctx := context.Background()
	os.WriteFile(filepath.Join(p.Path, "hello.txt"), []byte("hello\n"), 0o644)
	e := &Engine{store: st, acts: actions.New(st, nil)}
	msg, _ := st.Chat().AddMessage(ctx, storage.Message{ConversationID: own.ID, Role: "assistant", Content: "done"})
	diff := "diff --git a/hello.txt b/hello.txt\n--- a/hello.txt\n+++ b/hello.txt\n@@ -1 +1 @@\n-hello\n+xin chào\n"
	pat, err := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, MessageID: msg.ID, Diff: diff, Files: []string{"hello.txt"}, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}

	// simulate a turn in another chat currently writing directly into p.Path
	e.mu.Lock()
	if e.direct == nil {
		e.direct = map[string]bool{}
	}
	e.direct[p.ID] = true
	e.mu.Unlock()

	if _, err := e.DecidePatch(ctx, pat.ID, true); err != ErrProjectBusy {
		t.Fatalf("DecidePatch while project busy = %v, want ErrProjectBusy", err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.Path, "hello.txt")); string(b) != "hello\n" {
		t.Fatalf("patch must not be applied while busy, file = %q", b)
	}

	// clear the busy flag: the same patch applies now
	e.mu.Lock()
	e.direct[p.ID] = false
	e.mu.Unlock()
	d, err := e.DecidePatch(ctx, pat.ID, true)
	if err != nil || d.Status != "applied" {
		t.Fatalf("merge after busy cleared = %s %v", d.Status, err)
	}
	if b, _ := os.ReadFile(filepath.Join(p.Path, "hello.txt")); string(b) != "xin chào\n" {
		t.Fatalf("patch should now apply, file = %q", b)
	}
}
