// Package clitools installs, updates and signs in the AI command-line tools the office
// can use (Claude Code, Codex, Gemini CLI) on the machine that runs the office server.
// Only commands defined here can run: the API never accepts a command string.
package clitools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/cliuse"
	"github.com/tung1998/agent-office/internal/llm"
)

// Method is one way to install a tool.
type Method struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Command     string `json:"command"`   // shown to the user before running
	Requires    string `json:"requires"`  // binary that must exist (curl, brew, npm)
	Available   bool   `json:"available"` // Requires found on this machine
	Recommended bool   `json:"recommended"`
	argv        []string
	update      []string // brings a tool installed this way to its latest version
}

// Tool is a CLI the office can drive.
type Tool struct {
	ID       string `json:"id"` // claude | codex | gemini
	Name     string `json:"name"`
	Bin      string `json:"bin"`
	DocsURL  string `json:"docs_url"`
	LoginCmd string `json:"login_command"`
	// LogoutCmd is shown before signing out: a subcommand, or the
	// credentials file removed when the tool has none (logoutFiles)
	LogoutCmd string `json:"logout_command"`

	methods     []Method
	loginArgv   []string
	logoutArgv  []string
	logoutFiles func(env []string) []string
	status      func(ctx context.Context, bin string, env []string) Auth
	// loginEnv / prepareLogin set up a sign-in that is no subcommand of its
	// own; loginDone ends it once signed in (Gemini's sign-in opens its chat).
	loginEnv     []string
	loginTerm    term
	prepareLogin func(env []string) error
	loginDone    func(ctx context.Context, bin string, env []string) bool
	// homeEnv names the variable that moves the tool's config and
	// credentials to another folder: a second account (ADR-178); share is
	// what that folder takes from the machine's
	homeEnv string
	share   shared
}

// shared is how a second account's folder shares the machine's setup
// (ADR-178): links (with every *.md) and copies, never the credentials.
type shared struct {
	src    func() string // the machine's folder
	sub    string        // where in the account's folder the tool looks ("" = the folder)
	links  []string
	copies []string // copied once: the account may change its own (Gemini's sign-in method)
}

