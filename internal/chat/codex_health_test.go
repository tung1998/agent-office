package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// The text Codex printed on 2026-11-05 for 3 failed turns: its reset is a
// clock time, which used to fall back to a 5-minute cooldown, so each new
// turn hit the limit again.
const codexQuota = "codex: You’ve hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/settings/usage to purchase more credits or try again at 9:18 PM."

func TestCodexHealthClockReset(t *testing.T) {
	zone := time.FixedZone("ICT", 7*3600)
	now := time.Date(2026, 11, 5, 10, 23, 0, 0, zone)
	l := limitsFromError(nil, errors.New(codexQuota), now)
	if l == nil || l.Windows["quota"].ResetsAt != time.Date(2026, 11, 5, 21, 18, 0, 0, zone) {
		t.Fatalf("limits = %+v, want a reset at 21:18 today", l)
	}
	if !overNow(*l, now.Add(time.Hour)) {
		t.Fatal("an hour later the connection must still be over its limit")
	}
	if overNow(*l, time.Date(2026, 11, 5, 21, 19, 0, 0, zone)) {
		t.Fatal("past the reset it must be free again")
	}
	late := time.Date(2026, 11, 5, 22, 0, 0, 0, zone) // 9:18 PM already past: tomorrow's
	if l := limitsFromError(nil, errors.New(codexQuota), late); l.Windows["quota"].ResetsAt != time.Date(2026, 11, 6, 21, 18, 0, 0, zone) {
		t.Fatalf("past clock time = %v, want tomorrow 21:18", l.Windows["quota"].ResetsAt)
	}
	dated := "You've hit your usage limit. ... or try again at Nov 8th, 2026 9:05 AM."
	if l := limitsFromError(nil, errors.New(dated), now); l.Windows["quota"].ResetsAt != time.Date(2026, 11, 8, 9, 5, 0, 0, zone) {
		t.Fatalf("dated reset = %v", l.Windows["quota"].ResetsAt)
	}
	if l := limitsFromError(nil, errors.New("rate limit, try again at 25:99"), now); len(l.Windows) != 0 {
		t.Fatalf("a bad clock must not set a window: %+v", l)
	}
}

// codexBin writes a fake codex that counts its runs in $CODEX_RUNS.
func codexBin(t *testing.T, body string) (bin string, runs func() int) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "codex")
	count := filepath.Join(dir, "runs")
	t.Setenv("CODEX_RUNS", count)
	t.Setenv("CODEX_HOME", filepath.Join(dir, "home")) // a sign-out marks the session refused there, not in ~/.codex
	script := "#!/bin/sh\necho x >> \"$CODEX_RUNS\"\ncat >/dev/null\n" + body
	if err := fakebin.Write(bin, []byte(script)); err != nil {
		t.Fatal(err)
	}
	return bin, func() int {
		b, _ := os.ReadFile(count)
		return strings.Count(string(b), "x")
	}
}

// npm's codex wrapper with its native binary gone: what every openAI turn
// printed from 08:23 on 2026-11-05 (18 failed runs, the connection never
// marked broken, so each turn tried it first again).
const brokenWrapper = `echo "Error: spawn /Users/u/.nvm/versions/node/v25.9.0/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/codex/codex ENOENT" >&2
echo "    at ChildProcess._handle.onexit (node:internal/child_process:285:19)" >&2
exit 1
`

func TestCodexHealthBrokenInstall(t *testing.T) {
	bin, _ := codexBin(t, brokenWrapper)
	_, err := codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "hi"}, func(Event) {})
	if !errors.Is(err, llm.ErrCLIBroken) || !connectionBroken(err) {
		t.Fatalf("err = %v, want ErrCLIBroken", err)
	}
	if !strings.Contains(err.Error(), "npm install -g @openai/codex") || strings.Contains(err.Error(), "ChildProcess") {
		t.Fatalf("err = %q: want the fix, not the stack trace", err)
	}
	// a command that is not there at all
	_, err = codexRunner{}.Run(context.Background(), RunRequest{Bin: filepath.Join(t.TempDir(), "codex"), WorkDir: t.TempDir(), Prompt: "hi"}, func(Event) {})
	if !errors.Is(err, llm.ErrCLIBroken) {
		t.Fatalf("missing binary: err = %v, want ErrCLIBroken", err)
	}
}

