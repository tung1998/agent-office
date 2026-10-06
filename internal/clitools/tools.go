// Package clitools installs and signs in the AI command-line tools the office
// can use (Claude Code, Codex) on the machine that runs the office server.
// Only commands defined here can run: the API never accepts a command string.
package clitools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
}

// Tool is a CLI the office can drive.
type Tool struct {
	ID       string `json:"id"` // claude | codex
	Name     string `json:"name"`
	Bin      string `json:"bin"`
	DocsURL  string `json:"docs_url"`
	LoginCmd string `json:"login_command"`

	methods   []Method
	loginArgv []string
	status    func(ctx context.Context, bin string, env []string) Auth
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
	Job       *JobView `json:"job,omitempty"` // running or last job
}

var tools = []Tool{
	{
		ID: "claude", Name: "Claude Code", Bin: "claude", DocsURL: "https://code.claude.com/docs/en/setup",
		LoginCmd: "claude auth login", loginArgv: []string{"claude", "auth", "login"},
		methods: []Method{
			{ID: "native", Label: "Trình cài chính thức", Command: "curl -fsSL https://claude.ai/install.sh | bash", Requires: "curl", Recommended: true,
				argv: []string{"/bin/sh", "-c", "curl -fsSL https://claude.ai/install.sh | bash"}},
			{ID: "brew", Label: "Homebrew", Command: "brew install --cask claude-code", Requires: "brew",
				argv: []string{"brew", "install", "--cask", "claude-code"}},
			{ID: "npm", Label: "npm (cần Node 22+)", Command: "npm install -g @anthropic-ai/claude-code", Requires: "npm",
				argv: []string{"npm", "install", "-g", "@anthropic-ai/claude-code"}},
		},
		status: claudeStatus,
	},
	{
		ID: "codex", Name: "Codex", Bin: "codex", DocsURL: "https://github.com/openai/codex",
		LoginCmd: "codex login --device-auth", loginArgv: []string{"codex", "login", "--device-auth"},
		methods: []Method{
			{ID: "brew", Label: "Homebrew", Command: "brew install --cask codex", Requires: "brew", Recommended: true,
				argv: []string{"brew", "install", "--cask", "codex"}},
			{ID: "npm", Label: "npm (cần Node 22+)", Command: "npm install -g @openai/codex", Requires: "npm",
				argv: []string{"npm", "install", "-g", "@openai/codex"}},
		},
		status: codexStatus,
	},
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
	out, err := runOut(ctx, env, bin, "login", "status")
	text := strings.TrimSpace(out)
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "not logged in"):
		return Auth{LoggedIn: false, Detail: text}
	case err == nil && strings.Contains(low, "logged in"):
		return Auth{LoggedIn: true, Detail: text}
	}
	if home, herr := os.UserHomeDir(); herr == nil {
		if _, serr := os.Stat(filepath.Join(home, ".codex", "auth.json")); serr == nil {
			return Auth{LoggedIn: true, Detail: "đã có ~/.codex/auth.json"}
		}
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
}

// NewManager builds a Manager. PATH is extended with the usual install
// locations so a freshly installed tool is found without restarting.
func NewManager() *Manager {
	return &Manager{jobs: map[string]*Job{}, last: map[string]*Job{}, env: toolEnv(), tools: tools}
}

func toolEnv() []string {
	home, _ := os.UserHomeDir()
	extra := []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, ".claude", "local"), "/opt/homebrew/bin", "/usr/local/bin"}
	if out, err := exec.Command("npm", "prefix", "-g").Output(); err == nil {
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

func (m *Manager) find(id string) (Tool, bool) {
	for _, t := range m.tools {
		if t.ID == id {
			return t, true
		}
	}
	return Tool{}, false
}

// ErrUnknownTool / ErrBusy / ErrUnknownMethod are returned to the API.
var (
	ErrUnknownTool   = errors.New("công cụ không được hỗ trợ")
	ErrUnknownMethod = errors.New("cách cài không hợp lệ hoặc máy chưa có công cụ cần thiết")
	ErrBusy          = errors.New("đang có một tác vụ chạy cho công cụ này")
	ErrNotInstalled  = errors.New("chưa cài công cụ này")
)

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
	t, ok := m.find(id)
	if !ok {
		return Status{}, ErrUnknownTool
	}
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
		s.Version = strings.TrimSpace(strings.SplitN(v, "\n", 2)[0])
		s.Auth = t.status(ctx, path, m.env)
	}
	m.mu.Lock()
	if j := m.last[id]; j != nil {
		snap := j.Snapshot()
		s.Job = &snap
	}
	m.mu.Unlock()
	return s, nil
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
		return m.start(id, "install", meth.argv, 15*time.Minute)
	}
	return nil, ErrUnknownMethod
}

// Login starts the tool's sign-in flow.
func (m *Manager) Login(id string) (*Job, error) {
	t, ok := m.find(id)
	if !ok {
		return nil, ErrUnknownTool
	}
	path := m.lookPath(t.Bin)
	if path == "" {
		return nil, ErrNotInstalled
	}
	argv := append([]string{path}, t.loginArgv[1:]...)
	return m.start(id, "login", argv, 15*time.Minute)
}

func (m *Manager) start(tool, action string, argv []string, timeout time.Duration) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.last[tool]; j != nil && j.Snapshot().State == "running" {
		return nil, ErrBusy
	}
	if p := m.lookPathLocked(argv[0]); p != "" {
		argv = append([]string{p}, argv[1:]...)
	}
	j, err := startJob(tool, action, argv, m.env, timeout)
	if err != nil {
		return nil, err
	}
	m.jobs[j.ID], m.last[tool] = j, j
	return j, nil
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
