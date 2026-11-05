package chat

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A shell install in a worktree whose node_modules/.venv is the project's
// (a link) would write into the project's libraries: refused, pointing to
// deps_install. Other commands, and installs where the folder is the
// worktree's own or in the project, go through.
func TestInstallInLinkedTreeRefused(t *testing.T) {
	linked := map[string]bool{"/w|node_modules": true, "/w/web|node_modules": true, "/w|.venv": true, "/w/ml|.venv": true}
	isLinked := func(dir, folder string) bool { return linked[dir+"|"+folder] }
	refused := []string{
		"pnpm install",
		"pnpm i --frozen-lockfile",
		"cd web && pnpm install",
		"pnpm -C web install",
		"pnpm --dir web add nuxt@4.6.4",
		"npm ci",
		"npm --prefix web install",
		"CI=1 npm install --no-audit",
		"yarn",
		"yarn add vue",
		"bun install",
		"git status && npm i",
		"uv sync",
		"uv add httpx",
		"uv pip install -e .",
		"pip install -e .",
		"cd ml; .venv/bin/pip install -r requirements.txt",
		"python3 -m pip install requests",
		"ml/.venv/bin/python -m pip install x",
	}
	for _, c := range refused {
		if why := installInLinkedTree(c, "/w", isLinked); why == "" {
			t.Errorf("%q: not refused", c)
		}
	}
	allowed := []string{
		"pnpm test",
		"pnpm run build",
		"npm run lint",
		"pnpm --dir web typecheck",
		"go test ./...",
		"uv run pytest",
		"pip list",
		"pnpm -C other install", // other's node_modules is its own
		"echo pnpm install",
		"git log --grep 'npm install'",
	}
	for _, c := range allowed {
		if why := installInLinkedTree(c, "/w", isLinked); why != "" {
			t.Errorf("%q: refused: %s", c, why)
		}
	}
	// in the project (or a worktree with libraries of its own): allowed
	if why := installInLinkedTree("pnpm install", "/p", isLinked); why != "" {
		t.Fatalf("project install refused: %s", why)
	}
	if why := installInLinkedTree("pnpm install", "/w", isLinked); !strings.Contains(why, "deps_install") {
		t.Fatalf("the refusal does not point to deps_install: %q", why)
	}
}

// pnpm's -w (--workspace-root) takes no value: the verb after it is still
// read. npm's -w <workspace> does take one.
func TestPnpmWorkspaceRootInstallRefused(t *testing.T) {
	isLinked := func(dir, folder string) bool { return dir == "/w" }
	for _, c := range []string{"pnpm -w add lodash", "pnpm --workspace-root add x", "yarn -W add x", "npm -w web install x", "npm --workspace web i"} {
		if why := installInLinkedTree(c, "/w", isLinked); why == "" {
			t.Errorf("%q: not refused", c)
		}
	}
	// npm -w's value is not a verb: "npm -w install run build" runs a script
	if why := installInLinkedTree("npm -w install run build", "/w", isLinked); why != "" {
		t.Errorf("npm -w <ws> run: refused: %s", why)
	}
}

// A global install writes the tool's own folder, never the worktree's
// node_modules: allowed.
func TestGlobalInstallAllowed(t *testing.T) {
	isLinked := func(dir, folder string) bool { return dir == "/w" }
	for _, c := range []string{"npm i -g typescript", "npm install --global pnpm", "npm install --location=global x", "pnpm add -g x", "pnpm add --global x", "bun add -g x", "yarn global add x"} {
		if why := installInLinkedTree(c, "/w", isLinked); why != "" {
			t.Errorf("%q: refused: %s", c, why)
		}
	}
}

// An install behind a shell, corepack, npx or env is still an install.
func TestWrappedInstallRefused(t *testing.T) {
	linked := map[string]bool{"/w|node_modules": true, "/w/web|node_modules": true, "/w|.venv": true}
	isLinked := func(dir, folder string) bool { return linked[dir+"|"+folder] }
	refused := []string{
		`bash -c "pnpm i"`,
		`sh -c 'npm install'`,
		`bash -lc "cd web && pnpm add x"`,
		`zsh -c "pip install x"`,
		"corepack pnpm install",
		"corepack yarn add x",
		"npx pnpm add x",
		"npx -y pnpm@9 install",
		"pnpx pnpm i",
		"bunx npm ci",
		"env -i pnpm install",
		"env X=1 pnpm i",
		"nohup pnpm install",
		"time npm ci",
	}
	for _, c := range refused {
		if why := installInLinkedTree(c, "/w", isLinked); why == "" {
			t.Errorf("%q: not refused", c)
		}
	}
	allowed := []string{
		`bash -c "pnpm test"`,
		"npx vitest run",
		"corepack enable",
		"npx nuxi typecheck",
		`sh -c 'echo pnpm install'`,
	}
	for _, c := range allowed {
		if why := installInLinkedTree(c, "/w", isLinked); why != "" {
			t.Errorf("%q: refused: %s", c, why)
		}
	}
}

// The hook itself, on a real worktree: node_modules linked from the project
// refuses a pnpm install, the project's own goes through.
func TestForegroundDeniesInstallInLinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	repo := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "package.json"), []byte("{}"), 0o644)
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "init")
	os.MkdirAll(filepath.Join(repo, "node_modules"), 0o755)
	tree := filepath.Join(t.TempDir(), "wt")
	run(repo, "worktree", "add", "-q", tree)
	if err := os.Symlink(filepath.Join(repo, "node_modules"), filepath.Join(tree, "node_modules")); err != nil {
		t.Fatal(err)
	}
	hook := func(cwd, cmd string) string {
		var out bytes.Buffer
		Foreground(strings.NewReader(`{"cwd":`+quote(cwd)+`,"tool_name":"Bash","tool_input":{"command":`+quote(cmd)+`}}`), &out)
		return out.String()
	}
	if out := hook(tree, "pnpm install"); !strings.Contains(out, "deny") || !strings.Contains(out, "deps_install") {
		t.Fatalf("worktree install = %q", out)
	}
	if out := hook(tree, "pnpm test"); out != "" {
		t.Fatalf("worktree test = %q", out)
	}
	if out := hook(repo, "pnpm install"); out != "" {
		t.Fatalf("project install = %q", out)
	}
	// the worktree's own node_modules (deps_install made it): allowed
	os.Remove(filepath.Join(tree, "node_modules"))
	os.MkdirAll(filepath.Join(tree, "node_modules"), 0o755)
	if out := hook(tree, "pnpm install"); out != "" {
		t.Fatalf("own node_modules install = %q", out)
	}
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
