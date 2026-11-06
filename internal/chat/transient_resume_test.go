package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/fakebin"
)

// fakeResumable writes a CLI that runs `first` on its first call and `again`
// on every later one; it returns the binary and how many times it ran.
func fakeResumable(t *testing.T, name, first, again string) (string, func() []string) {
	t.Helper()
	dir := t.TempDir()
	bin, calls := filepath.Join(dir, name), filepath.Join(dir, "calls")
	t.Setenv("CODEX_HOME", filepath.Join(dir, "home"))
	fakebin.Write(bin, []byte(`#!/bin/sh
cat > `+dir+`/stdin.$$
n=$(cat `+calls+` 2>/dev/null | wc -l | tr -d ' ')
echo "$*" >> `+calls+`
cp `+dir+`/stdin.$$ `+dir+`/stdin.$n
if [ "$n" = 0 ]; then
`+first+`
else
`+again+`
fi
`))
	return bin, func() []string {
		b, _ := os.ReadFile(calls)
		if len(b) == 0 {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

// An answer Codex stopped writing on a dropped stream passed as done: the
// rest of the turn never came. It goes on in its thread once (ADR-261).
func TestCodexCutStreamGoesOn(t *testing.T) {
	bin, calls := fakeResumable(t, "codex", `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"Phần đầu"}}'
echo '{"type":"turn.failed","error":{"message":"stream disconnected before completion: error sending request"}}'
exit 1`, `echo '{"type":"thread.started","thread_id":"s1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"Phần sau\nVERDICT: OK"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":2}}'`)
	var streamed strings.Builder
	res, err := codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "soát"}, func(ev Event) {
		if ev.Type == "text" {
			streamed.WriteString(ev.Text)
		}
	})
	if err != nil || res.Text != "Phần đầu\n\nPhần sau\nVERDICT: OK" || !strings.Contains(res.Notice, "đã tự chạy tiếp") {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if streamed.String() != res.Text {
		t.Fatalf("streamed = %q", streamed.String())
	}
	c := calls()
	if len(c) != 2 || strings.Contains(c[0], "resume") || !strings.Contains(c[1], "resume s1") {
		t.Fatalf("calls = %q", c)
	}
}

// A cut that is not a passing failure (out of quota) keeps the part written,
// marked cut, and does not run again; nor does a resume that fails again.
func TestCodexCutByQuotaDoesNotGoOn(t *testing.T) {
	bin, calls := fakeResumable(t, "codex", `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"Một nửa"}}'
echo '{"type":"turn.failed","error":{"message":"You have hit your usage limit, try again in 30m"}}'
exit 1`, `exit 1`)
	res, err := codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "x"}, func(Event) {})
	if err != nil || res.Text != "Một nửa" || len(calls()) != 1 {
		t.Fatalf("res = %+v err = %v calls = %q", res, err, calls())
	}
	bin, calls = fakeResumable(t, "codex", `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"Một nửa"}}'
echo '{"type":"turn.failed","error":{"message":"stream disconnected before completion"}}'
exit 1`, `echo '{"type":"turn.failed","error":{"message":"stream disconnected before completion"}}'
exit 1`)
	res, err = codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "x"}, func(Event) {})
	if err != nil || res.Text != "Một nửa" || !strings.Contains(res.Notice, "dừng giữa chừng") || len(calls()) != 2 {
		t.Fatalf("res = %+v err = %v calls = %q", res, err, calls())
	}
}

// Codex died on a 503 once its thread began: the turn failed outright,
// where Claude goes on in its session. Now it goes on once, told why.
func TestCodexTransientResumesOnce(t *testing.T) {
	bin, calls := fakeResumable(t, "codex", `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"turn.failed","error":{"message":"unexpected status 503 Service Unavailable"}}'
exit 1`, `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"xong"}}'`)
	res, err := codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "sửa lỗi"}, func(Event) {})
	if err != nil || res.Text != "xong" || len(calls()) != 2 || !strings.Contains(calls()[1], "resume s1") {
		t.Fatalf("res = %+v err = %v calls = %q", res, err, calls())
	}
	// still failing: the first error stands, as a passing one, run twice only
	bin, calls = fakeResumable(t, "codex", `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"turn.failed","error":{"message":"unexpected status 503 Service Unavailable"}}'
exit 1`, `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"turn.failed","error":{"message":"unexpected status 502 Bad Gateway"}}'
exit 1`)
	_, err = codexRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "x"}, func(Event) {})
	if !Transient(err) || !strings.Contains(err.Error(), "503") || len(calls()) != 2 {
		t.Fatalf("err = %v calls = %q", err, calls())
	}
	// a quota, a 429 or a broken CLI never goes on
	for _, body := range []string{
		`echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"turn.failed","error":{"message":"You’ve hit your usage limit. try again at 9:18 PM."}}'
exit 1`,
		`echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"turn.failed","error":{"message":"429 Too Many Requests: rate limit"}}'
exit 1`,
		brokenWrapper,
	} {
		bin, calls = fakeResumable(t, "codex", body, `echo '{"type":"item.completed","item":{"type":"agent_message","text":"no"}}'`)
		if _, err := (codexRunner{}).Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "x"}, func(Event) {}); err == nil || len(calls()) != 1 {
			t.Fatalf("body %q: err = %v calls = %q", body, err, calls())
		}
	}
}

// Gemini died on a passing failure after its session began: it ran the turn
// again afresh only when resuming, else failed. It goes on in its session.
func TestGeminiTransientResumesOnce(t *testing.T) {
	bin, calls := fakeResumable(t, "gemini", `echo '{"type":"init","session_id":"g1"}'
echo '{"type":"result","status":"error","error":{"message":"read ECONNRESET"}}'
exit 1`, `echo '{"type":"init","session_id":"g1"}'
echo '{"type":"message","role":"assistant","content":"xong"}'
echo '{"type":"result","status":"success","stats":{"input_tokens":1,"output_tokens":1}}'`)
	res, err := geminiRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "x"}, func(Event) {})
	if err != nil || res.Text != "xong" || len(calls()) != 2 || !strings.Contains(calls()[1], "--resume g1") {
		t.Fatalf("res = %+v err = %v calls = %q", res, err, calls())
	}
	bin, calls = fakeResumable(t, "gemini", `echo '{"type":"init","session_id":"g1"}'
echo '{"type":"result","status":"error","error":{"message":"Quota exceeded for quota metric"}}'
exit 1`, `echo '{"type":"message","role":"assistant","content":"no"}'`)
	if _, err := (geminiRunner{}).Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "x"}, func(Event) {}); err == nil || len(calls()) != 1 {
		t.Fatalf("quota: err = %v calls = %q", err, calls())
	}
}
