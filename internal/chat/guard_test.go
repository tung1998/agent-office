package chat

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func guardSays(t *testing.T, ev map[string]any, userMCP bool) string {
	t.Helper()
	b, _ := json.Marshal(ev)
	var out bytes.Buffer
	Guard(bytes.NewReader(b), &out, userMCP, []string{"context7"})
	return out.String()
}

// Edits stay in the working folder, .claude/skills included; MCP is office's own
// unless the agent may use the person's servers.
func TestGuard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	in := func(p string) map[string]any {
		return map[string]any{"cwd": dir, "tool_name": "Write", "tool_input": map[string]any{"file_path": p}}
	}
	for _, p := range []string{filepath.Join(dir, ".claude/skills/x/SKILL.md"), "src/a.go", filepath.Join(dir, "new/deep/b.md")} {
		if s := guardSays(t, in(p), false); s != "" {
			t.Errorf("%s denied: %s", p, s)
		}
	}
	for _, p := range []string{filepath.Join(filepath.Dir(dir), "out.txt"), "../out.txt", "/etc/hosts", filepath.Join(dir, "src/../../x")} {
		if s := guardSays(t, in(p), false); !strings.Contains(s, `"deny"`) {
			t.Errorf("%s allowed", p)
		}
	}
	os.Symlink("/etc", filepath.Join(dir, "link"))
	if s := guardSays(t, in(filepath.Join(dir, "link/hosts")), false); !strings.Contains(s, `"deny"`) {
		t.Error("a symlink out of the folder is allowed")
	}
	mcp := func(name string) map[string]any { return map[string]any{"cwd": dir, "tool_name": name} }
	if s := guardSays(t, mcp("mcp__jira__search"), false); !strings.Contains(s, `"deny"`) {
		t.Error("user MCP allowed without the capability")
	}
	if s := guardSays(t, mcp("mcp__jira__search"), true); s != "" {
		t.Error("user MCP denied with the capability")
	}
	if s := guardSays(t, mcp("mcp__office__propose_change"), false); s != "" {
		t.Errorf("office MCP denied: %s", s)
	}
	if s := guardSays(t, mcp("mcp__context7__resolve"), false); s != "" { // the office's gateway (ADR-091)
		t.Errorf("gateway MCP denied: %s", s)
	}
	if s := guardSays(t, mcp("mcp__context7x__resolve"), false); !strings.Contains(s, `"deny"`) {
		t.Error("a name sharing the gateway's prefix allowed")
	}
}

// With the guard, a chat that edits runs under bypassPermissions (Claude Code
// lets it write .claude/skills) and every edit goes through the hook.
func TestClaudeArgsGuard(t *testing.T) {
	var r claudeRunner
	old := GuardCommand
	defer func() { GuardCommand = old }()
	GuardCommand = ""
	if a := r.args(RunRequest{Write: true}, false); a[slices.Index(a, "--permission-mode")+1] != "dontAsk" {
		t.Fatalf("no guard, still dontAsk: %v", a)
	}
	GuardCommand = "'/x/office' hook guard"
	a := r.args(RunRequest{Write: true, DenyPaths: []string{"secrets/"}}, false)
	if a[slices.Index(a, "--permission-mode")+1] != "bypassPermissions" {
		t.Fatalf("mode: %v", a)
	}
	i := slices.Index(a, "--settings")
	if i < 0 || !strings.Contains(a[i+1], `hook guard`) || !strings.Contains(a[i+1], "Write|Edit") {
		t.Fatalf("guard hook missing: %v", a)
	}
	if d := a[slices.Index(a, "--disallowedTools")+1]; !strings.Contains(d, "Edit(./secrets/**)") || !strings.Contains(d, "Edit(./.env)") {
		t.Fatalf("deny rules lost: %s", d)
	}
	if t2 := a[slices.Index(a, "--tools")+1]; strings.Contains(t2, "Bash") {
		t.Fatalf("tools: %s", t2)
	}
	if b := r.args(RunRequest{}, false); b[slices.Index(b, "--permission-mode")+1] != "dontAsk" {
		t.Fatal("a read-only chat must stay dontAsk")
	}
}

