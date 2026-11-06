package chat

import (
	"context"
	"strings"
	"testing"
)

// The CLIs' own tools that fail are told (event tool_failed, with what the
// tool said) so a run stuck on one can get an advisor (ADR-262).
func TestRunnersTellToolFailures(t *testing.T) {
	claude := fakeCLI(t, "claude", `cat >/dev/null
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./x"}}]}}'
echo '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"--- FAIL: TestA FAIL"}]}}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"a.go"}}]}}'
echo '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":[{"type":"text","text":"File does not exist."}]}]}}'
echo '{"type":"result","subtype":"success","result":"done","usage":{"input_tokens":1,"output_tokens":1}}'
`)
	codex := fakeCLI(t, "codex", `echo '{"type":"thread.started","thread_id":"s1"}'
echo '{"type":"item.started","item":{"id":"c1","type":"command_execution","command":"make build"}}'
echo '{"type":"item.completed","item":{"id":"c1","type":"command_execution","command":"make build","exit_code":2,"aggregated_output":"make: *** No rule to make target"}}'
echo '{"type":"item.started","item":{"id":"c2","type":"command_execution","command":"ls"}}'
echo '{"type":"item.completed","item":{"id":"c2","type":"command_execution","command":"ls","exit_code":0,"aggregated_output":"a"}}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"done"}}'
`)
	for name, run := range map[string]func(emit func(Event)) (RunResult, error){
		"claude": func(emit func(Event)) (RunResult, error) {
			return claudeRunner{}.Run(context.Background(), RunRequest{Bin: claude, WorkDir: t.TempDir(), Prompt: "x"}, emit)
		},
		"codex": func(emit func(Event)) (RunResult, error) {
			return codexRunner{}.Run(context.Background(), RunRequest{Bin: codex, WorkDir: t.TempDir(), Prompt: "x"}, emit)
		},
	} {
		var failed []Event
		res, err := run(func(ev Event) {
			if ev.Type == "tool_failed" {
				failed = append(failed, ev)
			}
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		switch name {
		case "claude":
			if len(failed) != 2 || failed[0].Tool.Name != "Bash" || !strings.Contains(failed[0].Text, "FAIL: TestA") || failed[1].Text != "File does not exist." {
				t.Fatalf("claude failures = %+v", failed)
			}
		case "codex":
			if len(failed) != 1 || !strings.Contains(failed[0].Tool.Summary, "make build") || !strings.Contains(failed[0].Text, "No rule") || !res.Tools[0].Error || res.Tools[1].Error {
				t.Fatalf("codex failures = %+v tools = %+v", failed, res.Tools)
			}
		}
	}
}
