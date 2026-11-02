package chat

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/storage"
)

// The user's own MCP servers (their Claude Code setup) are allowed only when
// the agent has the capability: a PreToolUse hook allows mcp__* under dontAsk.
func TestClaudeArgsUserMCP(t *testing.T) {
	var r claudeRunner
	off := r.args(RunRequest{}, false)
	if slices.Contains(off, "--settings") {
		t.Fatalf("no capability, no hook: %v", off)
	}
	on := r.args(RunRequest{UserMCP: true}, false)
	i := slices.Index(on, "--settings")
	if i < 0 || i+1 >= len(on) {
		t.Fatalf("hook missing: %v", on)
	}
	var s struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string
				Hooks   []struct{ Type, Command string }
			}
		}
	}
	if err := json.Unmarshal([]byte(on[i+1]), &s); err != nil || len(s.Hooks.PreToolUse) != 1 {
		t.Fatalf("settings are not valid JSON: %v %s", err, on[i+1])
	}
	h := s.Hooks.PreToolUse[0]
	if h.Matcher != "mcp__.*" || len(h.Hooks) != 1 || !strings.Contains(h.Hooks[0].Command, `"permissionDecision":"allow"`) {
		t.Fatalf("hook = %+v", h)
	}
	if !slices.Contains(on, "dontAsk") {
		t.Fatal("everything else stays denied")
	}
}

// Claude Code reports the subscription's usage windows and each call's
// context; office keeps the latest (shown next to the chat and in the sidebar).
func TestClaudeReportsLimitsAndContext(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.19,"resetsAt":1790593200},"seven_day":{"utilization":0.18,"resetsAt":1790902800},"seven_day_fable":{"utilization":0.04,"resetsAt":1790902800}}}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_read_input_tokens":20000,"cache_creation_input_tokens":5000,"output_tokens":7},"content":[]}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5-5","usage":{"input_tokens":12,"cache_read_input_tokens":30000,"cache_creation_input_tokens":1000,"output_tokens":9},"content":[]}}`,
		`{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":22,"output_tokens":16},"modelUsage":{"claude-haiku-4-5":{"contextWindow":200000},"claude-opus-5-5":{"contextWindow":1000000}}}`,
	}
	fixture := filepath.Join(dir, "out.jsonl")
	os.WriteFile(fixture, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	bin := filepath.Join(dir, "claude")
	fakebin.Write(bin, []byte("#!/bin/sh\ncat >/dev/null\ncat "+fixture+"\n"))
	res, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Limits == nil || res.Limits.Status != "allowed" || len(res.Limits.Windows) != 3 {
		t.Fatalf("limits = %+v", res.Limits)
	}
	w := res.Limits.Windows["five_hour"]
	if w.Utilization != 0.19 || !w.ResetsAt.Equal(time.Unix(1790593200, 0)) {
		t.Fatalf("five_hour = %+v", w)
	}
	// context = the last call's input (with cache), in the main model's window
	if res.Context.Tokens != 31012 || res.Context.Window != 1000000 {
		t.Fatalf("context = %+v", res.Context)
	}
}

// Full access (the office assistant as administrator): every tool, no asking.
func TestClaudeArgsFullAccess(t *testing.T) {
	var r claudeRunner
	a := r.args(RunRequest{FullAccess: true}, false)
	i := slices.Index(a, "--permission-mode")
	if i < 0 || a[i+1] != "bypassPermissions" || slices.Contains(a, "--allowedTools") {
		t.Fatalf("full access args = %v", a)
	}
	if b := r.args(RunRequest{}, false); b[slices.Index(b, "--permission-mode")+1] != "dontAsk" {
		t.Fatalf("default args = %v", b)
	}
}

