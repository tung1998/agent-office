package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/fakebin"
)

func TestAntigravityRunner(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bin, log := filepath.Join(tmp, "agy"), filepath.Join(tmp, "log")
	fakebin.Write(bin, []byte(`#!/bin/sh
in=$(cat)
echo "ARGS $*" >> `+log+`
echo "STDIN $in" >> `+log+`
case "$*" in *"--conversation gone"*)
  echo '{"event":"result","result":{"conversation_id":"","status":"ERROR","response":"","error":"conversation not found"}}'; exit 1;; esac
echo '{"event":"init","init":{"cwd":"/w","tools":[],"permission_mode":"default","model":"gemini-3.8-flash-high"}}'
echo '{"event":"step_update","step_update":{"conversation_id":"c1","step_index":0,"state":"DONE","step_type":"user_input"}}'
echo '{"event":"step_update","step_update":{"conversation_id":"c1","step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"Xin "}}'
echo '{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"ACTIVE","step_type":"tool","tool_name":"read_file","tool_info":{"file_path":"a.go"}}}'
echo '{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"DONE","step_type":"tool","tool_name":"read_file"}}'
echo '{"event":"step_update","step_update":{"conversation_id":"c1","step_index":3,"state":"ACTIVE","step_type":"agent_response","text_delta":"chào"}}'
echo '{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"Xin chào","num_turns":1,"usage":{"input_tokens":10,"output_tokens":4,"thinking_tokens":2,"cache_read_tokens":5}}}'
`))
	hist := []HistoryItem{{Role: "user", Content: "câu cũ"}, {Role: "assistant", Content: "trả lời cũ"}}
	req := RunRequest{Bin: bin, System: "HƯỚNG DẪN", History: hist, Prompt: "mới", WorkDir: tmp, Effort: "high", ExtraDirs: []string{"/x"}}
	var text strings.Builder
	res, err := antigravityRunner{}.Run(context.Background(), req, func(e Event) {
		if e.Type == "text" {
			text.WriteString(e.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Xin chào" || res.Text != "Xin chào" || res.SessionID != "c1" || res.Usage.Model != "gemini-3.8-flash-high" ||
		res.Usage.InputTokens != 15 || res.Usage.OutputTokens != 6 {
		t.Fatalf("text=%q res=%+v", text.String(), res)
	}
	if len(res.Tools) != 1 || res.Tools[0].Summary != "Đọc a.go" {
		t.Fatalf("tools = %+v", res.Tools)
	}
	raw, _ := os.ReadFile(log)
	got := string(raw)
	for _, want := range []string{"--mode plan", "--effort high", "--add-dir /x", "HƯỚNG DẪN", "câu cũ"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}

	// going on in the conversation, allowed to edit: only the new message
	os.Remove(log)
	req.SessionID, req.Write = "c1", true
	if _, err := (antigravityRunner{}).Run(context.Background(), req, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(log)
	if got = string(raw); !strings.Contains(got, "--conversation c1") || !strings.Contains(got, "--mode accept-edits") || strings.Contains(got, "HƯỚNG DẪN") {
		t.Fatalf("resume call:\n%s", got)
	}

	// a conversation agy lost: start over with the transcript
	os.Remove(log)
	req.SessionID = "gone"
	if res, err = (antigravityRunner{}).Run(context.Background(), req, func(Event) {}); err != nil || res.SessionID != "c1" {
		t.Fatalf("lost: %+v %v", res, err)
	}
	raw, _ = os.ReadFile(log)
	if got = string(raw); strings.Count(got, "ARGS") != 2 || !strings.Contains(got, "HƯỚNG DẪN") {
		t.Fatalf("fallback calls:\n%s", got)
	}
}

func TestAntigravityRunnerSignedOut(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "agy")
	fakebin.Write(bin, []byte(`#!/bin/sh
cat >/dev/null
echo "Error: authentication required. Run 'agy' to log in, then retry." >&2
echo '{"event":"result","result":{"conversation_id":"","status":"ERROR","response":"","error":"authentication failed or timed out"}}'
`))
	_, err := antigravityRunner{}.Run(context.Background(), RunRequest{Bin: bin, Prompt: "x", WorkDir: tmp}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "chưa đăng nhập") {
		t.Fatalf("err = %v", err)
	}
}

// A command agy denied (read-only, no one to approve) ends the turn with no
// answer: the model was told there is no shell, the retry says so again, and
// the error names the command and why instead of a bare "tool lỗi".
func TestAntigravityDeniedCommand(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bin, log := filepath.Join(tmp, "agy"), filepath.Join(tmp, "log")
	fakebin.Write(bin, []byte(`#!/bin/sh
cat >> `+log+`
echo '{"event":"step_update","step_update":{"conversation_id":"c1","step_index":2,"state":"ERROR","step_type":"tool","tool_name":"run_command","tool_info":{"name":"run_command","parameters":{"CommandLine":"git status"},"error":{"type":"TOOL_ERROR","message":"permission check failed"}}}}'
echo '{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":""}}'
`))
	_, err := antigravityRunner{}.Run(context.Background(), RunRequest{Bin: bin, System: "S", Prompt: "x", WorkDir: tmp}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), `run_command "git status": permission check failed`) {
		t.Fatalf("err = %v", err)
	}
	raw, _ := os.ReadFile(log)
	if got := string(raw); strings.Count(got, agyNoShell) != 2 {
		t.Fatalf("both calls must say there is no shell:\n%s", got)
	}
}

// A JSON line over the scanner's 16MB cap must fail the run instead of
// silently returning an empty "success" (the "result" event never gets read).
func TestAntigravityFailsOnOversizedLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "agy")
	script := `#!/bin/sh
cat >/dev/null
echo '{"event":"init","init":{"model":"m"}}'
python3 -c 'print("{\"event\":\"step_update\",\"step_update\":{\"conversation_id\":\"c1\",\"step_index\":0,\"state\":\"ACTIVE\",\"step_type\":\"agent_response\",\"text_delta\":\"" + "x"*(17*1024*1024) + "\"}}")'
`
	fakebin.Write(bin, []byte(script))
	res, err := antigravityRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err == nil {
		t.Fatalf("expected an error for an oversized line, got res = %+v", res)
	}
	if res.Text != "" {
		t.Fatalf("result must not be read past the oversized line: res = %+v", res)
	}
}

// A read-only agy in a linked worktree may read that worktree's git: its
// .git names <repo>/.git/worktrees/<name>, outside the workspace, and agy
// refused the read ("permission check failed for read_file
// .git/worktrees/burn-…/HEAD"), ending its turn with no answer. An agent
// that may write is not given .git (its hooks run code) (ADR-270).
func TestAgyAddsGitCommonDir(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	repo, wt := filepath.Join(tmp, "repo"), filepath.Join(tmp, "wt")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b.c", "-c", "user.name=a"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	os.MkdirAll(repo, 0o755)
	git(repo, "init", "-q")
	os.WriteFile(filepath.Join(repo, "a"), []byte("a"), 0o644)
	git(repo, "add", "a")
	git(repo, "commit", "-qm", "a")
	git(repo, "worktree", "add", "-q", wt)

	dirs := worktreeGitDirs(context.Background(), wt)
	if len(dirs) != 2 || !strings.HasSuffix(dirs[0], filepath.Join(".git", "worktrees", "wt")) || !strings.HasSuffix(dirs[1], ".git") {
		t.Fatalf("worktree dirs = %q", dirs)
	}
	if d := worktreeGitDirs(context.Background(), repo); len(d) != 0 {
		t.Fatalf("the main checkout's .git is in its workspace: %q", d)
	}
	if d := worktreeGitDirs(context.Background(), tmp); len(d) != 0 {
		t.Fatalf("not in git: %q", d)
	}

	bin, log := filepath.Join(tmp, "agy"), filepath.Join(tmp, "log")
	fakebin.Write(bin, []byte(`#!/bin/sh
cat >/dev/null
echo "ARGS $*" >> `+log+`
echo '{"event":"result","result":{"conversation_id":"c1","status":"SUCCESS","response":"ok","num_turns":1}}'
`))
	run := func(write bool) string {
		os.Remove(log)
		if _, err := (antigravityRunner{}).Run(context.Background(), RunRequest{Bin: bin, Prompt: "x", WorkDir: wt, Write: write}, func(Event) {}); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(log)
		return string(raw)
	}
	if got := run(false); !strings.Contains(got, "--add-dir "+dirs[1]) || !strings.Contains(got, "--add-dir "+dirs[0]) {
		t.Fatalf("read only, args: %s", got)
	}
	if got := run(true); strings.Contains(got, ".git") {
		t.Fatalf("may write, given .git: %s", got)
	}
}
