package chat

import (
	"context"
	"strings"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/verify"
)

// maxCheckFixes caps the turns office opens on its own, in a chain of
// turns, for an agent to fix what failed the checks (ADR-222).
const maxCheckFixes = 2

// mergeCheck runs the project's checks on a diff in dir before office merges
// it with no person (ADR-222), the way Burn checks a piece (ADR-131): an
// agent's "done" is not taken at its word. True when the diff must wait (a
// check failed, or the turn was stopped while checking), why opening pt's
// detail; a failure is kept on turn for its fix turn.
func (e *Engine) mergeCheck(ctx context.Context, turn *Turn, policy perm.Policy, dir string, pt *storage.Patch) bool {
	// a key or token in the diff never merges on its own (ADR-227)
	if leak := verify.Secrets(dir, pt.Files); leak != "" {
		pt.Detail = lines(verify.Held+":\n"+leak, pt.Detail)
		turn.held = leak
		return true
	}
	cmds := verify.ForDiff(policy.MergeChecks, dir, pt.Files)
	if len(cmds) == 0 {
		return false
	}
	turn.emit(Event{Type: "status", Text: "Đang kiểm tra trước khi gộp: " + strings.Join(cmds, ", ")})
	failed := verify.Run(ctx, cmds, dir)
	switch {
	case ctx.Err() != nil:
		pt.Detail = lines(verify.Held+": lượt bị dừng khi đang kiểm tra", pt.Detail)
		return true
	case failed != "":
		pt.Detail = lines(verify.Held+":\n"+failed, pt.Detail)
		turn.held = failed
		return true
	}
	pt.Detail = lines(verify.Passed+" ("+strings.Join(cmds, ", ")+")", pt.Detail)
	return false
}

// lines joins the non-empty ones.
func lines(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}