// Without full access, an extra dir is read only: Edit/Write there are denied
// even though --add-dir lets the run read it (ADR-074 security fix). The rule
// needs a SECOND leading "/" for an absolute path — a single "/" is relative
// to the settings source (the working directory), not the filesystem root
// (https://code.claude.com/docs/en/permissions).
func TestClaudeArgsExtraDirsDeniesWriteWithoutFullAccess(t *testing.T) {
	var r claudeRunner
	extra := "/tmp/an-extra-dir"
	a := r.args(RunRequest{Write: true, ExtraDirs: []string{extra}}, false)
	i := slices.Index(a, "--disallowedTools")
	if i < 0 || i+1 >= len(a) {
		t.Fatalf("no --disallowedTools: %v", a)
	}
	deny := a[i+1]
	if !strings.Contains(deny, "Edit(/"+extra+"/**)") || !strings.Contains(deny, "Write(/"+extra+"/**)") {
		t.Fatalf("extra dir not denied with the absolute-path (double leading /) syntax: %q", deny)
	}
	// the single-leading-slash form (the old, wrong rule) must not be there
	// instead — it would only deny a path relative to the working directory
	if strings.Contains(deny, "Edit("+extra+"/**)") {
		t.Fatalf("extra dir denied with the wrong (relative) syntax: %q", deny)
	}
}

// A JSON line over the scanner's 16MB cap must fail the run instead of
// silently returning an empty "success" (the "result" event never gets read).
func TestClaudeFailsOnOversizedLine(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	// nothing is written after the oversized line: once the scanner gives up
	// reading it, nobody drains the pipe, so a script that kept writing (e.g.
	// the real "result" event) would block forever on a full pipe and hang
	// cmd.Wait() — not what this test is checking.
	script := `#!/bin/sh
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
python3 -c 'print("{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"" + "x"*(17*1024*1024) + "\"}]}}")'
`
	fakebin.Write(bin, []byte(script))
	res, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err == nil {
		t.Fatalf("expected an error for an oversized line, got res = %+v", res)
	}
	if res.Text != "" {
		t.Fatalf("result must not be read past the oversized line: res = %+v", res)
	}
}

// A subagent Claude Code starts on its own (Task): its steps show under it.
func TestClaudeShowsItsSubagents(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tk1","name":"Task","input":{"description":"tìm chỗ gọi API","subagent_type":"Explore"}}]}}`,
		`{"type":"assistant","parent_tool_use_id":"tk1","message":{"content":[{"type":"tool_use","id":"g1","name":"Grep","input":{"pattern":"fetch("}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"a.go"}}]}}`,
		`{"type":"result","subtype":"success","result":"ok","usage":{"input_tokens":1,"output_tokens":1}}`,
	}
	fixture := filepath.Join(dir, "out.jsonl")
	os.WriteFile(fixture, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	bin := filepath.Join(dir, "claude")
	fakebin.Write(bin, []byte("#!/bin/sh\ncat >/dev/null\ncat "+fixture+"\n"))
	res, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 3 || res.Tools[0].Summary != "Giao subagent Explore: tìm chỗ gọi API" ||
		res.Tools[1].Summary != `↳ Explore · tìm chỗ gọi API: Tìm "fetch("` || res.Tools[2].Summary != "Đọc a.go" {
		t.Fatalf("tools = %+v", res.Tools)
	}
}

