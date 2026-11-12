package chat

import (
	"testing"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A role without access runs with its agent's own permission; a value caps it.
func TestAccessLevelDefaultsToAgent(t *testing.T) {
	t.Parallel()
	for access, want := range map[string]string{workflow.AccessAgent: "", workflow.AccessAnalyze: perm.Read, workflow.AccessPropose: perm.Propose, workflow.AccessEdit: perm.Edit} {
		if got := accessLevel(access); got != want {
			t.Fatalf("accessLevel(%q) = %q, want %q", access, got, want)
		}
	}
}
