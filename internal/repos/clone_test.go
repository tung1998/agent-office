package repos

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneURL(t *testing.T) {
	ok := []string{
		"https://github.com/acme/shop.git",
		"https://github.com/acme/shop",
		"git@github.com:acme/shop.git",
		"ssh://git@bitbucket.org/acme/shop.git",
		"  https://gitlab.com/a/b  ",
	}
	for _, u := range ok {
		if _, err := CloneURL(u); err != nil {
			t.Errorf("CloneURL(%q) = %v, want ok", u, err)
		}
	}
	bad := []string{"", "/tmp/repo", "file:///tmp/repo", "ext::sh -c touch% /tmp/x", "--upload-pack=touch /tmp/x", "https://x y", "../repo"}
	for _, u := range bad {
		if _, err := CloneURL(u); !errors.Is(err, ErrCloneURL) {
			t.Errorf("CloneURL(%q) = %v, want ErrCloneURL", u, err)
		}
	}
}

func TestCloneDirName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/shop.git":  "shop",
		"https://github.com/acme/shop/":     "shop",
		"git@github.com:acme/admin-v2.git":  "admin-v2",
		"git@github.com:shop.git":           "shop",
		"ssh://git@host:22/team/api-server": "api-server",
	}
	for in, want := range cases {
		if got := CloneDirName(in); got != want {
			t.Errorf("CloneDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripCredentials(t *testing.T) {
	got := StripCredentials("fatal: could not read from https://user:ghp_secret@github.com/a/b.git")
	if strings.Contains(got, "ghp_secret") || !strings.Contains(got, "https://github.com/a/b.git") {
		t.Fatalf("StripCredentials = %q", got)
	}
}

func TestCloneInto(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	src := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	parent := t.TempDir()
	dest, err := cloneInto(context.Background(), src, parent, "shop")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil || filepath.Base(dest) != "shop" {
		t.Fatalf("dest %q: %v", dest, err)
	}
	if _, err := cloneInto(context.Background(), src, parent, "shop"); !errors.Is(err, ErrCloneExists) {
		t.Fatalf("again = %v, want ErrCloneExists", err)
	}
	if _, err := cloneInto(context.Background(), src, parent, "../x"); !errors.Is(err, ErrCloneDir) {
		t.Fatalf("bad name = %v, want ErrCloneDir", err)
	}
	// a failed clone leaves no folder behind
	var ce *CloneError
	if _, err := cloneInto(context.Background(), filepath.Join(src, "missing"), parent, "gone"); !errors.As(err, &ce) {
		t.Fatalf("missing = %v, want CloneError", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "gone")); !os.IsNotExist(err) {
		t.Fatalf("failed clone left %v", err)
	}
}
