package home_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/home"
)

func TestResolve(t *testing.T) {
	t.Setenv(home.EnvHome, "")
	t.Setenv("HOME", t.TempDir())
	proj := t.TempDir()
	sub := filepath.Join(proj, "a", "b")
	os.MkdirAll(sub, 0o755)

	h, err := home.Resolve("", sub)
	if err != nil || h.Mode != home.Global {
		t.Fatalf("no local office → global: %+v, %v", h, err)
	}
	os.MkdirAll(filepath.Join(proj, ".office"), 0o700)
	os.WriteFile(filepath.Join(proj, ".office", "office.db"), nil, 0o600)
	h, _ = home.Resolve("", sub)
	realProj, _ := filepath.EvalSymlinks(proj)
	if h.Mode != home.Local || (h.ProjectRoot != proj && h.ProjectRoot != realProj) {
		t.Fatalf("nested dir → local project: %+v", h)
	}
	t.Setenv(home.EnvHome, filepath.Join(proj, "custom"))
	if h, _ := home.Resolve("", sub); h.Dir != filepath.Join(proj, "custom") {
		t.Fatalf("env override: %+v", h)
	}
	if h, _ := home.Resolve(filepath.Join(proj, "flag"), sub); h.Dir != filepath.Join(proj, "flag") {
		t.Fatalf("flag override: %+v", h)
	}
}