// The hook runs through sh: a quote in its text breaks the command, sh exits
// 2, and Claude Code then blocks every MCP tool call.
func TestUserMCPSettingsHookRuns(t *testing.T) {
	var s struct {
		Hooks struct {
			PreToolUse []struct {
				Hooks []struct{ Command string } `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(userMCPSettings), &s); err != nil {
		t.Fatal(err)
	}
	cmd := s.Hooks.PreToolUse[0].Hooks[0].Command
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		t.Fatalf("hook %q: %v", cmd, err)
	}
	var v struct {
		HookSpecificOutput struct{ PermissionDecision string } `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &v); err != nil || v.HookSpecificOutput.PermissionDecision != "allow" {
		t.Fatalf("hook output %q: %v", out, err)
	}
}

// Office's MCP servers are allowed in the whole-name form Claude Code 2.1.299
// matches: mcp__<server>__*, one rule per server.
func TestOfficeAllow(t *testing.T) {
	got := officeAllow(&OfficeAccess{Gateway: []string{"github", "my-db"}})
	if got != " mcp__office__* mcp__github__* mcp__my-db__*" {
		t.Fatalf("allow rules: %q", got)
	}
}

// Haiku before 5 holds 200K; Haiku 5.5 goes by what its last run said.
func TestWindowOfHaiku(t *testing.T) {
	if w := windowOf("claude-haiku-4-5", 0); w != 200_000 {
		t.Fatalf("haiku 4.5: %d", w)
	}
	if w := windowOf("claude-haiku-5-5", 1_000_000); w != 1_000_000 {
		t.Fatalf("haiku 5.5: %d", w)
	}
}

// A connection signed in to an account of its own runs in its config folder (ADR-178).
func TestClaudeRunsInTheConnectionsAccount(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
cat >/dev/null
echo '{"type":"result","subtype":"success","result":"'"$CLAUDE_CONFIG_DIR"'","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`
	fakebin.Write(bin, []byte(script))
	home := filepath.Join(dir, "prv_2")
	p := storage.Provider{Kind: storage.ProviderClaudeCLI, CLIHome: home}
	res, err := claudeRunner{}.Run(context.Background(), RunRequest{Provider: p, Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err != nil || res.Text != home {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if cliEnv(storage.Provider{Kind: storage.ProviderClaudeCLI}) != nil {
		t.Fatal("the machine's account must keep the office's environment")
	}
}

// ADR-192: web search and fetch only for an agent that picked tools.web.
func TestClaudeArgsWeb(t *testing.T) {
	var r claudeRunner
	toolsOf := func(a []string) string { return a[slices.Index(a, "--tools")+1] }
	if off := r.args(RunRequest{}, false); strings.Contains(toolsOf(off), "Web") {
		t.Fatalf("web without the capability: %v", toolsOf(off))
	}
	on := r.args(RunRequest{Web: true, Write: true}, false)
	if !strings.Contains(toolsOf(on), "WebSearch") || !strings.Contains(toolsOf(on), "WebFetch") {
		t.Fatalf("web missing: %v", toolsOf(on))
	}
}

// A turn thinking above medium runs Claude's subagents at medium, and
// "ultrathink" is off; the person's own settings win.
func TestClaudeEnv(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SUBAGENT_EFFORT", "")
	t.Setenv("CLAUDE_CODE_DISABLE_ULTRATHINK", "") // restored after the test
	os.Unsetenv("CLAUDE_CODE_DISABLE_ULTRATHINK")
	for effort, want := range map[string]string{"": "", "low": "", "medium": "", "high": "medium", "max": "medium"} {
		if got := subagentEffort(effort); got != want {
			t.Errorf("subagentEffort(%q) = %q, want %q", effort, got, want)
		}
	}
	low := claudeEnv(RunRequest{Effort: "low"})
	if !slices.Contains(low, "CLAUDE_CODE_DISABLE_ULTRATHINK=1") || slices.Contains(low, "CLAUDE_CODE_SUBAGENT_EFFORT=medium") {
		t.Fatalf("low: ultrathink off, subagents as is")
	}
	if env := claudeEnv(RunRequest{Effort: "xhigh"}); !slices.Contains(env, "CLAUDE_CODE_SUBAGENT_EFFORT=medium") {
		t.Fatalf("xhigh: subagent effort missing")
	}
	t.Setenv("CLAUDE_CODE_SUBAGENT_EFFORT", "high")
	t.Setenv("CLAUDE_CODE_DISABLE_ULTRATHINK", "0")
	if env := claudeEnv(RunRequest{Effort: "max"}); env != nil {
		t.Fatalf("the person's own settings must win: %v", env)
	}
}
