package deps

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDepChangesNodeAndPython(t *testing.T) {
	// files alone: the Nuxt upgrade of the dashboard
	got := Changes("", []string{"dashboard/package.json", "dashboard/pnpm-lock.yaml", "internal/x.go"})
	if want := []Change{{Dir: "dashboard", Manager: "pnpm"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dashboard = %v, want %v", got, want)
	}
	if got := Changes("", []string{"README.md", "go.mod", "web/node_modules/x/package.json", "src/app.ts"}); len(got) != 0 {
		t.Fatalf("no dependency change = %v", got)
	}

	// a monorepo: a workspace package's package.json installs from the lockfile's folder
	root := t.TempDir()
	write(t, filepath.Join(root, "pnpm-lock.yaml"), "")
	write(t, filepath.Join(root, "package.json"), "{}")
	write(t, filepath.Join(root, "apps", "web", "package.json"), "{}")
	write(t, filepath.Join(root, "svc", "package-lock.json"), "{}")
	write(t, filepath.Join(root, "svc", "package.json"), "{}")
	write(t, filepath.Join(root, "py", "uv.lock"), "")
	write(t, filepath.Join(root, "py", "pyproject.toml"), "")
	write(t, filepath.Join(root, "tool", "pyproject.toml"), "") // no uv.lock: any tool's settings
	got = Changes(root, []string{"apps/web/package.json", "svc/package.json", "py/pyproject.toml", "tool/pyproject.toml", "ml/requirements.txt"})
	want := []Change{{Dir: ".", Manager: "pnpm"}, {Dir: "svc", Manager: "npm"}, {Dir: "py", Manager: "uv"}, {Dir: "ml", Manager: "pip"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("monorepo = %v, want %v", got, want)
	}
	if l := (Change{Dir: "dashboard", Manager: "pnpm"}).Label(); l != "dashboard (pnpm)" {
		t.Fatalf("label = %q", l)
	}
}

func TestInstallRefusesUnknownAndOutside(t *testing.T) {
	root := t.TempDir()
	if _, err := Install(context.Background(), root, Change{Dir: "../x", Manager: "npm"}); err == nil || !strings.Contains(err.Error(), "ngoài project") {
		t.Fatalf("outside = %v", err)
	}
	if _, err := Install(context.Background(), root, Change{Dir: ".", Manager: "pip"}); err == nil || !strings.Contains(err.Error(), ".venv") {
		t.Fatalf("pip without .venv = %v", err)
	}
	if _, err := Install(context.Background(), root, Change{Dir: ".", Manager: "cargo"}); err != ErrNoManager {
		t.Fatalf("unknown = %v", err)
	}
}
