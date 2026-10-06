package chat

import (
	"slices"
	"strings"
	"testing"
)

// A thinking level goes to Claude Code as --effort in every kind of run, to
// Codex as model_reasoning_effort (max = its xhigh); none, nothing is added.
func TestEffortArgs(t *testing.T) {
	for _, req := range []RunRequest{{Effort: "max"}, {Effort: "max", Write: true}, {Effort: "max", FullAccess: true}, {Effort: "max", NoTools: true}} {
		a := claudeRunner{}.args(req, false)
		if i := slices.Index(a, "--effort"); i < 0 || a[i+1] != "max" {
			t.Fatalf("claude args %+v: %v", req, a)
		}
	}
	if a := (claudeRunner{}).args(RunRequest{}, false); slices.Contains(a, "--effort") {
		t.Fatalf("no level, yet: %v", a)
	}
	if got := strings.Join(codexEffortArgs("max"), " "); got != `-c model_reasoning_effort="xhigh"` {
		t.Fatalf("codex max = %s", got)
	}
	if got := strings.Join(codexEffortArgs("low"), " "); got != `-c model_reasoning_effort="low"` {
		t.Fatalf("codex low = %s", got)
	}
	if codexEffortArgs("") != nil {
		t.Fatal("codex: no level, yet args")
	}
}
