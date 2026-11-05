package deps

import (
	"context"
	"os"
	"os/exec"
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

// fakeBin puts scripts on PATH (name → shell body).
func fakeBin(t *testing.T, scripts map[string]string) {
	t.Helper()
	bin := t.TempDir()
	for name, body := range scripts {
		write(t, filepath.Join(bin, name), "#!/bin/sh\n"+body)
		os.Chmod(filepath.Join(bin, name), 0o755)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// pip in a package with no .venv (a worktree whose link was taken away):
// the virtualenv is made, then requirements are installed into it.
func TestPipMakesItsVenv(t *testing.T) {
	fakeBin(t, map[string]string{"python3": `mkdir -p .venv/bin
printf '#!/bin/sh\necho pip "$@"\n' > .venv/bin/python
chmod +x .venv/bin/python
echo venv made
`})
	root := t.TempDir()
	write(t, filepath.Join(root, "ml", "requirements.txt"), "requests\n")
	out, err := Install(context.Background(), root, Change{Dir: "ml", Manager: "pip"})
	if err != nil || !strings.Contains(out, "venv made") || !strings.Contains(out, "pip -m pip install -r requirements.txt") {
		t.Fatalf("pip = %q, %v", out, err)
	}
}

// An install's scripts never see office's secrets.
func TestInstallEnvScrubbed(t *testing.T) {
	fakeBin(t, map[string]string{"npm": `echo "key=[$ANTHROPIC_API_KEY] home=[$HOME] ci=[$CI]"`})
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("HOME", "/home/x")
	root := t.TempDir()
	out, err := Install(context.Background(), root, Change{Dir: ".", Manager: "npm"})
	if err != nil || !strings.Contains(out, "key=[] home=[/home/x] ci=[true]") {
		t.Fatalf("env = %q, %v", out, err)
	}
}

// A real pnpm (when the machine has it): a package with no dependencies
// installs offline and gets its node_modules.
func TestRealPnpmInstall(t *testing.T) {
	if _, err := exec.LookPath("pnpm"); err != nil {
		t.Skip("pnpm not installed")
	}
	root := t.TempDir()
	write(t, filepath.Join(root, "web", "package.json"), `{"name":"demo","version":"1.0.0","private":true}`)
	out, err := Install(context.Background(), root, Change{Dir: "web", Manager: "pnpm"})
	if err != nil {
		t.Fatalf("pnpm install: %v\n%s", err, out)
	}
	if fi, err := os.Stat(filepath.Join(root, "web", "node_modules")); err != nil || !fi.IsDir() {
		t.Fatalf("no node_modules after pnpm install: %v\n%s", err, out)
	}
}

func TestInstallRefusesUnknownAndOutside(t *testing.T) {
	root := t.TempDir()
	if _, err := Install(context.Background(), root, Change{Dir: "../x", Manager: "npm"}); err == nil || !strings.Contains(err.Error(), "ngoài project") {
		t.Fatalf("outside = %v", err)
	}
	if _, err := Install(context.Background(), root, Change{Dir: ".", Manager: "cargo"}); err != ErrNoManager {
		t.Fatalf("unknown = %v", err)
	}
}
