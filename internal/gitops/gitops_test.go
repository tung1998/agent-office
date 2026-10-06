package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("dist/\n*.log\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "dist"), 0o755)
	got := Ignored(context.Background(), root, []string{"dist/", "a.log", "a.txt"})
	if !got["dist/"] || !got["a.log"] || got["a.txt"] {
		t.Fatalf("ignored = %v", got)
	}
}

func TestCommitAndStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	root := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@x.io")
	run("config", "user.name", "T")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644)
	run("add", ".")
	run("commit", "-qm", "init")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("ONE\n"), 0o644)
	os.WriteFile(filepath.Join(root, "new.txt"), []byte("n\n"), 0o644)
	os.WriteFile(filepath.Join(root, "other.txt"), []byte("o\n"), 0o644)
	ctx := context.Background()
	st, err := ReadStatus(ctx, root)
	if err != nil || st.Branch != "main" || len(st.Changes) != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	if d, _ := Diff(ctx, root, []string{"a.txt"}, 0); !strings.Contains(d, "+ONE") {
		t.Fatal(d)
	}
	hash, err := Commit(ctx, root, "feat: đổi a", []string{"a.txt", "new.txt"})
	if err != nil || hash == "" {
		t.Fatal(err)
	}
	st, _ = ReadStatus(ctx, root)
	if len(st.Changes) != 1 || st.Changes[0].Path != "other.txt" {
		t.Fatalf("only chosen files are committed: %+v", st.Changes)
	}
	if err := CreateBranch(ctx, root, "feat/x"); err != nil {
		t.Fatal(err)
	}
	if err := CreateBranch(ctx, root, "bad name"); err == nil {
		t.Fatal("invalid branch accepted")
	}
	if _, err := ReadStatus(ctx, t.TempDir()); err != ErrNotRepo {
		t.Fatalf("not a repo: %v", err)
	}

	// branches: listed with the current one, switched, a merged one deleted
	bs, err := Branches(ctx, root)
	if err != nil || len(bs) != 2 {
		t.Fatalf("branches = %+v %v", bs, err)
	}
	for _, b := range bs {
		if b.Current != (b.Name == "feat/x") || b.Subject != "feat: đổi a" || b.Date == "" {
			t.Fatalf("branch = %+v", b)
		}
	}
	if err := DeleteBranch(ctx, root, "feat/x"); err == nil {
		t.Fatal("deleted the branch checked out")
	}
	if err := SwitchBranch(ctx, root, "main"); err != nil || CurrentBranch(ctx, root) != "main" {
		t.Fatalf("switch: %v (on %s)", err, CurrentBranch(ctx, root))
	}
	if err := SwitchBranch(ctx, root, "--orphan"); err == nil {
		t.Fatal("an option taken as a branch")
	}
	if err := DeleteBranch(ctx, root, "feat/x"); err != nil {
		t.Fatal(err)
	}
	if Tracked(ctx, root, "other.txt") || !Tracked(ctx, root, "a.txt") {
		t.Fatal("tracked wrong")
	}
}

// Fetch updates what the project knows of its remote, so behind is right.
func TestFetchShowsBehind(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	base := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x.io", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x.io")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	remote, a, b := filepath.Join(base, "r.git"), filepath.Join(base, "a"), filepath.Join(base, "b")
	run(base, "init", "-q", "--bare", "-b", "main", remote)
	run(base, "clone", "-q", remote, a)
	os.WriteFile(filepath.Join(a, "x"), []byte("1"), 0o644)
	run(a, "add", ".")
	run(a, "commit", "-qm", "one")
	run(a, "push", "-q", "-u", "origin", "main")
	run(base, "clone", "-q", remote, b)
	os.WriteFile(filepath.Join(b, "x"), []byte("2"), 0o644)
	run(b, "commit", "-qam", "two")
	run(b, "push", "-q")
	ctx := context.Background()
	if st, _ := ReadStatus(ctx, a); st.Behind != 0 {
		t.Fatalf("before fetch behind = %d", st.Behind)
	}
	if err := Fetch(ctx, a); err != nil {
		t.Fatal(err)
	}
	if st, _ := ReadStatus(ctx, a); st.Behind != 1 || st.Upstream != "origin/main" {
		t.Fatalf("after fetch = %+v", st)
	}
}

// A PR from a fork: its branch is not on origin, only refs/pull/N/head.
func TestBranchDiffFromFork(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	origin, work, project := t.TempDir(), t.TempDir(), t.TempDir()
	run := func(dir string, args ...string) {
		c := exec.Command("git", append([]string{"-c", "user.email=t@x.io", "-c", "user.name=T"}, args...)...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	run(origin, "init", "-q", "--bare", "-b", "main")
	run(work, "clone", "-q", origin, ".")
	os.WriteFile(filepath.Join(work, "pay.go"), []byte("package pay\n"), 0o644)
	run(work, "add", "-A")
	run(work, "commit", "-qm", "init")
	run(work, "push", "-q", "origin", "HEAD:main")
	run(project, "clone", "-q", origin, ".")
	os.WriteFile(filepath.Join(work, "pay.go"), []byte("package pay\n\nfunc Charge() {}\n"), 0o644)
	run(work, "commit", "-qam", "charge")
	run(work, "push", "-q", "origin", "HEAD:refs/pull/7/head")
	ctx := context.Background()
	d, err := BranchDiff(ctx, project, "main", []string{"refs/pull/7/head", "fix/pay"}, 1<<20)
	if err != nil || !strings.Contains(d, "+func Charge() {}") {
		t.Fatalf("diff = %q %v", d, err)
	}
	if _, err := BranchDiff(ctx, "", "main", []string{"fix/pay"}, 1<<20); err == nil {
		t.Fatal("no folder: no error")
	}
}