// What would let an agent run commands (Claude Code settings and hooks, MCP
// servers, git hooks) is never written; a link out, even to nothing yet, is
// refused; an unclear request is refused.
func TestGuardProtected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	in := func(p string) map[string]any {
		return map[string]any{"cwd": dir, "tool_name": "Write", "tool_input": map[string]any{"file_path": p}}
	}
	for _, p := range []string{".claude/settings.json", ".claude/settings.local.json", ".claude/hooks/x.sh", ".mcp.json", ".git/hooks/pre-commit", "~/.bashrc"} {
		if s := guardSays(t, in(p), false); !strings.Contains(s, `"deny"`) {
			t.Errorf("%s allowed", p)
		}
	}
	for _, p := range []string{".claude/skills/x/SKILL.md", ".claude/agents/reviewer.md", "CLAUDE.md"} {
		if s := guardSays(t, in(p), false); s != "" {
			t.Errorf("%s denied: %s", p, s)
		}
	}
	os.Symlink(filepath.Join(t.TempDir(), "nothing-yet", "f"), filepath.Join(dir, "dangling"))
	if s := guardSays(t, in(filepath.Join(dir, "dangling")), false); !strings.Contains(s, `"deny"`) {
		t.Error("a dangling link out of the folder is allowed")
	}
	if s := guardSays(t, map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "/etc/x"}}, false); !strings.Contains(s, `"deny"`) {
		t.Error("no cwd: allowed")
	}
	if s := guardSays(t, map[string]any{"cwd": dir, "tool_name": "Edit", "tool_input": map[string]any{}}, false); !strings.Contains(s, `"deny"`) {
		t.Error("no path: allowed")
	}
}

// A turn is one `claude -p`: what runs in the background dies with it, so the
// administrator's runs may not start it (nor monitors or wakeups).
func TestForeground(t *testing.T) {
	say := func(in map[string]any) string {
		b, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": in})
		var out bytes.Buffer
		Foreground(bytes.NewReader(b), &out)
		return out.String()
	}
	if s := say(map[string]any{"command": "make test", "run_in_background": true}); !strings.Contains(s, `"deny"`) {
		t.Fatalf("background allowed: %q", s)
	}
	for _, in := range []map[string]any{{"command": "make test"}, {"command": "make test", "run_in_background": false}} {
		if s := say(in); s != "" {
			t.Fatalf("foreground denied: %v %q", in, s)
		}
	}
	var r claudeRunner
	old := ForegroundCommand
	defer func() { ForegroundCommand = old }()
	ForegroundCommand = "'/x/office' hook foreground"
	a := r.args(RunRequest{FullAccess: true, NoSubagents: true}, false)
	if d := a[slices.Index(a, "--disallowedTools")+1]; !strings.Contains(d, "Monitor") || !strings.Contains(d, "ScheduleWakeup") || !strings.Contains(d, "Agent Task") {
		t.Fatalf("disallowed: %s", d)
	}
	if i := slices.Index(a, "--settings"); i < 0 || !strings.Contains(a[i+1], "hook foreground") || !strings.Contains(a[i+1], "Bash|Agent") {
		t.Fatalf("foreground hook missing: %v", a)
	}
}

func TestPushesProtected(t *testing.T) {
	t.Parallel()
	on := func(branch, up string) func(string) (string, string) {
		return func(string) (string, string) { return branch, up }
	}
	feature, main := on("feat/x", "origin/feat/x"), on("main", "origin/main")
	for _, c := range []struct {
		cmd    string
		branch func(string) (string, string)
		want   bool
	}{
		{"git push", feature, false},
		{"git push", main, true},
		{"git push", on("feat/x", "origin/master"), true},
		{"git push origin feat/x", main, false},
		{"git push origin main", feature, true},
		{"git push origin HEAD:master", feature, true},
		{"git push -f origin +refs/heads/main", feature, true},
		{"git push origin HEAD", main, true},
		{"git push --all origin", feature, true},
		{"go test ./... && git -C ../repo push", main, true},
		{"git status && git log", main, false},
		{"echo git push", main, false},
	} {
		if got := pushesProtected(c.cmd, "/repo", c.branch); got != c.want {
			t.Errorf("%q: %v, want %v", c.cmd, got, c.want)
		}
	}
}
