package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), identity...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupRepo(t *testing.T) string {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	write(t, filepath.Join(repo, ".gitignore"), "node_modules/\n.env\n")
	write(t, filepath.Join(repo, "a.txt"), "one\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "init")
	// the person's uncommitted work, a dependency folder and a secret
	write(t, filepath.Join(repo, "a.txt"), "one\ntwo\n")
	write(t, filepath.Join(repo, "new.txt"), "draft\n")
	write(t, filepath.Join(repo, "web", "node_modules", "pkg", "index.js"), "x")
	write(t, filepath.Join(repo, ".env"), "KEY=1\n")
	return repo
}

func TestWorktreeLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := setupRepo(t)
	statusBefore := run(t, repo, "status", "--porcelain")
	m := New(filepath.Join(t.TempDir(), "worktrees"))
	dir, err := m.Ensure(ctx, repo, "p1", "chat-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	// starts from what the person sees, without touching their repo
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\ntwo\n" {
		t.Fatalf("a.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Fatal("untracked file missing in worktree")
	}
	if l, err := os.Readlink(filepath.Join(dir, "web", "node_modules")); err != nil || l != filepath.Join(repo, "web", "node_modules") {
		t.Fatalf("node_modules link = %q, %v", l, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); string(b) != "KEY=1\n" {
		t.Fatal(".env not copied")
	}
	if got := run(t, repo, "status", "--porcelain"); got != statusBefore {
		t.Fatalf("repo status changed:\n%s\nwas\n%s", got, statusBefore)
	}
	if again, err := m.Ensure(ctx, repo, "p1", "chat-1", nil); err != nil || again != dir {
		t.Fatalf("Ensure again = %q, %v", again, err)
	}

	// nothing changed yet; links and copied secrets are not changes
	if diff, files, err := Changes(ctx, dir, nil); err != nil || diff != "" || files != nil {
		t.Fatalf("clean changes = %q %v %v", diff, files, err)
	}

	// the agent edits and adds files
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\nthree\n")
	write(t, filepath.Join(dir, "b.txt"), "bee\n")
	write(t, filepath.Join(dir, "out.txt"), "outside\n")
	diff, files, err := Changes(ctx, dir, []string{"a.txt", "b.txt"})
	if err != nil || strings.Join(files, ",") != "a.txt,b.txt" || !strings.Contains(diff, "+three") {
		t.Fatalf("changes = %v %v\n%s", files, err, diff)
	}
	if err := Restore(ctx, dir, []string{"out.txt"}); err != nil {
		t.Fatal(err)
	}
	if all, _ := Changed(ctx, dir); strings.Join(all, ",") != "a.txt,b.txt" {
		t.Fatalf("after restore = %v", all)
	}

	// merging into the project, then accepting: nothing pending any more
	cmd := exec.Command("git", "apply", "--binary", "-")
	cmd.Dir, cmd.Stdin = repo, strings.NewReader(diff)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("apply to repo: %v %s", err, out)
	}
	if err := Accept(ctx, dir, diff); err != nil {
		t.Fatal(err)
	}
	if all, _ := Changed(ctx, dir); len(all) != 0 {
		t.Fatalf("pending after accept = %v", all)
	}

	// a rejected change is taken back out
	write(t, filepath.Join(dir, "b.txt"), "bee\nwrong\n")
	d2, _, _ := Changes(ctx, dir, nil)
	if err := Discard(ctx, dir, d2); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); string(b) != "bee\n" {
		t.Fatalf("b.txt after discard = %q", b)
	}

	if err := m.Remove(ctx, repo, "p1", "chat-1"); err != nil {
		t.Fatal(err)
	}
	if m.Exists("p1", "chat-1") || strings.Contains(run(t, repo, "worktree", "list"), dir) {
		t.Fatal("worktree still there")
	}
}

func TestNotGit(t *testing.T) {
	if _, err := New(t.TempDir()).Ensure(context.Background(), t.TempDir(), "p", "x", nil); err != ErrNotGit {
		t.Fatalf("err = %v", err)
	}
	// a folder inside another repo (the assistant's, under an ignored .office)
	repo := setupRepo(t)
	write(t, filepath.Join(repo, ".gitignore"), "node_modules/\n.env\n.office/\n")
	inner := filepath.Join(repo, ".office", "assistant")
	os.MkdirAll(inner, 0o755)
	if _, err := New(t.TempDir()).Ensure(context.Background(), inner, "p", "x", nil); err != ErrNotGit {
		t.Fatalf("inside a repo: err = %v", err)
	}
}

