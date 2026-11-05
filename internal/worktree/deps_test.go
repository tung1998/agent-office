package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The copy Keep sets aside sits next to the worktrees: Sweep leaves one an
// install still holds (it is no worktree; deleting it loses the libraries a
// failed install gives back) and deletes one left by an office that died
// mid-install, once older than any install.
func TestStaleKeepDirsSwept(t *testing.T) {
	m := New(t.TempDir())
	tree := m.Path("p1", "chat-x")
	os.MkdirAll(filepath.Join(tree, "web", "node_modules", "mine"), 0o755)
	k, err := Keep(tree, "web")
	if err != nil || k.Saved() != 1 {
		t.Fatalf("keep: %v %d", err, k.Saved())
	}
	stale, _ := os.MkdirTemp(filepath.Dir(tree), ".deps-keep-*")
	old := time.Now().Add(-2 * KeepMaxAge)
	os.Chtimes(stale, old, old)
	m.Sweep(context.Background(), "", "p1", func(name string) (bool, bool) { return name == "chat-x", false }, time.Hour)
	if _, err := os.Stat(k.tmp); err != nil {
		t.Fatal("Sweep deleted the copy an install still holds")
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("a stale copy was left")
	}
	if err := k.Restore(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tree, "web", "node_modules", "mine")); err != nil {
		t.Fatal("not restored")
	}
}

// Restore never deletes what the install left when its saved copy is gone:
// the worktree keeps something rather than nothing.
func TestRestoreWithoutCopyKeepsFolder(t *testing.T) {
	tree := filepath.Join(t.TempDir(), "wt")
	os.MkdirAll(filepath.Join(tree, "web", "node_modules", "mine"), 0o755)
	k, err := Keep(tree, "web")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(tree, "web", "node_modules", "half"), 0o755)
	os.RemoveAll(k.tmp)
	if err := k.Restore(); err == nil {
		t.Fatal("a lost copy restored with no error")
	}
	if _, err := os.Stat(filepath.Join(tree, "web", "node_modules", "half")); err != nil {
		t.Fatal("Restore deleted the folder with nothing to put back")
	}
}

// An install in the worktree (after Detach) never touches the project's
// node_modules, its workspace packages' included; the folders stay out of
// the diff; Relink puts the links back once the project has them.
func TestPrivateDepsLeaveProjectUntouched(t *testing.T) {
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
	write(t, filepath.Join(repo, "node_modules", "nuxt", "v"), "4.5.2")
	write(t, filepath.Join(repo, "apps", "web", "node_modules", "vue", "v"), "3.5")
	m := New(filepath.Join(t.TempDir(), "worktrees"))
	dir, err := m.Ensure(ctx, repo, "p1", "chat-1", nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Detach(dir, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"apps/web/node_modules", "node_modules"}) {
		t.Fatalf("detached = %v", got)
	}
	// what a pnpm install in the worktree writes
	write(t, filepath.Join(dir, "node_modules", "nuxt", "v"), "4.6.4")
	write(t, filepath.Join(dir, "apps", "web", "node_modules", "vue", "v"), "3.6")
	if b, _ := os.ReadFile(filepath.Join(repo, "node_modules", "nuxt", "v")); string(b) != "4.5.2" {
		t.Fatalf("the project's node_modules changed: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "apps", "web", "node_modules", "vue", "v")); string(b) != "3.5" {
		t.Fatalf("a workspace package's node_modules changed: %q", b)
	}
	if diff, files, err := Changes(ctx, dir, nil); err != nil || diff != "" || len(files) != 0 {
		t.Fatalf("the worktree's own node_modules is in the diff: %v %q", files, diff)
	}
	if again, _ := Detach(dir, "."); len(again) != 0 {
		t.Fatalf("detached twice: %v", again)
	}

	// the project installed the same: the worktree links back
	back, err := Relink(repo, dir, ".")
	if err != nil || len(back) != 2 {
		t.Fatalf("relinked = %v, %v", back, err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "node_modules")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("node_modules not linked back: %v", err)
	}
	if _, err := Detach(dir, "../x"); err == nil {
		t.Fatal("a folder outside the worktree was walked")
	}
}
