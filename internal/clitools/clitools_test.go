package clitools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/llm"
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
	m := &Manager{jobs: map[string]*Job{}, last: map[string]*Job{}, env: env, tools: tools, noFetch: true}
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
	if !strings.Contains(v.Command, "brew install --cask codex") {
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

func TestCodexStatusTrustsCLI(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
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

// fakeGemini acts like `gemini` signing in with NO_BROWSER: a link and a code
// prompt, a fresh link after a wrong code, then its chat (which never ends).
const fakeGemini = `#!/bin/sh
[ "$1" = "--version" ] && { echo "0.63.0"; exit 0; }
[ "$NO_BROWSER" = "true" ] || { echo "no NO_BROWSER"; exit 1; }
n=1
while :; do
  echo "Please visit the following URL to authorize the application:"
  echo "https://accounts.google.com/o/oauth2/v2/auth?state=s$n&client_id=x"
  printf "Enter the authorization code: "
  read CODE
  if [ "$CODE" = "good-code" ]; then
    mkdir -p "$GEMINI_CLI_HOME/.gemini"
    echo '{}' > "$GEMINI_CLI_HOME/.gemini/oauth_creds.json"
    echo '{"active":"me@gmail.com","old":[]}' > "$GEMINI_CLI_HOME/.gemini/google_accounts.json"
    echo "Gemini CLI ready"
    sleep 600
  fi
  echo "Failed to authenticate with authorization code:invalid_grant"
  n=$((n+1))
done
`

func TestGeminiLogin(t *testing.T) {
	m, dir := testManager(t)
	home := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", home)
	m.env = append(m.env, "GEMINI_CLI_HOME="+home)
	for _, k := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_USE_VERTEXAI", "GOOGLE_GENAI_USE_GCA", "GOOGLE_CLOUD_ACCESS_TOKEN"} {
		t.Setenv(k, "")
	}
	writeBin(t, dir, "gemini", fakeGemini)
	ctx := context.Background()
	if s, _ := m.Status(ctx, "gemini"); !s.Installed || s.Version != "0.63.0" || s.Auth.LoggedIn {
		t.Fatalf("status = %+v", s)
	}
	j, err := m.Login("gemini")
	if err != nil {
		t.Fatal(err)
	}
	// sign in with Google is picked so the CLI prints the link, not a menu
	raw, _ := os.ReadFile(filepath.Join(home, ".gemini", "settings.json"))
	if !strings.Contains(string(raw), `"selectedType": "oauth-personal"`) {
		t.Fatalf("settings = %s", raw)
	}
	waitURL := func(state string) {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if v := j.Snapshot(); len(v.URLs) == 1 && strings.Contains(v.URLs[0], "state="+state) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("want one link with state=%s; urls = %v", state, j.Snapshot().URLs)
	}
	waitURL("s1")
	// a wrong code: the newest link replaces the old one
	if err := j.Input("bad"); err != nil {
		t.Fatal(err)
	}
	waitURL("s2")
	if err := j.Input("good-code"); err != nil {
		t.Fatal(err)
	}
	// signed in: the job ends although the CLI goes on into its chat
	waitState(t, j, "succeeded")
	s, _ := m.Status(ctx, "gemini")
	if !s.Auth.LoggedIn || s.Auth.Account != "me@gmail.com" {
		t.Fatalf("after login = %+v", s.Auth)
	}
}

// fakeAgy acts like Antigravity CLI's full-screen sign-in: it waits for an
// answer to its terminal query, starts on a method menu, and prints the link
// at the window's width; `agy models` works once signed in.
const fakeAgy = `#!/bin/sh
STATE="$(dirname "$0")/.agy-signed-in"
case "$1" in
  --version) echo "1.3.1"; exit 0;;
  models) if [ -f "$STATE" ]; then echo "Fetching available models..."; echo "  gemini-3.8-flash-high"; echo "  gemini-3.5-pro"; exit 0; fi
          echo "Error: Please sign in to view available models."; exit 1;;
esac
stty raw -echo
printf '\033[>c'
REPLY=$(head -c 11)
case "$REPLY" in *">0;276;0c"*) ;; *) echo "no terminal reply"; exit 1;; esac
printf 'Select login method:\r\n > 1. Google OAuth\r\n'
KEY=$(head -c 1)
[ "$KEY" = "$(printf '\r')" ] || { echo "no enter"; exit 1; }
printf 'Open the URL below in your browser:\r\n'
printf 'https://accounts.google.com/o/oauth2/auth?client_id=x&state=%0'"$(stty size | cut -d' ' -f2)"'d\r\n' 0 | cut -c1-200
stty sane
printf 'authorization code... '
read CODE
[ "$CODE" = "good-code" ] && touch "$STATE"
echo "Signed in"
sleep 600
`

func TestAntigravityLogin(t *testing.T) {
	m, dir := testManager(t)
	writeBin(t, dir, "agy", fakeAgy)
	ctx := context.Background()
	if s, _ := m.Status(ctx, "antigravity"); !s.Installed || s.Version != "1.3.1" || s.Auth.LoggedIn {
		t.Fatalf("status = %+v", s)
	}
	j, err := m.Login("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(j.Snapshot().URLs) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	v := j.Snapshot()
	// the window is wide enough that the link is not wrapped
	if len(v.URLs) != 1 || !strings.HasPrefix(v.URLs[0], "https://accounts.google.com/") || len(v.URLs[0]) < 150 {
		t.Fatalf("urls = %v, output = %q", v.URLs, v.Output)
	}
	if err := j.Input("good-code"); err != nil {
		t.Fatal(err)
	}
	waitState(t, j, "succeeded") // signed in: the job ends though the CLI keeps running
	if s, _ := m.Status(ctx, "antigravity"); !s.Auth.LoggedIn {
		t.Fatalf("after login = %+v", s.Auth)
	}
}

func TestCodexStatusRejectedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	auth := filepath.Join(home, "auth.json")
	if err := os.WriteFile(auth, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(auth, past, past)
	m, dir := testManager(t)
	writeBin(t, dir, "codex", "#!/bin/sh\ncase \"$1 $2\" in \"--version \"*) echo codex-cli 1;; \"login status\") echo 'Logged in using ChatGPT';; esac\n")
	llm.MarkCodexRejected(home)
	if s, _ := m.Status(context.Background(), "codex"); s.Auth.LoggedIn {
		t.Fatalf("a session the server refused must read signed out: %+v", s.Auth)
	}
	// a new sign-in rewrites auth.json
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(auth, future, future)
	if s, _ := m.Status(context.Background(), "codex"); !s.Auth.LoggedIn {
		t.Fatalf("signed in again: %+v", s.Auth)
	}
}

func TestLogout(t *testing.T) {
	m, dir := testManager(t)
	marker := filepath.Join(dir, "logged-out")
	writeBin(t, dir, "codex", "#!/bin/sh\n[ \"$1\" = logout ] && touch "+marker+"\n")
	if err := m.Logout(context.Background(), "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("codex logout did not run")
	}
	// Gemini has no logout subcommand: its credentials file goes
	gh := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", gh)
	creds := filepath.Join(gh, ".gemini", "oauth_creds.json")
	_ = os.MkdirAll(filepath.Dir(creds), 0o700)
	_ = os.WriteFile(creds, []byte(`{}`), 0o600)
	if err := m.Logout(context.Background(), "gemini"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(creds); !os.IsNotExist(err) {
		t.Fatal("gemini credentials still there")
	}
	if err := m.Logout(context.Background(), "gemini"); err != nil {
		t.Fatalf("already signed out is fine: %v", err)
	}
}

func TestUpdateFollowsInstallMethod(t *testing.T) {
	m, dir := testManager(t)
	if _, err := m.Update("codex"); err != ErrNotInstalled {
		t.Fatalf("not installed err = %v", err)
	}
	// codex from a Homebrew cask: bin/codex links into Caskroom
	cask := filepath.Join(dir, "Caskroom", "codex", "1.0.0")
	if err := os.MkdirAll(cask, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBin(t, cask, "codex", "#!/bin/sh\necho codex-cli 1.0.0\n")
	if err := os.Symlink(filepath.Join(cask, "codex"), filepath.Join(dir, "codex")); err != nil {
		t.Fatal(err)
	}
	writeBin(t, dir, "brew", "#!/bin/sh\necho \"brew $*\"\n")
	s, _ := m.Status(context.Background(), "codex")
	if !strings.Contains(s.UpdateCmd, "brew upgrade --cask codex") {
		t.Fatalf("update command = %q", s.UpdateCmd)
	}
	j, err := m.Update("codex")
	if err != nil {
		t.Fatal(err)
	}
	if v := waitState(t, j, "succeeded"); v.Action != "update" || !strings.Contains(v.Output, "brew upgrade --cask codex") {
		t.Fatalf("job = %+v", v)
	}

	// Claude Code from the official installer updates itself
	writeBin(t, dir, "claude", "#!/bin/sh\necho \"claude $*\"\n")
	j, err = m.Update("claude")
	if err != nil {
		t.Fatal(err)
	}
	if v := waitState(t, j, "succeeded"); !strings.Contains(v.Output, "claude update") {
		t.Fatalf("claude job = %+v", v)
	}

	// Gemini CLI has no installer of its own: a plain binary is of unknown origin
	writeBin(t, dir, "gemini", "#!/bin/sh\necho 0.1.0\n")
	if _, err := m.Update("gemini"); err != ErrUnknownInstall {
		t.Fatalf("gemini err = %v", err)
	}
}

// DeepSeek Harness: installed is ready, there is no account to sign in.
func TestDSHNeedsNoLogin(t *testing.T) {
	m, dir := testManager(t)
	ctx := context.Background()
	if s, _ := m.Status(ctx, "dsh"); s.Installed || len(s.Methods) == 0 || s.Methods[0].Command != "npm install -g @deepseek-ai/dsh" {
		t.Fatalf("not installed status = %+v", s)
	}
	writeBin(t, dir, "dsh", "#!/bin/sh\necho 0.2.1-rc.5\n")
	if s, _ := m.Status(ctx, "dsh"); !s.Installed || !s.Auth.LoggedIn || s.LoginCmd != "" {
		t.Fatalf("installed status = %+v", s)
	}
	if _, err := m.Login("dsh"); err != ErrNoLogin {
		t.Fatalf("login = %v", err)
	}
	if err := m.Logout(ctx, "dsh"); err != ErrNoLogin {
		t.Fatalf("logout = %v", err)
	}
}