// Auth is a tool's sign-in state.
type Auth struct {
	LoggedIn bool   `json:"logged_in"`
	Account  string `json:"account,omitempty"` // email or plan, when the tool says
	Method   string `json:"method,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Status is what the dashboard shows for a tool.
type Status struct {
	Tool
	Installed bool     `json:"installed"`
	Path      string   `json:"path,omitempty"`
	Version   string   `json:"version,omitempty"`
	Auth      Auth     `json:"auth"`
	Methods   []Method `json:"methods"`
	// UpdateCmd updates the tool the way it was installed ("" when that is unknown)
	UpdateCmd string   `json:"update_command,omitempty"`
	Job       *JobView `json:"job,omitempty"` // running or last job
	// Latest is the newest version published ("" = not known yet) and
	// Outdated whether it is newer than the installed one (ADR-259)
	Latest   string `json:"latest,omitempty"`
	Outdated bool   `json:"outdated,omitempty"`
	// LatestChecking: the newest version is being read again (it may change soon)
	LatestChecking bool `json:"latest_checking,omitempty"`
	// InUse: runs that have the CLI running now; Upgrade: its update in
	// progress or the last one
	InUse   int          `json:"in_use"`
	Upgrade *UpgradeView `json:"upgrade,omitempty"`
	// Used: a connection runs it (the update card shows only those);
	// Installs: the copies they run, when more than one (ADR-279)
	Used     bool      `json:"used"`
	Installs []Install `json:"installs,omitempty"`
}

var tools = []Tool{
	{
		ID: "claude", Name: "Claude Code", Bin: "claude", DocsURL: "https://code.claude.com/docs/en/setup",
		LoginCmd: "claude auth login", loginArgv: []string{"claude", "auth", "login"},
		LogoutCmd: "claude auth logout", logoutArgv: []string{"claude", "auth", "logout"},
		homeEnv: "CLAUDE_CONFIG_DIR",
		// the person's setup and the sessions (a turn resumes on another
		// account), never the .claude.json backups or the organisation's
		// policy and remote settings
		share: shared{src: claudeDir, links: []string{"settings.json", "skills", "agents", "commands", "plugins", "hooks", "output-styles", "projects"}},
		methods: []Method{
			{ID: "native", Label: "Trình cài chính thức", Command: "curl -fsSL https://claude.ai/install.sh | bash", Requires: "curl", Recommended: true,
				argv: []string{"/bin/sh", "-c", "curl -fsSL https://claude.ai/install.sh | bash"}, update: []string{"claude", "update"}},
			{ID: "brew", Label: "Homebrew", Command: "brew install --cask claude-code", Requires: "brew",
				argv: []string{"brew", "install", "--cask", "claude-code"}, update: []string{"brew", "upgrade", "--cask", "claude-code"}},
			{ID: "npm", Label: "npm (cần Node 22+)", Command: "npm install -g @anthropic-ai/claude-code", Requires: "npm",
				argv: []string{"npm", "install", "-g", "@anthropic-ai/claude-code"}, update: []string{"npm", "install", "-g", "@anthropic-ai/claude-code@latest"}},
		},
		status: claudeStatus,
	},
	{
		ID: "codex", Name: "Codex", Bin: "codex", DocsURL: "https://github.com/openai/codex",
		LoginCmd: "codex login --device-auth", loginArgv: []string{"codex", "login", "--device-auth"},
		LogoutCmd: "codex logout", logoutArgv: []string{"codex", "logout"},
		homeEnv: "CODEX_HOME",
		share:   shared{src: llm.CodexHome, links: []string{"config.toml", "prompts", "skills", "rules"}},
		methods: []Method{
			// like Antigravity: a quarantined cask binary hangs on a Gatekeeper
			// check when the office (no window) runs it
			{ID: "brew", Label: "Homebrew", Command: "brew install --cask codex", Requires: "brew", Recommended: true,
				argv:   []string{"/bin/sh", "-c", `brew install --cask codex && { xattr -dr com.apple.quarantine "$(brew --prefix)/Caskroom/codex" 2>/dev/null || true; }`},
				update: []string{"/bin/sh", "-c", `brew upgrade --cask codex && { xattr -dr com.apple.quarantine "$(brew --prefix)/Caskroom/codex" 2>/dev/null || true; }`}},
			{ID: "npm", Label: "npm (cần Node 22+)", Command: "npm install -g @openai/codex", Requires: "npm",
				argv: []string{"npm", "install", "-g", "@openai/codex"}, update: []string{"npm", "install", "-g", "@openai/codex@latest"}},
		},
		status: codexStatus,
	},
	{
		ID: "gemini", Name: "Gemini CLI", Bin: "gemini", DocsURL: "https://github.com/google-gemini/gemini-cli",
		// Gemini signs in when it starts; NO_BROWSER prints the URL and asks for the code
		LoginCmd: "gemini", loginArgv: []string{"gemini"},
		// no logout subcommand (only /logout inside its chat): drop the Google credentials
		LogoutCmd: "rm ~/.gemini/oauth_creds.json", logoutFiles: func(env []string) []string { return []string{filepath.Join(llm.GeminiDir(env), "oauth_creds.json")} },
		homeEnv: "GEMINI_CLI_HOME",
		// sessions and the project registry too; settings.json is copied:
		// it holds the sign-in method
		share: shared{src: func() string { return llm.GeminiDir(nil) }, sub: ".gemini",
			links: []string{"extensions", "commands", "skills", "tmp", "history", "projects.json"}, copies: []string{"settings.json"}},
		loginEnv: []string{"NO_BROWSER=true", "GEMINI_CLI_TRUST_WORKSPACE=true"},
		methods: []Method{
			{ID: "npm", Label: "npm (cần Node 20+)", Command: "npm install -g @google/gemini-cli", Requires: "npm", Recommended: true,
				argv: []string{"npm", "install", "-g", "@google/gemini-cli"}, update: []string{"npm", "install", "-g", "@google/gemini-cli@latest"}},
			{ID: "brew", Label: "Homebrew", Command: "brew install gemini-cli", Requires: "brew",
				argv: []string{"brew", "install", "gemini-cli"}, update: []string{"brew", "upgrade", "gemini-cli"}},
		},
		status:       geminiStatus,
		prepareLogin: geminiPrepareLogin,
		loginDone:    func(_ context.Context, _ string, env []string) bool { return llm.GeminiSignedIn(llm.GeminiDir(env)) },
	},
	{
		// Antigravity CLI replaces Gemini CLI for Google accounts (AI Pro/Ultra
		// and free) since June 2026 (ADR-106)
		ID: "antigravity", Name: "Antigravity CLI", Bin: "agy", DocsURL: "https://antigravity.google/docs/cli",
		LoginCmd: "agy", loginArgv: []string{"agy"},
		// no logout subcommand either
		LogoutCmd: "rm ~/.gemini/antigravity-cli/antigravity-oauth-token",
		logoutFiles: func([]string) []string {
			home, _ := os.UserHomeDir()
			return []string{filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")}
		},
		// a full-screen sign-in: it waits for answers to its terminal queries,
		// wraps the link at the window width, and starts on a method menu
		loginTerm: term{cols: 1000, answer: true, keys: []autoKey{{after: "Select login method", keys: "\r"}}},
		methods: []Method{
			// a cask straight from the internet waits on a Gatekeeper dialog
			// nobody at the dashboard can click; Google signs the binary
			{ID: "brew", Label: "Homebrew", Command: "brew install --cask antigravity-cli", Requires: "brew", Recommended: true,
				argv:   []string{"/bin/sh", "-c", `brew install --cask antigravity-cli && xattr -dr com.apple.quarantine "$(brew --prefix)/Caskroom/antigravity-cli"`},
				update: []string{"/bin/sh", "-c", `brew upgrade --cask antigravity-cli && xattr -dr com.apple.quarantine "$(brew --prefix)/Caskroom/antigravity-cli"`}},
		},
		status: antigravityStatus,
		loginDone: func(ctx context.Context, bin string, env []string) bool {
			_, err := llm.AntigravityModels(ctx, bin, env)
			return err == nil
		},
	},
	{
		// DeepSeek Harness (ADR-245): no account to sign in, its connection
		// carries the API key; installed is ready
		ID: "dsh", Name: "DeepSeek Harness", Bin: "dsh", DocsURL: "https://github.com/deepseek-ai/deepseek-harness",
		methods: []Method{
			{ID: "npm", Label: "npm (cần Node 22+)", Command: "npm install -g @deepseek-ai/dsh", Requires: "npm", Recommended: true,
				argv: []string{"npm", "install", "-g", "@deepseek-ai/dsh"}, update: []string{"npm", "install", "-g", "@deepseek-ai/dsh@latest"}},
		},
		status: func(context.Context, string, []string) Auth { return Auth{LoggedIn: true, Method: "api_key"} },
	},
}

// ErrNoLogin: the tool has no account to sign in to (DeepSeek Harness).
var ErrNoLogin = errors.New("công cụ này không cần đăng nhập: API key nằm ở kết nối")

// antigravityStatus asks Antigravity CLI for its models, which needs a
// signed-in account; it has no status subcommand.
func antigravityStatus(ctx context.Context, bin string, env []string) Auth {
	if _, err := llm.AntigravityModels(ctx, bin, env); err != nil {
		return Auth{Detail: err.Error()}
	}
	return Auth{LoggedIn: true}
}

// geminiStatus: Gemini CLI has no status subcommand; read its credentials.
func geminiStatus(ctx context.Context, bin string, env []string) Auth {
	if ok, account := llm.GeminiAccount(llm.GeminiDir(env)); ok {
		return Auth{LoggedIn: true, Account: account}
	}
	return Auth{Detail: "chưa đăng nhập Google hoặc chưa có GEMINI_API_KEY"}
}

// geminiPrepareLogin picks "Login with Google" in ~/.gemini/settings.json when
// no sign-in method is chosen yet, so the CLI goes straight to the URL instead
// of a menu the dashboard cannot drive.
func geminiPrepareLogin(env []string) error {
	dir := llm.GeminiDir(env)
	path := filepath.Join(dir, "settings.json")
	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &settings) != nil {
			return nil // comments or a format we would lose: leave it, the CLI asks
		}
	}
	sec, _ := settings["security"].(map[string]any)
	if sec == nil {
		sec = map[string]any{}
	}
	auth, _ := sec["auth"].(map[string]any)
	if auth == nil {
		auth = map[string]any{}
	}
	if t, _ := auth["selectedType"].(string); t != "" {
		return nil
	}
	auth["selectedType"] = "oauth-personal"
	sec["auth"], settings["security"] = auth, sec
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// claudeDir is the machine's Claude Code config folder.
func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude")
}

// claudeStatus reads `claude auth status --json`.
func claudeStatus(ctx context.Context, bin string, env []string) Auth {
	out, _ := runOut(ctx, env, bin, "auth", "status", "--json")
	var s struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
		Email      string `json:"email"`
		OrgName    string `json:"orgName"`
	}
	if err := json.Unmarshal([]byte(firstJSON(out)), &s); err != nil {
		return Auth{Detail: "không đọc được trạng thái đăng nhập"}
	}
	a := Auth{LoggedIn: s.LoggedIn, Account: s.Email, Method: s.AuthMethod}
	if s.OrgName != "" && s.Email != "" {
		a.Account = s.Email + " · " + s.OrgName
	}
	return a
}

// codexStatus trusts `codex login status` when it answers; the credentials
// file is only a fallback for versions without that subcommand.
func codexStatus(ctx context.Context, bin string, env []string) Auth {
	home := llm.CodexHomeIn(env)
	out, err := runOut(ctx, env, bin, "login", "status")
	text := strings.TrimSpace(out)
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "not logged in"):
		return Auth{LoggedIn: false, Detail: text}
	case err == nil && strings.Contains(low, "logged in"):
		// the CLI only checks the file exists: an expired session reads "Logged in" too
		if expired, detail := llm.CodexSessionExpired(home, time.Now()); expired {
			return Auth{LoggedIn: false, Detail: detail}
		}
		// nor does it know the server refused the session in a real call
		if rejected, detail := llm.CodexRejected(home); rejected {
			return Auth{LoggedIn: false, Detail: detail}
		}
		return Auth{LoggedIn: true, Detail: text}
	}
	if _, serr := os.Stat(filepath.Join(home, "auth.json")); serr == nil {
		if expired, detail := llm.CodexSessionExpired(home, time.Now()); expired {
			return Auth{LoggedIn: false, Detail: detail}
		}
		if rejected, detail := llm.CodexRejected(home); rejected {
			return Auth{LoggedIn: false, Detail: detail}
		}
		return Auth{LoggedIn: true, Detail: "đã có " + filepath.Join(home, "auth.json")}
	}
	return Auth{LoggedIn: false, Detail: text}
}

// Manager tracks tool status and install/login jobs.
type Manager struct {
	mu    sync.Mutex
	jobs  map[string]*Job // by id
	last  map[string]*Job // latest job per tool
	env   []string
	tools []Tool

	// newest versions published (ADR-259)
	latest   map[string]latestEntry
	registry string       // npm registry ("" = registry.npmjs.org)
	httpc    *http.Client // nil = a default one
	noFetch  bool         // Status never reads newest versions on its own (tests)
	now      func() time.Time

	// updates of a CLI in use (ADR-259)
	upgrades     map[string]*upgrade
	upPoll       time.Duration // 0 = 5s
	upMaxWait    time.Duration // 0 = UpgradeMaxWait
	upGrace      time.Duration // 0 = UpgradeDrainAfter
	upDrainMax   time.Duration // 0 = UpgradeDrainMax
	afterUpdate  func(ctx context.Context, tool string) string
	bad          map[string]string // per tool, a version whose update was put back
	badLoaded    bool              // bad took what the setting kept (SyncBad)
	onUpgradeEnd func(UpgradeView)
	// uses: the binaries the connections run per tool (SetUses, ADR-279)
	uses func(ctx context.Context) map[string][]string
}

// NewManager builds a Manager. PATH is extended with the usual install
// locations so a freshly installed tool is found without restarting.
func NewManager() *Manager {
	return &Manager{jobs: map[string]*Job{}, last: map[string]*Job{}, env: toolEnv(), tools: tools}
}

func toolEnv() []string {
	home, _ := os.UserHomeDir()
	// the person's own Node first: a system npm (/usr/local) installs into
	// a root-owned folder and fails with EACCES
	extra := append(nodeBins(home), filepath.Join(home, ".local", "bin"), filepath.Join(home, ".claude", "local"), "/opt/homebrew/bin", "/usr/local/bin")
	npm := "npm"
	for _, d := range extra {
		if _, err := os.Stat(filepath.Join(d, "npm")); err == nil {
			npm = filepath.Join(d, "npm")
			break
		}
	}
	if out, err := exec.Command(npm, "prefix", "-g").Output(); err == nil {
		extra = append(extra, filepath.Join(strings.TrimSpace(string(out)), "bin"))
	}
	env := os.Environ()
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = "PATH=" + strings.Join(append(extra, strings.TrimPrefix(kv, "PATH=")), string(os.PathListSeparator))
			return append(env, "TERM=xterm-256color", "NO_COLOR=1")
		}
	}
	return append(env, "PATH="+strings.Join(extra, string(os.PathListSeparator)), "TERM=xterm-256color", "NO_COLOR=1")
}

// nodeBins finds Node installed by a version manager (nvm, Volta), which a
// service started outside a login shell does not have on PATH: the active
// nvm one, else the newest.
func nodeBins(home string) []string {
	var out []string
	if b := os.Getenv("NVM_BIN"); b != "" {
		out = append(out, b)
	} else if dirs, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "v*", "bin")); len(dirs) > 0 {
		slices.SortFunc(dirs, func(a, b string) int { return semverCmp(versionOf(b), versionOf(a)) })
		out = append(out, dirs[0])
	}
	if _, err := os.Stat(filepath.Join(home, ".volta", "bin")); err == nil {
		out = append(out, filepath.Join(home, ".volta", "bin"))
	}
	return out
}

// versionOf reads [major minor patch] from ".../v22.3.1/bin".
func versionOf(binDir string) [3]int {
	var v [3]int
	parts := strings.SplitN(strings.TrimPrefix(filepath.Base(filepath.Dir(binDir)), "v"), ".", 3)
	for i, p := range parts {
		v[i], _ = strconv.Atoi(p)
	}
	return v
}

func semverCmp(a, b [3]int) int {
	for i := range a {
		if a[i] != b[i] {
			return a[i] - b[i]
		}
	}
	return 0
}

// Env is the environment tools run with (PATH includes the usual install
// locations); other runners (project processes) reuse it.
func (m *Manager) Env() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.env...)
}

// LookPath finds bin the way install/login do (including ~/.local/bin, npm's
// global bin and Homebrew), so a tool installed from the dashboard is usable
// without restarting the office.
func (m *Manager) LookPath(bin string) string { return m.lookPath(bin) }

func (m *Manager) lookPath(bin string) string {
	for _, kv := range m.env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			for _, dir := range filepath.SplitList(p) {
				f := filepath.Join(dir, bin)
				if st, err := os.Stat(f); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
					return f
				}
			}
		}
	}
	return ""
}

// Duplicates finds every other executable also on PATH under the same
// command name as bin (lookPath stops at the first; this looks at all of
// them), excluding bin itself. More than one install of the same CLI is the
// common way an update of one leaves the copy office actually runs
// untouched and still broken (an npm update reaching a different global
// prefix than the one earlier on PATH, a stray Homebrew copy ahead of a
// native install, …).
func (m *Manager) Duplicates(bin string) []string {
	name := filepath.Base(bin)
	var out []string
	seen := map[string]bool{filepath.Clean(bin): true}
	for _, kv := range m.env {
		p, ok := strings.CutPrefix(kv, "PATH=")
		if !ok {
			continue
		}
		for _, dir := range filepath.SplitList(p) {
			f := filepath.Join(dir, name)
			if seen[f] {
				continue
			}
			if st, err := os.Stat(f); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

func (m *Manager) find(id string) (Tool, bool) {
	for _, t := range m.tools {
		if t.ID == id {
			return t, true
		}
	}
	return Tool{}, false
}

// findByBin finds the tool whose command is bin (its ID when they match, as
// for codex/claude/gemini, else its Bin — antigravity's ID is "antigravity"
// but its command is "agy").
func (m *Manager) findByBin(bin string) (Tool, bool) {
	for _, t := range m.tools {
		if t.Bin == bin {
			return t, true
		}
	}
	return Tool{}, false
}

// ErrUnknownTool / ErrBusy / ErrUnknownMethod are returned to the API.
var (
	ErrUnknownTool    = errors.New("công cụ không được hỗ trợ")
	ErrUnknownMethod  = errors.New("cách cài không hợp lệ hoặc máy chưa có công cụ cần thiết")
	ErrBusy           = errors.New("đang có một tác vụ chạy cho công cụ này")
	ErrNotInstalled   = errors.New("chưa cài công cụ này")
	ErrUnknownInstall = errors.New("không rõ công cụ được cài bằng cách nào, hãy cập nhật bằng tay")
)

// IDs are the tools the office knows, in the dashboard's order.
func (m *Manager) IDs() []string {
	ids := make([]string, 0, len(m.tools))
	for _, t := range m.tools {
		ids = append(ids, t.ID)
	}
	return ids
}

// List returns every tool's status.
func (m *Manager) List(ctx context.Context) []Status {
	out := make([]Status, 0, len(m.tools))
	for _, t := range m.tools {
		s, _ := m.Status(ctx, t.ID)
		out = append(out, s)
	}
	return out
}

// Status inspects one tool: installed, version, sign-in, install methods, last job.
func (m *Manager) Status(ctx context.Context, id string) (Status, error) {
	return m.StatusIn(ctx, id, "")
}

// StatusIn is Status for the account kept in home ("" = the machine's).
func (m *Manager) StatusIn(ctx context.Context, id, home string) (Status, error) {
	t, ok := m.find(id)
	if !ok {
		return Status{}, ErrUnknownTool
	}
	vars, err := homeVars(t, home)
	if err != nil {
		return Status{}, err
	}
	env := append(m.Env(), vars...)
	s := Status{Tool: t, Methods: []Method{}}
	for _, meth := range t.methods {
		meth.Available = m.lookPath(meth.Requires) != ""
		s.Methods = append(s.Methods, meth)
	}
	if path := m.lookPath(t.Bin); path != "" {
		s.Installed, s.Path = true, path
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		v, _ := runOut(ctx, m.env, path, "--version")
		s.Version = versionLine(v)
		s.Auth = t.status(ctx, path, env)
		if meth, ok := updateMethod(t, path); ok {
			s.UpdateCmd = displayCommand(meth.update)
		}
		if home == "" {
			s.Latest, s.LatestChecking = m.latestState(id)
			s.Outdated = Newer(s.Version, s.Latest)
		}
	}
	if home == "" {
		m.statusInstalls(ctx, t, &s)
	}
	s.InUse = cliuse.InUse(id)
	if u, ok := m.Upgrade(id); ok {
		s.Upgrade = &u
	}
	m.mu.Lock()
	if j := m.last[jobKey(id, home)]; j != nil {
		snap := j.Snapshot()
		s.Job = &snap
	}
	m.mu.Unlock()
	return s, nil
}

// Detect is the cheap part of Status: installed and version, no sign-in check
// (those can take seconds, e.g. Antigravity lists its models).
func (m *Manager) Detect(ctx context.Context, id string) (installed bool, version string, err error) {
	t, ok := m.find(id)
	if !ok {
		return false, "", ErrUnknownTool
	}
	path := m.lookPath(t.Bin)
	if path == "" {
		return false, "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, _ := runOut(ctx, m.env, path, "--version")
	return true, versionLine(v), nil
}

// versionLine is the line of a CLI's --version output that has its version:
// a warning printed first (Codex about its PATH aliases) is skipped.
func versionLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines {
		if VersionOf(l) != "" {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(lines[0])
}

// Install starts an install job with one of the tool's methods.
func (m *Manager) Install(id, method string) (*Job, error) {
	t, ok := m.find(id)
	if !ok {
		return nil, ErrUnknownTool
	}
	for _, meth := range t.methods {
		if meth.ID != method {
			continue
		}
		if m.lookPath(meth.Requires) == "" {
			return nil, ErrUnknownMethod
		}
		// brew asks [y/n] before installing (the default since Homebrew 6, or
		// HOMEBREW_ASK before); the install box cannot answer
		return m.start(id, id, "install", meth.argv, 15*time.Minute, term{}, "HOMEBREW_NO_ASK=1", "HOMEBREW_ASK=")
	}
	return nil, ErrUnknownMethod
}

// Update starts a job that brings the tool to its latest version, the way it
// was installed (official installer, Homebrew or npm).
func (m *Manager) Update(id string) (*Job, error) {
	t, ok := m.find(id)
	if !ok {
		return nil, ErrUnknownTool
	}
	path := m.lookPath(t.Bin)
	if path == "" {
		return nil, ErrNotInstalled
	}
	meth, ok := updateMethod(t, path)
	if !ok {
		return nil, ErrUnknownInstall
	}
	return m.start(id, id, "update", meth.update, 15*time.Minute, term{}, "HOMEBREW_NO_ASK=1", "HOMEBREW_ASK=")
}

// updateMethod is the method the tool at path was installed with, read from
// where its binary really lives: an npm package (or a Volta shim), Homebrew
// (its Cellar/Caskroom, or a cask binary copied into its prefix), else the
// official installer.
func updateMethod(t Tool, path string) (Method, bool) {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	id := "native"
	switch {
	case strings.Contains(real, "/node_modules/") || strings.Contains(real, "/.volta/"):
		id = "npm"
	case strings.Contains(real, "/Caskroom/") || strings.Contains(real, "/Cellar/") ||
		strings.HasPrefix(real, "/opt/homebrew/") || strings.HasPrefix(real, "/home/linuxbrew/"):
		id = "brew"
	}
	for _, meth := range t.methods {
		if meth.ID == id && len(meth.update) > 0 {
			return meth, true
		}
	}
	return Method{}, false
}

// Login starts the tool's sign-in flow.
func (m *Manager) Login(id string) (*Job, error) { return m.LoginIn(id, "") }

// LoginIn signs in the account kept in home ("" = the machine's).
func (m *Manager) LoginIn(id, home string) (*Job, error) {
	t, ok := m.find(id)
	if !ok {
		return nil, ErrUnknownTool
	}
	if len(t.loginArgv) == 0 {
		return nil, ErrNoLogin
	}
	vars, err := homeVars(t, home)
	if err != nil {
		return nil, err
	}
	path := m.lookPath(t.Bin)
	if path == "" {
		return nil, ErrNotInstalled
	}
	if home != "" {
		if err := PrepareHome(id, home); err != nil {
			return nil, err
		}
	}
	if t.prepareLogin != nil {
		if err := t.prepareLogin(append(m.Env(), vars...)); err != nil {
			return nil, err
		}
	}
	argv := append([]string{path}, t.loginArgv[1:]...)
	j, err := m.start(jobKey(id, home), id, "login", argv, 15*time.Minute, t.loginTerm, append(vars, t.loginEnv...)...)
	if err == nil && t.loginDone != nil {
		go func() {
			tick := time.NewTicker(2 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-j.done:
					return
				case <-tick.C:
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					done := t.loginDone(ctx, path, append(m.Env(), vars...))
					cancel()
					if done {
						j.finish()
						return
					}
				}
			}
		}()
	}
	return j, err
}

// Logout signs the tool out: its logout subcommand, or removing its
// credentials file. Signing in again after it starts from a clean slate
// (Gemini's sign-in skips straight to its chat while credentials exist).
func (m *Manager) Logout(ctx context.Context, id string) error { return m.LogoutIn(ctx, id, "") }

// LogoutIn signs out the account kept in home ("" = the machine's).
func (m *Manager) LogoutIn(ctx context.Context, id, home string) error {
	t, ok := m.find(id)
	if !ok {
		return ErrUnknownTool
	}
	if len(t.loginArgv) == 0 {
		return ErrNoLogin
	}
	vars, err := homeVars(t, home)
	if err != nil {
		return err
	}
	m.mu.Lock()
	j := m.last[jobKey(id, home)]
	m.mu.Unlock()
	if j != nil && j.Snapshot().State == "running" {
		return ErrBusy
	}
	if t.logoutFiles != nil {
		for _, f := range t.logoutFiles(append(m.Env(), vars...)) {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	path := m.lookPath(t.Bin)
	if path == "" {
		return ErrNotInstalled
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if out, err := runOut(ctx, append(m.Env(), vars...), path, t.logoutArgv[1:]...); err != nil {
		return fmt.Errorf("%s: %v: %s", t.LogoutCmd, err, strings.TrimSpace(out))
	}
	return nil
}

// start runs a job; key is what at most one job runs for at a time (the
// tool, or the tool in one account's folder).
func (m *Manager) start(key, tool, action string, argv []string, timeout time.Duration, tm term, env ...string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.last[key]; j != nil && j.Snapshot().State == "running" {
		return nil, ErrBusy
	}
	if p := m.lookPathLocked(argv[0]); p != "" {
		argv = append([]string{p}, argv[1:]...)
	}
	j, err := startJob(tool, action, argv, append(slices.Clone(m.env), env...), timeout, tm)
	if err != nil {
		return nil, err
	}
	m.jobs[j.ID], m.last[key] = j, j
	go m.expireJob(j)
	return j, nil
}

// expireJob drops a finished job from m.jobs a while after it ends, so
// m.jobs doesn't grow without bound over a long-lived server; m.last[tool]
// keeps its own reference for Status(), unaffected by this.
func (m *Manager) expireJob(j *Job) {
	<-j.done
	time.Sleep(10 * time.Minute)
	m.mu.Lock()
	delete(m.jobs, j.ID)
	m.mu.Unlock()
}

func (m *Manager) lookPathLocked(bin string) string {
	if strings.Contains(bin, "/") {
		return bin
	}
	return m.lookPath(bin)
}

// Job returns a job by id.
func (m *Manager) Job(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

// ErrNoHome: the tool cannot keep a second account in a folder of its own.
var ErrNoHome = errors.New("công cụ này chưa hỗ trợ nhiều tài khoản")

// homeVars is what the tool's environment adds for the account in home.
func homeVars(t Tool, home string) ([]string, error) {
	if home == "" {
		return nil, nil
	}
	if t.homeEnv == "" {
		return nil, ErrNoHome
	}
	return []string{t.homeEnv + "=" + home}, nil
}

func jobKey(id, home string) string {
	if home == "" {
		return id
	}
	return id + "@" + home
}

// PrepareHome makes home the config folder of a second account of tool
// (ADR-178): the machine's setup (Tool.share) is linked in, so the account
// differs and the setup does not. Run again, it links what is new.
func PrepareHome(tool, home string) error {
	t, ok := tools[0], false
	for _, x := range tools {
		if x.ID == tool {
			t, ok = x, true
		}
	}
	if !ok || t.homeEnv == "" {
		return ErrNoHome
	}
	dir := filepath.Join(home, t.share.sub)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	src := t.share.src()
	entries, err := os.ReadDir(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		link := slices.Contains(t.share.links, name) || (!e.IsDir() && strings.HasSuffix(name, ".md"))
		if !link && !slices.Contains(t.share.copies, name) {
			continue
		}
		dst := filepath.Join(dir, name)
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if link {
			err = os.Symlink(filepath.Join(src, name), dst)
		} else if raw, rerr := os.ReadFile(filepath.Join(src, name)); rerr == nil {
			err = os.WriteFile(dst, raw, 0o600)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func runOut(ctx context.Context, env []string, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func firstJSON(s string) string {
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}

// NewManagerWithPath builds a Manager that only looks for tools in path
// (tests, or pinning where tools live).
func NewManagerWithPath(path string) *Manager {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	env = append(env, "PATH="+path, "TERM=xterm-256color", "NO_COLOR=1")
	return &Manager{jobs: map[string]*Job{}, last: map[string]*Job{}, env: env, tools: tools}
}