// A failed resumed turn ran again afresh whatever the error: a broken CLI,
// a quota or a sign-out failed twice. Only a lost session is worth a retry.
func TestCodexHealthRetriesOnlyALostSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"broken install", brokenWrapper, 1},
		{"out of quota", `echo '{"type":"turn.failed","error":{"message":"You’ve hit your usage limit. try again at 9:18 PM."}}'
exit 1
`, 1},
		{"signed out", `echo '{"type":"turn.failed","error":{"message":"unauthorized (401)"}}'
exit 1
`, 1},
		{"lost session", `echo "Error: thread s1 not found" >&2
exit 1
`, 2},
		{"no rollout", `echo "Error: no rollout found for thread id s1" >&2
exit 1
`, 2},
		// a passing failure: the thread is still there, running afresh would
		// lose its context and pay twice (ADR-254)
		{"503", `echo '{"type":"turn.failed","error":{"message":"unexpected status 503 Service Unavailable: upstream connect error"}}'
exit 1
`, 1},
		{"stream dropped", `echo '{"type":"turn.failed","error":{"message":"stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)"}}'
exit 1
`, 1},
		{"crash", `echo "thread 'main' panicked at core/src/exec.rs:12:5" >&2
exit 101
`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, runs := codexBin(t, tc.body)
			req := RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "hi", SessionID: "s1"}
			if _, err := (codexRunner{}).Run(context.Background(), req, func(Event) {}); err == nil {
				t.Fatal("want an error")
			}
			if got := runs(); got != tc.want {
				t.Fatalf("codex ran %d times, want %d", got, tc.want)
			}
		})
	}
}

// An answer Codex stopped writing (the turn failed after it) passed as a
// whole one: a review cut before its VERDICT read as complete.
func TestCodexHealthCutAnswerSaysSo(t *testing.T) {
	bin, _ := codexBin(t, `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"Half a review"}}'
echo '{"type":"turn.failed","error":{"message":"You have hit your usage limit, try again in 30m"}}'
exit 1
`)
	var streamed strings.Builder
	res, err := codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "hi"}, func(ev Event) { streamed.WriteString(ev.Text) })
	if err != nil {
		t.Fatalf("err = %v: the part written is kept", err)
	}
	if res.Text != "Half a review" || !strings.Contains(res.Notice, "dừng giữa chừng") || !strings.Contains(streamed.String(), "dừng giữa chừng") {
		t.Fatalf("text = %q, notice = %q, streamed = %q", res.Text, res.Notice, streamed.String())
	}
	if c := answerContent(res, true); !strings.HasPrefix(c, "Half a review") || !strings.Contains(c, "dừng giữa chừng") {
		t.Fatalf("a person's chat shows the note under the answer: %q", c)
	}
	if res.Limits == nil || res.Limits.Status != "rejected" || !overNow(*res.Limits, time.Now().Add(20*time.Minute)) {
		t.Fatalf("limits = %+v: the quota it ran out of must hold the connection back", res.Limits)
	}
}

// Codex prints {"type":"error"} for a reconnect it gets over: a turn that
// reconnected, then answered in full, was marked cut short.
func TestCodexReconnectIsNotACut(t *testing.T) {
	bin, _ := codexBin(t, `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"error","message":"Reconnecting... 1/5"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"Full answer\nVERDICT: OK"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":2}}'
`)
	var streamed strings.Builder
	res, err := codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "hi"}, func(ev Event) { streamed.WriteString(ev.Text) })
	if err != nil || res.Text != "Full answer\nVERDICT: OK" || res.Notice != "" || strings.Contains(streamed.String(), "dừng giữa chừng") {
		t.Fatalf("res = %+v err = %v streamed = %q: a reconnect is not a cut", res, err, streamed.String())
	}
}

// A review Codex wrote in full then a turn.failed: the note went into the
// text after "VERDICT: OK", so the workflow read no verdict. The note stays
// out of what a role, a Burn task or a tool reads.
func TestCodexCutNoteKeepsVerdictLast(t *testing.T) {
	bin, _ := codexBin(t, `echo '{"type":"thread.started","thread_id":"s1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"Review\nVERDICT: OK"}}'
echo '{"type":"turn.failed","error":{"message":"stream disconnected before completion"}}'
exit 1
`)
	res, err := codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "hi"}, func(Event) {})
	if err != nil || res.Notice == "" {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	for _, c := range []string{res.Text, answerContent(res, false)} {
		lines := strings.Split(strings.TrimSpace(c), "\n")
		if lines[len(lines)-1] != "VERDICT: OK" {
			t.Fatalf("last line = %q, want the verdict", lines[len(lines)-1])
		}
	}
}

// Once Codex has a thread it runs: an MCP server of its config that cannot
// start ("spawn npx ENOENT") must not mark the connection broken.
func TestCodexMCPSpawnErrorIsNotBroken(t *testing.T) {
	bin, _ := codexBin(t, `echo '{"type":"thread.started","thread_id":"s1"}'
echo "ERROR rmcp: MCP client for docs failed to start: spawn npx ENOENT" >&2
exit 1
`)
	_, err := codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "hi"}, func(Event) {})
	if err == nil || errors.Is(err, llm.ErrCLIBroken) || connectionBroken(err) {
		t.Fatalf("err = %v: an MCP server's ENOENT is not a broken Codex", err)
	}
}

