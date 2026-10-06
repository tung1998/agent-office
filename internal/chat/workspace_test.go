package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspace(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.MkdirAll(filepath.Join(root, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n\nfunc Hello() string {\n\treturn \"hi\"\n}\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1\n"), 0o644)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "x.txt"), []byte("outside"), 0o644)
	os.Symlink(outside, filepath.Join(root, "link"))
	w := Workspace{Root: root}

	ls, err := w.ListDir(".")
	if err != nil || !strings.Contains(ls, "src/") || strings.Contains(ls, "node_modules") {
		t.Fatalf("ls = %q, %v", ls, err)
	}
	rf, err := w.ReadFile("src/main.go", 3, 4)
	if err != nil || rf != "3\tfunc Hello() string {\n4\t\treturn \"hi\"\n" {
		t.Fatalf("read = %q, %v", rf, err)
	}
	for _, bad := range []string{"../x", "/etc/passwd", "link/x.txt", "src/../../x"} {
		if _, err := w.ReadFile(bad, 0, 0); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	if _, err := w.ReadFile(".env", 0, 0); err != errSecret {
		t.Fatalf(".env err = %v", err)
	}
	s, err := w.Search("func hello", "*.go")
	if err != nil || !strings.Contains(s, "src/main.go:3:") {
		t.Fatalf("search = %q, %v", s, err)
	}
	if s, _ := w.Search("SECRET", ""); strings.Contains(s, ".env") {
		t.Fatal("search must skip secret files")
	}
}

func TestPatches(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	answer := "Sửa như sau:\n```diff\n--- a/a.txt\n+++ b/a.txt\n@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n```\nXong."
	ps := ExtractPatches(answer)
	if len(ps) != 1 {
		t.Fatalf("patches = %v", ps)
	}
	files, err := PatchFiles(ps[0])
	if err != nil || len(files) != 1 || files[0] != "a.txt" {
		t.Fatalf("files = %v, %v", files, err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if err := CheckPatch(context.Background(), root, ps[0]); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPatch(context.Background(), root, ps[0]); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "one\nTWO\nthree\n" {
		t.Fatalf("after apply = %q", b)
	}
	if err := ApplyPatch(context.Background(), root, ps[0]); err == nil {
		t.Fatal("applying twice must fail cleanly")
	}
	if _, err := PatchFiles("--- a/../etc/passwd\n+++ b/../etc/passwd\n"); err == nil {
		t.Fatal("path escape must be refused")
	}
	if _, err := PatchFiles("--- a/.git/config\n+++ b/.git/config\n"); err == nil {
		t.Fatal(".git must be refused")
	}
	if got := ExtractPatches("```diff\nnot a diff\n```"); len(got) != 0 {
		t.Fatalf("non-diff block extracted: %v", got)
	}
}
