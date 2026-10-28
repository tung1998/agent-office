package chat_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/verify"
	"github.com/tung1998/agent-office/internal/worktree"
)

// A diff office would merge on its own is checked first (ADR-222): failing,
// it waits and its agent gets a turn to fix it; passing, it is merged and
// says what it passed.
func TestMergeCheckHoldsThenFixTurnMerges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := fakeAnthropic(t, "your own git worktree", []map[string]any{
		{"name": "edit_file", "input": map[string]any{"path": "hello.txt", "old": "hello", "new": "xin chào"}},
		{"name": "write_file", "input": map[string]any{"path": "extra.txt", "content": "ok\n"}},
	})
	defer srv.Close()
	f := setup(t, func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, _ := provs.Create(context.Background(), provider.Input{Name: "C", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})
		return p
	})
	gitRun(t, f.dir, "init", "-q")
	gitRun(t, f.dir, "add", "-A")
	gitRun(t, f.dir, "commit", "-q", "-m", "init")
	f.engine.SetWorktrees(worktree.New(filepath.Join(t.TempDir(), "worktrees")))
	ctx := actor.With(context.Background(), "human:a@b.c")
	pol := perm.LoadPolicy(ctx, f.st, f.project.ID)
	pol.MergeChecks = "test -f extra.txt"
	if err := perm.SavePolicy(ctx, f.st, f.project.ID, pol); err != nil {
		t.Fatal(err)
	}
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	f.engine.SetMode(ctx, conv.ID, perm.Edit)

	turn, _, err := f.engine.Send(ctx, conv.ID, "đổi lời chào", nil)
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, turn)
	done := evs[len(evs)-1]
	if m := done.Message; len(m.Patches) != 1 || m.Patches[0].Status != "pending" || !strings.HasPrefix(m.Patches[0].Detail, verify.Held) {
		t.Fatalf("the check fails: the diff waits, got %+v", m.Patches)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "hello.txt")); strings.Contains(string(b), "xin chào") {
		t.Fatal("a diff that failed its check reached the project")
	}
	next, ok := f.engine.Turn(done.NextTurnID)
	if !ok {
		t.Fatal("no turn opened to fix the failed check")
	}
	evs = collect(t, next)
	m := evs[len(evs)-1].Message
	if len(m.Patches) != 1 || m.Patches[0].Status != "applied" || !strings.HasPrefix(m.Patches[0].Detail, verify.Passed) {
		t.Fatalf("fixed: merged, saying it passed, got %+v", m.Patches)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "extra.txt")); err != nil {
		t.Fatal("the fixed diff is in the project")
	}
	all, _ := f.st.Chat().ListPatches(ctx, conv.ID)
	if all[0].Status != "rejected" || !strings.HasPrefix(all[0].Detail, verify.Held) {
		t.Fatalf("the held diff, replaced, still says it was held: %+v", all[0])
	}
}
