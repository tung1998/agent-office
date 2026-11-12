package chat

import (
	"context"
	"encoding/json"
	"errors"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	got := officeAllow(&OfficeAccess{Gateway: []string{"github", "my-db"}})
	if got != " mcp__office__* mcp__github__* mcp__my-db__*" {
		t.Fatalf("allow rules: %q", got)
	}
}

// Haiku before 5 holds 200K; Haiku 5.5 goes by what its last run said.
func TestWindowOfHaiku(t *testing.T) {
	t.Parallel()
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
	t.Setenv("OFFICE_TEST_KEEP", "1")
	t.Setenv("GH_TOKEN", "x")
	env := cliEnv(storage.Provider{Kind: storage.ProviderClaudeCLI})
	if !slices.Contains(env, "OFFICE_TEST_KEEP=1") {
		t.Fatal("the machine's account must keep the office's environment")
	}
	if slices.Contains(env, "GH_TOKEN=x") {
		t.Fatal("a git host token reached the agent (ADR-271)")
	}
}

// ADR-192: web search and fetch only for an agent that picked tools.web.
func TestClaudeArgsWeb(t *testing.T) {
	t.Parallel()
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
	if env := claudeEnv(RunRequest{Effort: "max"}); slices.Contains(env, "CLAUDE_CODE_SUBAGENT_EFFORT=medium") || slices.Contains(env, "CLAUDE_CODE_DISABLE_ULTRATHINK=1") {
		t.Fatalf("the person's own settings must win: %v", env)
	}
}

// The CLI's stderr is read while it still writes to it (a result with
// is_error): run with -race, a plain bytes.Buffer is a data race (ADR-258).
func TestClaudeStderrNoRace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
cat >/dev/null
( i=0; while [ $i -lt 400 ]; do echo "warn $i" >&2; i=$((i+1)); done ) &
echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom","usage":{"input_tokens":1}}'
wait
`
	fakebin.Write(bin, []byte(script))
	for range 5 {
		_, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v", err)
		}
	}
}

// A CLI that exits 1 with no result and nothing on stderr: what it printed on
// stdout is the reason, not a bare "exit status 1" (ADR-258).
func TestClaudeExitKeepsStdoutTail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo 'Error: the plugin marketplace could not be read'
exit 1
`
	fakebin.Write(bin, []byte(script))
	_, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "plugin marketplace could not be read") || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v", err)
	}
}

// A CLI that exits 1 with no result, nothing on stderr and only events on
// stdout (a long run that dies after the API kept failing): the API error it
// last retried on and the step it was at are the reason, not a bare "exit
// status 1" (the daily upgrade of 2026-11-06 died so, 17 minutes in).
func TestClaudeExitSaysWhatItWasDoing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}'
echo '{"type":"system","subtype":"api_retry","attempt":3,"max_retries":10,"retry_delay_ms":4000,"error_status":529,"error":"overloaded_error"}'
echo '{"type":"system","subtype":"api_retry","attempt":10,"max_retries":10,"retry_delay_ms":4000,"error_status":529,"error":"overloaded_error"}'
exit 1
`
	fakebin.Write(bin, []byte(script))
	_, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "529") || !strings.Contains(err.Error(), "overloaded_error") ||
		!strings.Contains(err.Error(), "10/10") || !strings.Contains(err.Error(), "go test") || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v", err)
	}

	// no retry, only a step: the step is said
	script = `#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/x/main.go"}}]}}'
