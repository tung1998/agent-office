package selfupdate

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
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) string {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a.go"), "package a\n")
	write(t, filepath.Join(dir, ".gitignore"), "bin/\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "first")
	return dir
}

func fp(t *testing.T, dir string) string {
	t.Helper()
	f, err := SourceFingerprint(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSourceFingerprint(t *testing.T) {
	dir := newRepo(t)
	clean := fp(t, dir)

	write(t, filepath.Join(dir, "a.go"), "package a\n// edit\n")
	if fp(t, dir) == clean {
		t.Fatal("an uncommitted edit must change the fingerprint")
	}
	write(t, filepath.Join(dir, "a.go"), "package a\n")
	if fp(t, dir) != clean {
		t.Fatal("undoing the edit must give the same fingerprint")
	}

	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	write(t, filepath.Join(dir, "bin", "office"), "binary")
	if fp(t, dir) != clean {
		t.Fatal("ignored files must not count")
	}
	write(t, filepath.Join(dir, "new.go"), "package a\n")
	if fp(t, dir) == clean {
		t.Fatal("a new file must change the fingerprint")
	}
}

func TestChanges(t *testing.T) {
	dir := newRepo(t)
	src := Source{Root: dir}
	head := run(t, dir, "rev-parse", "HEAD")
	ctx := context.Background()

	old := Fingerprint
	defer func() { Fingerprint = old }()

	// a build by hand: compared by commit and uncommitted files
	Fingerprint = ""
	if c, err := src.Changes(ctx, head); err != nil || c.Changed || c.Uncommitted != 0 {
		t.Fatalf("clean source, same commit: %+v %v", c, err)
	}
	write(t, filepath.Join(dir, "b.go"), "package a\n")
	if c, _ := src.Changes(ctx, head); !c.Changed || c.Uncommitted != 1 {
		t.Fatalf("new file: %+v", c)
	}
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-qm", "second")
	c, _ := src.Changes(ctx, head)
	if !c.Changed || len(c.Commits) != 1 || c.Commits[0].Subject != "second" {
		t.Fatalf("new commit: %+v", c)
	}

	// a stamped build: exact
	Fingerprint = fp(t, dir)
	if c, _ := src.Changes(ctx, head); c.Changed {
		t.Fatalf("same fingerprint must not be changed: %+v", c)
	}
	write(t, filepath.Join(dir, "b.go"), "package a\n// edit\n")
	if c, _ := src.Changes(ctx, head); !c.Changed {
		t.Fatalf("edited source must be changed: %+v", c)
	}
}
