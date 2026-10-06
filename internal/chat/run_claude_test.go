package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	os.WriteFile(bin, []byte("#!/bin/sh\ncat >/dev/null\ncat "+fixture+"\n"), 0o755)
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

// After a switch the new agent must know which answers were another agent's.
func TestTranscriptNamesOtherAgents(t *testing.T) {
	h := []HistoryItem{
		{Role: "user", Content: "kiểm tra graylog"},
		{Role: "assistant", Content: "mình chỉ đọc", Author: "Trưởng nhóm"},
		{Role: "assistant", Content: "đã xem"}, // this agent's own
	}
	got := transcript(h, "sửa đi")
	if !strings.Contains(got, "[Trưởng nhóm]\nmình chỉ đọc") || !strings.Contains(got, "[Bạn]\nđã xem") || strings.Contains(got, "[Bạn]\nmình chỉ đọc") {
		t.Fatalf("transcript:\n%s", got)
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

// Extra directories (an agent's own, or an automation's override — ADR-074)
// become --add-dir flags of the run.
func TestClaudeArgsExtraDirs(t *testing.T) {
	dir := t.TempDir()
	bin, argsLog := filepath.Join(dir, "claude"), filepath.Join(dir, "args")
	os.WriteFile(bin, []byte(`#!/bin/sh
printf '%s\n' "$@" >`+argsLog+`
cat >/dev/null
echo '{"type":"result","subtype":"success","result":"ok"}'
`), 0o755)
	extra := t.TempDir()
	req := RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi", ExtraDirs: []string{extra, " ", ""}}
	var r claudeRunner
	if _, err := r.Run(context.Background(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(argsLog)
	got := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	i := slices.Index(got, "--add-dir")
	if i < 0 || i+1 >= len(got) || got[i+1] != extra {
		t.Fatalf("--add-dir missing for extra dirs: %v", got)
	}
	n := 0
	for _, a := range got {
		if a == "--add-dir" {
			n++
		}
	}
	if n != 1 { // blank entries are skipped
		t.Fatalf("blank extra dirs were not skipped: %v", got)
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
