package chat_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// queueRunner: queued cards run as a person's approval does.
func queueRunner(f fixture) {
	svc := actions.New(f.st, nil)
	f.engine.SetQueueRunner(func(ctx context.Context, kind, id, by string) string {
		if kind == "patch" {
			p, err := f.engine.DecidePatch(actor.With(ctx, by), id, true)
			if err != nil {
				return "⚠️ " + err.Error()
			}
			return "✅ " + p.Status
		}
		a, err := svc.Decide(ctx, id, true, by)
		if err != nil {
			return "⚠️ " + err.Error()
		}
		return "✅ " + a.Detail
	})
}

func heldCommit(t *testing.T, f fixture, conv storage.Conversation, msgID, status string, files ...string) storage.Action {
	t.Helper()
	a, err := f.st.Actions().Create(context.Background(), storage.Action{ProjectID: f.project.ID, ConversationID: conv.ID, MessageID: msgID, Kind: "git_commit",
		Target: "feat: greet", Status: status, DecidedBy: "auto:Lead (Sửa)", ProposedBy: "Lead",
		Args: storage.ActionArgs{Message: "feat: greet", Files: files, AfterDiff: true}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// ADR-189: a diff the person rejects takes the commit held for it along: a
// pending one no longer blocks the chat's later cards (nor a step waiting
// for the merge), a queued one is not run to fail with nothing to commit.
func TestRejectedDiffDropsItsHeldCommit(t *testing.T) {
	f, conv, say := deferredFixture(t)
	ctx := actor.With(context.Background(), "human:a@b.c")
	first := say("đổi lời chào")
	if len(first.Patches) != 1 || first.Patches[0].Status != "pending" {
		t.Fatalf("first diff = %+v", first.Patches)
	}
	pending := heldCommit(t, f, conv, first.ID, "pending", "hello.txt")
	queued := heldCommit(t, f, conv, first.ID, "queued", "hello.txt")
	queueRunner(f)
	if p, err := f.engine.DecidePatch(ctx, first.Patches[0].ID, false); err != nil || p.Status != "rejected" {
		t.Fatalf("reject = %+v %v", p, err)
	}
	f.engine.Drain(conv.ID)
	for _, id := range []string{pending.ID, queued.ID} {
		if a, _ := f.st.Actions().Get(ctx, id); a.Status != "rejected" || !strings.Contains(a.Detail, "Diff đi kèm") {
			t.Fatalf("held commit after its diff was rejected = %s %q", a.Status, a.Detail)
		}
	}
	if f.engine.Waiting(ctx, conv.ID) {
		t.Fatal("the chat still waits for a commit of a rejected diff")
	}
}

// ADR-189: a diff approved but failing to merge fails the commit held for
// it with the diff's reason, instead of running it to a "nothing to commit".
func TestFailedDiffFailsItsQueuedCommit(t *testing.T) {
	f, conv, say := deferredFixture(t)
	ctx := actor.With(context.Background(), "human:a@b.c")
	first := say("đổi lời chào")
	if len(first.Patches) != 1 {
		t.Fatalf("first diff = %+v", first.Patches)
	}
	// the project moved on the same line: the merge conflicts
	if err := os.WriteFile(filepath.Join(f.dir, "hello.txt"), []byte("bonjour\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, f.dir, "commit", "-qam", "bonjour")
	commit := heldCommit(t, f, conv, first.ID, "queued", "hello.txt")
	queueRunner(f)
	p, err := f.engine.DecidePatch(ctx, first.Patches[0].ID, true)
	if err != nil || p.Status != "failed" {
		t.Fatalf("merge = %+v %v", p, err)
	}
	f.engine.Drain(conv.ID)
	if a, _ := f.st.Actions().Get(ctx, commit.ID); a.Status != "failed" || !strings.Contains(a.Detail, "Diff đi kèm không gộp được") {
		t.Fatalf("held commit after its diff failed = %s %q", a.Status, a.Detail)
	}
	out, _ := exec.Command("git", "-C", f.dir, "log", "-1", "--format=%s").CombinedOutput()
	if strings.TrimSpace(string(out)) != "bonjour" {
		t.Fatalf("HEAD = %s", out)
	}
}

// ADR-189: while a turn's new diff replaces the old one, a Drain from
// elsewhere (a person's decision) must not run the commit held for the old
// diff: it waits until the new diff is saved, then the commit stays behind it.
func TestDrainWaitsWhileTheDiffIsReplaced(t *testing.T) {
	f, conv, say := deferredFixture(t)
	first := say("đổi lời chào")
	if len(first.Patches) != 1 || first.Patches[0].Status != "pending" {
		t.Fatalf("first diff = %+v", first.Patches)
	}
	commit := heldCommit(t, f, conv, first.ID, "queued", "hello.txt")
	queueRunner(f)
	drained := make(chan struct{})
	defer chat.SetBeforePatchSave(func(id string) {
		go func() { f.engine.Drain(id); close(drained) }()
		time.Sleep(150 * time.Millisecond) // a Drain that is not held off runs here
	})()
	second := say("thêm file")
	<-drained
	if len(second.Patches) != 1 || second.Patches[0].Status != "pending" {
		t.Fatalf("second diff = %+v", second.Patches)
	}
	if a, _ := f.st.Actions().Get(context.Background(), commit.ID); a.Status != "queued" || a.MessageID != second.ID {
		t.Fatalf("held commit ran ahead of its diff: %s %q (message %s)", a.Status, a.Detail, a.MessageID)
	}
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