exit 1
`
	fakebin.Write(bin, []byte(script))
	_, err = claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "main.go") || strings.Contains(err.Error(), "API") {
		t.Fatalf("err = %v", err)
	}
}

func TestLineTailKeepsTheLast(t *testing.T) {
	t.Parallel()
	var tl lineTail
	for i := range 30 {
		tl.add(strings.Repeat("x", i%2) + " line " + string(rune('a'+i%26)))
	}
	tl.add("   ")
	if len(tl.lines) != tailLines || !strings.HasSuffix(tl.String(), "line d") {
		t.Fatalf("tail = %q", tl.String())
	}
}

// The CLI wraps an API failure in an assistant message of model "<synthetic>"
// and ends with a result whose api_error_status is a number: both used to
// fail to decode (api_error_status read as a string), fell to the plain tail
// and the error was raw JSON (16 failures of 2026-11-07/08). The message's
// own words are the reason.
func TestClaudeSyntheticMessageReadsItsText(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := `#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"assistant","message":{"id":"m1","model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence","type":"message","usage":{"output_tokens_details":null,"input_tokens":0,"output_tokens":0},"content":[{"type":"text","text":"API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}"}]},"is_api_error_message":true,"api_error_status":529,"error":"server_error","session_id":"s1"}'
echo '{"duration_api_ms":0,"stop_reason":"stop_sequence","session_id":"s1","total_cost_usd":0,"usage":{"output_tokens_details":{"thinking_tokens":0},"input_tokens":0,"output_tokens":0},"type":"result","subtype":"success","is_error":true,"api_error_status":529,"result":"API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}"}'
exit 1
`
	fakebin.Write(bin, []byte(script))
	_, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err == nil || strings.Contains(err.Error(), "<synthetic>") || strings.Contains(err.Error(), "duration_api_ms") || strings.Contains(err.Error(), "success") ||
		!strings.Contains(err.Error(), "Overloaded") || !strings.Contains(err.Error(), "529") {
		t.Fatalf("err = %v", err)
	}
	if !Transient(err) {
		t.Fatalf("a 529 overloaded must read as passing: %v", err)
	}

	// no result line: the synthetic message alone is the reason
	script = `#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
cat >/dev/null
echo '{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"You'"'"'ve hit your limit · resets 3pm"}]},"api_error_status":429,"error":"rate_limit"}'
exit 1
`
	fakebin.Write(bin, []byte(script))
	_, err = claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err == nil || strings.Contains(err.Error(), "<synthetic>") || !strings.Contains(err.Error(), "hit your limit") || !OutOfTokens(err) {
		t.Fatalf("err = %v", err)
	}
}

// Claude's own limit message says when it resets ("resets 3pm", "resets Nov
// 13, 10:30am (Asia/Saigon)"): the connection is held until then, not for the
// 5 minutes of a report without a time.
func TestClaudeSyntheticLimitSetsLimits(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("ICT", 7*3600)
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, loc)
	cases := []struct {
		msg  string
		want time.Time
	}{
		{"claude: You've hit your limit · resets 3pm", time.Date(2026, 11, 8, 15, 0, 0, 0, loc)},
		{"claude: You've hit your limit · resets 9am", time.Date(2026, 11, 9, 9, 0, 0, 0, loc)},
		{"claude: You've hit your limit · resets 3:30pm (Asia/Saigon)", time.Date(2026, 11, 8, 15, 30, 0, 0, time.FixedZone("ICT", 7*3600))},
		{"claude: You've hit your weekly limit · resets Nov 13, 10am", time.Date(2026, 11, 13, 10, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		l := limitsFromError(nil, errors.New(c.msg), now)
		if l == nil || l.Status != "rejected" || !l.Windows["quota"].ResetsAt.Equal(c.want) {
			t.Fatalf("%q: limits = %+v, want reset %v", c.msg, l, c.want)
		}
		if !overNow(*l, now) {
			t.Fatalf("%q: not over quota now", c.msg)
		}
	}
}

// A compaction whose CLI exits 1 with nothing on stderr: what it printed
// (plain or its own synthetic message) is the reason, not "exit status 1".
func TestCompactClaudeKeepsStdoutReason(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	fakebin.Write(bin, []byte(`#!/bin/sh
echo 'Error: No conversation found with session ID: s1'
exit 1
`))
	_, err := compactClaude(context.Background(), bin, nil, dir, "", "s1")
	if err == nil || !strings.Contains(err.Error(), "No conversation found") || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("err = %v", err)
	}

	fakebin.Write(bin, []byte(`#!/bin/sh
echo '{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"Prompt is too long"}]},"api_error_status":400}'
echo '{"type":"result","subtype":"success","is_error":true,"api_error_status":400,"result":""}'
exit 1
`))
	_, err = compactClaude(context.Background(), bin, nil, dir, "", "s1")
	if err == nil || !strings.Contains(err.Error(), "Prompt is too long") || strings.Contains(err.Error(), "success") {
		t.Fatalf("err = %v", err)
	}
}
