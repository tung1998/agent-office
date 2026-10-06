package repos_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/repos"
)

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"storefront","description":"Shop"}`), 0o644)
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[core]\n\tbare = false\n[remote \"origin\"]\n\turl = https://user:token@bitbucket.org/acme/storefront.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"), 0o644)
	info, err := repos.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "storefront" || info.Description != "Shop" || info.GitRemote != "https://bitbucket.org/acme/storefront.git" || !filepath.IsAbs(info.Path) {
		t.Fatalf("info = %+v", info)
	}
	goDir := t.TempDir()
	os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module github.com/acme/api\n\ngo 1.24\n"), 0o644)
	if info, _ := repos.Detect(goDir); info.Name != "api" {
		t.Fatalf("go name = %q", info.Name)
	}
	if _, err := repos.Detect(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing dir must fail")
	}
}
