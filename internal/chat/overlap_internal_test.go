package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/worktree"
)

// ADR-277: a turn hears which files the project's other chats are changing,
// those it changed too first; its own worktrees and other projects' are left out.
func TestOverlapsListsOtherChats(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}} {
		gitIn(t, repo, args...)
	}
	os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "b.go"), []byte("package a\n"), 0o644)
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init")

	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: repo})
	mine, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "mine"})
	other, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "sửa giao diện", CreatedBy: "b@x.y"})
	idle, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "idle"})

	trees := worktree.New(filepath.Join(t.TempDir(), "wt"))
	e := &Engine{store: st, trees: trees}
	dir := func(name string) string {
		d, err := trees.Ensure(ctx, repo, p.ID, name, nil)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	me := dir(ChatTree(mine.ID))
	if got := e.overlaps(ctx, p.ID, mine.ID, me); got != "" {
		t.Fatalf("nothing changed elsewhere, got %q", got)
	}
	os.WriteFile(filepath.Join(me, "a.go"), []byte("package a // mine\n"), 0o644)
	os.WriteFile(filepath.Join(dir(ChatAgentTree(mine.ID, "agt_2")), "b.go"), []byte("x"), 0o644) // own chat, other agent
	od := dir(ChatTree(other.ID))
	os.WriteFile(filepath.Join(od, "a.go"), []byte("package a // theirs\n"), 0o644)
	os.WriteFile(filepath.Join(od, "c.go"), []byte("package a\n"), 0o644)
	dir(ChatTree(idle.ID)) // no edits
	dir(TaskTree("t1"))    // not a chat

	got := e.overlaps(ctx, p.ID, mine.ID, me)
	if !strings.Contains(got, `"sửa giao diện"`) || !strings.Contains(got, "c="+other.ID) || !strings.Contains(got, "by b@x.y") {
		t.Fatalf("other chat not described: %q", got)
	}
	if !strings.Contains(got, "a.go, c.go") || !strings.Contains(got, "⚠ you changed these too: a.go") {
		t.Fatalf("files or shared mark wrong: %q", got)
	}
	if strings.Contains(got, "idle") || strings.Contains(got, mine.ID) || strings.Count(got, "\n- ") != 0 {
		t.Fatalf("own, idle or extra chats listed: %q", got)
	}

	// a turn: the chat on a shared file in full, the rest in one line
	third, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "docs"})
	os.WriteFile(filepath.Join(dir(ChatTree(third.ID)), "b.go"), []byte("package a // docs\n"), 0o644)
	shared, files := e.overlapBrief(ctx, p.ID, mine.ID, me)
	if !strings.Contains(shared, `"sửa giao diện"`) || strings.Contains(shared, "docs") {
		t.Fatalf("shared = %q", shared)
	}
	if files != "b.go" {
		t.Fatalf("other files = %q", files)
	}
	// nothing changed on my side: no chat in full, only the files
	os.Remove(filepath.Join(me, "a.go"))
	gitIn(t, me, "checkout", "--", "a.go")
	shared, files = e.overlapBrief(ctx, p.ID, mine.ID, me)
	if shared != "" || !strings.Contains(files, "a.go") || !strings.Contains(files, "c.go") || !strings.Contains(files, "b.go") {
		t.Fatalf("no shared edits: shared=%q files=%q", shared, files)
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestSiblingOf(t *testing.T) {
	for in, want := range map[string]string{"chat-c1": "c1", "chat-c1--agt": "c1", "task-1": "", "burn-x": ""} {
		if got := siblingOf(in); got != want {
			t.Errorf("siblingOf(%q) = %q, want %q", in, got, want)
		}
	}
}
