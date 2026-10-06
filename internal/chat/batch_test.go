package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestApplyBatchAllOrNothing(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	exec.Command("git", "-C", root, "init", "-q").Run()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("two\n"), 0o644)
	ctx := context.Background()
	da := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-one\n+ONE\n"
	db := "--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-two\n+TWO\n"
	bad := "--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-nope\n+x\n"
	read := func(f string) string { b, _ := os.ReadFile(filepath.Join(root, f)); return string(b) }

	if err := ApplyBatch(ctx, root, []string{da, bad}); err == nil {
		t.Fatal("a broken diff must fail the batch")
	}
	if read("a.txt") != "one\n" {
		t.Fatal("nothing may be applied when the batch fails")
	}
	if err := ApplyBatch(ctx, root, []string{da, db}); err != nil {
		t.Fatal(err)
	}
	if read("a.txt") != "ONE\n" || read("b.txt") != "TWO\n" {
		t.Fatal("batch not applied")
	}
	// new and deleted files in one batch
	dn := "--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1 @@\n+hello\n"
	dd := "--- a/b.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-TWO\n"
	if err := ApplyBatch(ctx, root, []string{dn, dd}); err != nil {
		t.Fatal("new/deleted files:", err)
	}
	if read("new.txt") != "hello\n" {
		t.Fatal("new file not created")
	}
	if _, err := os.Stat(filepath.Join(root, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("file not deleted")
	}
	if err := RevertBatch(ctx, root, []string{dn, dd}); err != nil {
		t.Fatal(err)
	}
	if err := RevertBatch(ctx, root, []string{da, db}); err != nil {
		t.Fatal(err)
	}
	if read("a.txt") != "one\n" || read("b.txt") != "two\n" {
		t.Fatal("batch not reverted")
	}
}
