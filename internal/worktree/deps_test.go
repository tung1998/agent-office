package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

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
