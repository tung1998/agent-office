package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/usage"
)

func init() { TransientRetryDelay = 20 * time.Millisecond } // every test of the package

// The errors runs died on (run.log, failure_digest): which pass by waiting.
func TestTransient(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{errors.New("claude: exit status 1 · lỗi API cuối: 529 overloaded_error, lần 10/10 · sau 17m3s"), true},
		{errors.New("claude: API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}"), true},
		{errors.New("claude: API Error: 500 {\"type\":\"api_error\",\"message\":\"Internal server error\"}"), true},
		{errors.New("codex: stream disconnected before completion: error sending request"), true},
		{errors.New("codex: unexpected status 503 Service Unavailable"), true},
		{errors.New("gemini: read ECONNRESET"), true},
		{transientError{"claude: x"}, true},
		{fmt.Errorf("wrapped: %w", ErrTransient), true},
		{errors.New("claude: exit status 1"), false},
		{errors.New("claude: You've hit your limit · resets 3pm"), false},
		{errors.New("API Error: 429 rate_limit_error overloaded"), false}, // quota first
		{fmt.Errorf("%w: 503 Service Unavailable", llm.ErrNeedsLogin), false},
		{fmt.Errorf("%w: overloaded", llm.ErrCLIBroken), false},
		{&usage.BudgetError{}, false},
		{context.Canceled, false},
		{errors.New("go test: sau 500ms"), false},
		{nil, false},
	} {
		if got := Transient(c.err); got != c.want {
			t.Errorf("Transient(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// A CLI that gave up on an overloaded API mid-turn goes on in its session a
// while later, told so; the turn ends as one answer (ADR-260).
func TestClaudeGoesOnAfterTransientExit(t *testing.T) {
	dir := t.TempDir()
	bin, calls := filepath.Join(dir, "claude"), filepath.Join(dir, "calls")
	fakebin.Write(bin, []byte(`#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
echo "$*" >> `+calls+`
cat > `+dir+`/stdin
echo '{"type":"system","subtype":"init","session_id":"s1"}'
case "$*" in
*"--resume s1"*) echo '{"type":"result","subtype":"success","is_error":false,"result":"xong","session_id":"s1","usage":{"input_tokens":3,"output_tokens":2}}' ;;
*) echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/x/a.go"}}]}}'
   echo '{"type":"system","subtype":"api_retry","attempt":10,"max_retries":10,"error_status":529,"error":"overloaded_error"}'
   exit 1 ;;
esac
`))
	res, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "sửa lỗi"}, func(Event) {})
	if err != nil || res.Text != "xong" || len(res.Tools) != 1 || !strings.Contains(res.Notice, "tạm thời") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	b, _ := os.ReadFile(calls)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || strings.Contains(lines[0], "--resume") || !strings.Contains(lines[1], "--resume s1") {
		t.Fatalf("calls: %q", lines)
	}
	in, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if !strings.Contains(string(in), "529") || !strings.Contains(string(in), "sửa lỗi") {
		t.Fatalf("second prompt: %s", in)
	}
}

// Only once: still failing, the turn fails as transient (to be put back
// later); a CLI out of quota or with no session is never run again.
func TestClaudeGoesOnOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	bin, calls := filepath.Join(dir, "claude"), filepath.Join(dir, "calls")
	write := func(body string) {
		os.Remove(calls)
		fakebin.Write(bin, []byte(`#!/bin/sh
if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
echo "$*" >> `+calls+`
cat >/dev/null
`+body))
	}
	count := func() int {
		b, _ := os.ReadFile(calls)
		return len(strings.Split(strings.TrimSpace(string(b)), "\n"))
	}
	write(`echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"system","subtype":"api_retry","attempt":10,"max_retries":10,"error_status":529,"error":"overloaded_error"}'
exit 1
`)
	_, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "x"}, func(Event) {})
	if !errors.Is(err, ErrTransient) || count() != 2 {
		t.Fatalf("err=%v calls=%d", err, count())
	}
	write(`echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"You have hit your usage limit","session_id":"s1"}'
exit 1
`)
	if _, err := (claudeRunner{}).Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "x"}, func(Event) {}); err == nil || count() != 1 {
		t.Fatalf("quota: err=%v calls=%d", err, count())
	}
	write(`echo '{"type":"system","subtype":"api_retry","attempt":10,"max_retries":10,"error_status":529,"error":"overloaded_error"}'
exit 1
`)
	if _, err := (claudeRunner{}).Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "x"}, func(Event) {}); err == nil || count() != 1 {
		t.Fatalf("no session: err=%v calls=%d", err, count())
	}
}