// The project moved on after the worktree was made: Refresh puts what the
// agent changed on top of the project as it is now (a 3-way merge in the
// worktree); the project itself is never touched.
func TestRefreshOntoMovedProject(t *testing.T) {
	ctx := context.Background()
	repo := setupRepo(t)
	write(t, filepath.Join(repo, "b.txt"), "1\n2\n3\n4\n5\n")
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "b")
	m := New(filepath.Join(t.TempDir(), "worktrees"))
	dir, err := m.Ensure(ctx, repo, "p1", "chat-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "b.txt"), "1\n2\n3\n4\nFIVE\n") // the agent, at the end
	write(t, filepath.Join(repo, "b.txt"), "ONE\n2\n3\n4\n5\n") // the project moved, at the start
	run(t, repo, "commit", "-q", "-am", "moved")
	conflicts, err := Refresh(ctx, repo, dir)
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("refresh = %v %v", conflicts, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); string(b) != "ONE\n2\n3\n4\nFIVE\n" {
		t.Fatalf("worktree after refresh = %q", b)
	}
	diff, files, err := Changes(ctx, dir, nil)
	if err != nil || len(files) != 1 || files[0] != "b.txt" {
		t.Fatalf("changes = %v %v", files, err)
	}
	apply := exec.Command("git", "apply", "--check", "-")
	apply.Dir, apply.Stdin = repo, strings.NewReader(diff)
	if out, err := apply.CombinedOutput(); err != nil {
		t.Fatalf("the refreshed diff does not apply: %s", out)
	}
	// the same line on both sides: a conflict, kept in the worktree
	write(t, filepath.Join(dir, "b.txt"), "ONE\n2\nthree-agent\n4\nFIVE\n")
	write(t, filepath.Join(repo, "b.txt"), "ONE\n2\nthree-project\n4\n5\n")
	run(t, repo, "commit", "-q", "-am", "again")
	conflicts, err = Refresh(ctx, repo, dir)
	if err != nil || len(conflicts) != 1 || conflicts[0] != "b.txt" {
		t.Fatalf("conflict refresh = %v %v", conflicts, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); !strings.Contains(string(b), "<<<<<<<") {
		t.Fatalf("no conflict markers: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "b.txt")); string(b) != "ONE\n2\nthree-project\n4\n5\n" {
		t.Fatalf("the project was touched: %q", b)
	}
}

// A Node project not installed yet: its node_modules is made (empty) in the
// project and linked, so what the agent installs in its worktree is the
// project's too (the project's dev server finds it); the link stays out of diffs.
func TestLinkNodeModulesNotInstalled(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	repo := t.TempDir()
	run(t, repo, "init", "-q")
	write(t, filepath.Join(repo, ".gitignore"), "node_modules\n")
	write(t, filepath.Join(repo, "package.json"), `{"name":"x"}`)
	write(t, filepath.Join(repo, "apps", "web", "package.json"), `{"name":"web"}`)
	run(t, repo, "add", "-A")
	run(t, repo, "commit", "-q", "-m", "init")
	m := New(filepath.Join(t.TempDir(), "worktrees"))
	dir, err := m.Ensure(ctx, repo, "p1", "chat-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "node_modules")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("node_modules is not a link: %v %v", fi, err)
	}
	write(t, filepath.Join(dir, "node_modules", "left-pad", "index.js"), "x") // pnpm install in the worktree
	if _, err := os.Stat(filepath.Join(repo, "node_modules", "left-pad", "index.js")); err != nil {
		t.Fatal("what was installed in the worktree is not the project's")
	}
	if _, err := os.Lstat(filepath.Join(dir, "apps", "web", "node_modules")); err != nil {
		t.Fatal("a workspace package's node_modules is not linked")
	}
	if diff, files, err := Changes(ctx, dir, nil); err != nil || diff != "" || len(files) != 0 {
		t.Fatalf("the links are in the diff: %v %q", files, diff)
	}
}
