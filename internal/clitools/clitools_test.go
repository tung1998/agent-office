package clitools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClaude behaves like `claude` for --version, auth status and auth login.
const fakeClaude = `#!/bin/sh
STATE="$(dirname "$0")/.logged-in"
case "$1 $2" in
  "--version "*) echo "9.9.9 (Claude Code)";;
  "auth status")
    if [ -f "$STATE" ]; then echo '{"loggedIn":true,"authMethod":"claude.ai","email":"a@b.c","orgName":"Acme"}'
    else echo '{"loggedIn":false}'; exit 1; fi;;
  "auth login")
    echo "Opening browser to sign in…"
    echo "If the browser did not open, visit: https://claude.ai/oauth/authorize?code=true&state=xyz"
    printf "Paste code here if prompted > "
    read CODE
    if [ "$CODE" = "good-code" ]; then touch "$STATE"; echo "Login successful."; else echo "Invalid code"; exit 1; fi;;
esac
`

func testManager(t *testing.T) (*Manager, string) {
	dir := t.TempDir()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	env = append(env, "PATH="+dir+":/usr/bin:/bin")
	m := &Manager{jobs: map[string]*Job{}, last: map[string]*Job{}, env: env, tools: tools}
	return m, dir
}

func writeBin(t *testing.T, dir, name, script string) {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitState(t *testing.T, j *Job, want string) JobView {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if v := j.Snapshot(); v.State == want {
			return v
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job state = %s, want %s; output:\n%s", j.Snapshot().State, want, j.Snapshot().Output)
	return JobView{}
}

func TestStatusAndLogin(t *testing.T) {
	m, dir := testManager(t)
	ctx := context.Background()

	s, _ := m.Status(ctx, "claude")
	if s.Installed {
		t.Fatal("claude should not be installed yet")
	}
	if _, err := m.Login("claude"); err != ErrNotInstalled {
		t.Fatalf("login before install err = %v", err)
	}

	writeBin(t, dir, "claude", fakeClaude)
	s, _ = m.Status(ctx, "claude")
	if !s.Installed || s.Version != "9.9.9 (Claude Code)" || s.Auth.LoggedIn {
		t.Fatalf("status = %+v", s)
	}

	j, err := m.Login("claude")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Login("claude"); err != ErrBusy {
		t.Fatalf("second login err = %v", err)
	}
	// wait for the URL to appear, then paste the code
	deadline := time.Now().Add(5 * time.Second)
	for len(j.Snapshot().URLs) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	v := j.Snapshot()
	if len(v.URLs) != 1 || !strings.HasPrefix(v.URLs[0], "https://claude.ai/oauth/authorize") {
		t.Fatalf("urls = %v, output = %q", v.URLs, v.Output)
	}
	if err := j.Input("good-code"); err != nil {
		t.Fatal(err)
	}
	v = waitState(t, j, "succeeded")
	if strings.Contains(v.Output, "\x1b") || !strings.Contains(v.Output, "Login successful.") {
		t.Fatalf("output = %q", v.Output)
	}
	s, _ = m.Status(ctx, "claude")
	if !s.Auth.LoggedIn || s.Auth.Account != "a@b.c · Acme" || s.Job == nil || s.Job.State != "succeeded" {
		t.Fatalf("after login = %+v", s)
	}
}

func TestInstallAndCancel(t *testing.T) {
	m, dir := testManager(t)
	// a fake "brew" that installs a fake codex
	writeBin(t, dir, "brew", "#!/bin/sh\necho \"==> Installing $3\"\nprintf '#!/bin/sh\\necho codex-cli 1.0.0\\n' > \""+dir+"/codex\"\nchmod +x \""+dir+"/codex\"\necho done\n")
	if _, err := m.Install("codex", "npm"); err != ErrUnknownMethod {
		t.Fatalf("npm missing err = %v", err)
	}
	if _, err := m.Install("codex", "rm -rf /"); err != ErrUnknownMethod {
		t.Fatalf("arbitrary method err = %v", err)
	}
	j, err := m.Install("codex", "brew")
	if err != nil {
		t.Fatal(err)
	}
	v := waitState(t, j, "succeeded")
	if v.Command != "brew install --cask codex" && !strings.HasSuffix(v.Command, "brew install --cask codex") {
		t.Fatalf("command = %q", v.Command)
	}
	s, _ := m.Status(context.Background(), "codex")
	if !s.Installed || s.Version != "codex-cli 1.0.0" {
		t.Fatalf("codex status = %+v", s)
	}

	writeBin(t, dir, "claude", "#!/bin/sh\nif [ \"$1\" = auth ] && [ \"$2\" = login ]; then sleep 30; fi\necho '{}'\n")
	lj, err := m.Login("claude")
	if err != nil {
		t.Fatal(err)
	}
	lj.Cancel()
	waitState(t, lj, "cancelled")
}

func TestCleanOutput(t *testing.T) {
	got := cleanOutput("\x1b[1mHello\x1b[0m\r\n\n\n\nWorld  \n")
	if got != "Hello\n\nWorld" {
		t.Fatalf("clean = %q", got)
	}
}

func TestCodexStatusTrustsCLI(t *testing.T) {
	m, dir := testManager(t)
	writeBin(t, dir, "codex", "#!/bin/sh\ncase \"$1 $2\" in \"--version \"*) echo codex-cli 1;; \"login status\") echo 'Not logged in'; exit 1;; esac\n")
	s, _ := m.Status(context.Background(), "codex")
	if s.Auth.LoggedIn {
		t.Fatalf("explicit 'Not logged in' must win over any credentials file: %+v", s.Auth)
	}
	writeBin(t, dir, "codex", "#!/bin/sh\ncase \"$1 $2\" in \"--version \"*) echo codex-cli 1;; \"login status\") echo 'Logged in using ChatGPT';; esac\n")
	if s, _ := m.Status(context.Background(), "codex"); !s.Auth.LoggedIn {
		t.Fatalf("logged in: %+v", s.Auth)
	}
}
