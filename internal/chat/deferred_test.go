package chat_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/worktree"
)

// deferredFixture: a chat in its own worktree, two turns (an edit, then a
// new file), its first diff waiting for a person.
func deferredFixture(t *testing.T) (fixture, storage.Conversation, func(string) chat.MessageDTO) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := fakeAnthropic(t, "your own git worktree", []map[string]any{
		{"name": "edit_file", "input": map[string]any{"path": "hello.txt", "old": "hello", "new": "xin chào"}},
		{"name": "write_file", "input": map[string]any{"path": "extra.txt", "content": "x\n"}},
	})
	t.Cleanup(srv.Close)
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
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	say := func(text string) chat.MessageDTO {
		t.Helper()
		turn, _, err := f.engine.Send(ctx, conv.ID, text, nil)
		if err != nil {
			t.Fatal(err)
		}
		evs := collect(t, turn)
		return *evs[len(evs)-1].Message
	}
	return f, conv, say
}

// ADR-189: a commit held for a diff that a later turn's diff replaced is
// carried onto the new diff (after it, with its files), not left before it
// where it would block the new diff, then run first and fail (or never run).
func TestDeferredCommitFollowsTheDiffThatReplacedIt(t *testing.T) {
	f, conv, say := deferredFixture(t)
	ctx := actor.With(context.Background(), "human:a@b.c")
	first := say("đổi lời chào")
	if len(first.Patches) != 1 || first.Patches[0].Status != "pending" {
		t.Fatalf("first diff = %+v", first.Patches)
	}
	// the agent asked a commit with its edits: approved on its own permission,
	// queued behind its diff (RunDeferred, blocked)
	commit, err := f.st.Actions().Create(ctx, storage.Action{ProjectID: f.project.ID, ConversationID: conv.ID, MessageID: first.ID, Kind: "git_commit",
		Target: "feat: greet", Status: "queued", DecidedBy: "auto:Lead (Sửa)", ProposedBy: "Lead",
		Args: storage.ActionArgs{Message: "feat: greet", Files: []string{"hello.txt"}, AfterDiff: true}})
	if err != nil {
		t.Fatal(err)
	}
	second := say("thêm file") // the review asked for more: its diff replaces the first
	if len(second.Patches) != 1 || second.Patches[0].Status != "pending" {
		t.Fatalf("second diff = %+v", second.Patches)
	}
	newPatch := second.Patches[0]
	if err := f.engine.CheckOrder(ctx, conv.ID, newPatch.ID); err != nil {
		t.Fatalf("the new diff waits behind the commit held for the old one: %v", err)
	}
	got, _ := f.st.Actions().Get(ctx, commit.ID)
	if got.MessageID != second.ID || got.Status != "queued" || !slices.Contains(got.Args.Files, "extra.txt") || !slices.Contains(got.Args.Files, "hello.txt") {
		t.Fatalf("commit not carried onto the new diff: %+v", got)
	}
	// the person merges the diff: the queued commit runs after it, with both files
	svc := actions.New(f.st, nil)
	f.engine.SetQueueRunner(func(ctx context.Context, kind, id, by string) string {
		a, err := svc.Decide(ctx, id, true, by)
		if err != nil {
			return "⚠️ " + err.Error()
		}
		return "✅ " + a.Detail
	})
	if p, err := f.engine.DecidePatch(ctx, newPatch.ID, true); err != nil || p.Status != "applied" {
		t.Fatalf("merge = %+v %v", p, err)
	}
	f.engine.Drain(conv.ID)
	got, _ = f.st.Actions().Get(ctx, commit.ID)
	if got.Status != "done" {
		t.Fatalf("commit after merge = %s %s", got.Status, got.Detail)
	}
	out, _ := exec.Command("git", "-C", f.dir, "show", "--name-only", "--format=%s", "HEAD").CombinedOutput()
	if s := string(out); !strings.Contains(s, "feat: greet") || !strings.Contains(s, "hello.txt") || !strings.Contains(s, "extra.txt") {
		t.Fatalf("HEAD = %s", s)
	}
}
