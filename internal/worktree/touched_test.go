package worktree

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// Touched sees edits and new files of a worktree without staging them.
func TestNamesAndTouched(t *testing.T) {
	ctx := context.Background()
	repo := setupRepo(t)
	m := New(filepath.Join(t.TempDir(), "worktrees"))
	dir, err := m.Ensure(ctx, repo, "p1", "chat-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Ensure(ctx, repo, "p1", "chat-2", nil); err != nil {
		t.Fatal(err)
	}
	names := m.Names("p1")
	slices.Sort(names)
	if !slices.Equal(names, []string{"chat-1", "chat-2"}) {
		t.Fatalf("names = %v", names)
	}
	if files, err := Touched(ctx, dir); err != nil || len(files) != 0 {
		t.Fatalf("fresh worktree touched = %v, %v", files, err)
	}
	write(t, filepath.Join(dir, "a.txt"), "changed\n")
	write(t, filepath.Join(dir, "sub", "b.go"), "package sub\n")
	files, err := Touched(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	if !slices.Equal(files, []string{"a.txt", "sub/b.go"}) {
		t.Fatalf("touched = %v", files)
	}
	if staged := run(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("Touched staged files: %q", staged)
	}
	if got := m.Names("nope"); got != nil {
		t.Fatalf("unknown project names = %v", got)
	}
}