// A run whose worktree was removed under it: the start fails with ENOENT,
// and the connection was marked broken, its fallback put first.
func TestStartErrorMissingDirIsNotBroken(t *testing.T) {
	bin, _ := codexBin(t, `echo '{"type":"thread.started","thread_id":"s1"}'
`)
	gone := filepath.Join(t.TempDir(), "worktree-removed")
	_, err := codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: gone, Prompt: "hi"}, func(Event) {})
	if err == nil || errors.Is(err, llm.ErrCLIBroken) || connectionBroken(err) || !strings.Contains(err.Error(), "worktree-removed") {
		t.Fatalf("err = %v: a missing folder is not a broken CLI", err)
	}
	// the binary gone, its folder there: still a broken CLI
	_, err = codexRunner{}.Run(context.Background(), RunRequest{Bin: filepath.Join(t.TempDir(), "codex"), WorkDir: t.TempDir(), Prompt: "hi"}, func(Event) {})
	if !errors.Is(err, llm.ErrCLIBroken) {
		t.Fatalf("missing binary: err = %v, want ErrCLIBroken", err)
	}
}

// Gemini's npm wrapper ran and its native part did not: the error was a
// plain one, so the connection was never marked broken.
func TestGeminiHealthBrokenInstall(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gemini")
	fakebin.Write(bin, []byte(`#!/bin/sh
cat >/dev/null
echo "Error: Cannot find module '/x/node_modules/@google/gemini-cli/dist/index.js'" >&2
echo "    at Module._resolveFilename (node:internal/modules/cjs/loader:1140:15)" >&2
exit 1
`))
	_, err := geminiRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if !errors.Is(err, llm.ErrCLIBroken) || !connectionBroken(err) || !strings.Contains(err.Error(), "npm install -g @google/gemini-cli") {
		t.Fatalf("err = %v, want ErrCLIBroken with the fix", err)
	}
	// it started (init), then failed on its own: a plain error
	fakebin.Write(bin, []byte(`#!/bin/sh
cat >/dev/null
echo '{"type":"init","session_id":"s1"}'
echo "command not found: rg" >&2
exit 1
`))
	if _, err := (geminiRunner{}).Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {}); err == nil || errors.Is(err, llm.ErrCLIBroken) {
		t.Fatalf("err = %v: a started Gemini is not a broken install", err)
	}
}

// A connection marked broken goes behind its fallbacks, so no run on it
// would ever clear the mark: it is checked again in the background.
func TestCodexHealthBrokenConnectionRechecked(t *testing.T) {
	e := limitsEngine(t)
	ctx := context.Background()
	codex, err := e.providers.Create(ctx, provider.Input{Name: "Codex", Kind: storage.ProviderCodexCLI})
	if err != nil {
		t.Fatal(err)
	}
	claude, err := e.providers.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI})
	if err != nil {
		t.Fatal(err)
	}
	var checks atomic.Int32
	done := make(chan struct{}, 4)
	e.recheck = func(ctx context.Context, id string) {
		if id == codex.ID {
			checks.Add(1)
		}
		done <- struct{}{}
	}
	agent := storage.Agent{ProviderID: codex.ID, FallbackProviderIDs: []string{claude.ID}}
	brokenErr := llm.CLIBrokenError("codex", "spawn codex ENOENT")
	long := time.Now().Add(-2 * RecheckBroken)
	e.keepHealth(codex, brokenErr)
	if p, _ := e.store.Providers().Get(ctx, codex.ID); p.Status != "error" {
		t.Fatalf("status = %q: a broken CLI must mark the connection broken", p.Status)
	}
	cands, _ := e.choices(ctx, agent)
	if len(cands) != 2 || cands[0].Provider.ID != claude.ID {
		t.Fatalf("choices = %+v: the broken one goes last", cands)
	}
	if checks.Load() != 0 {
		t.Fatal("just marked: no recheck yet")
	}
	_ = e.store.Providers().SetStatus(ctx, codex.ID, "error", brokenErr.Error(), nil, long)
	e.choices(ctx, agent)
	e.choices(ctx, agent) // a second turn right after: one check, not two
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("no recheck of a connection broken for a while")
	}
	time.Sleep(50 * time.Millisecond)
	if n := checks.Load(); n != 1 {
		t.Fatalf("rechecks = %d, want 1", n)
	}
}
