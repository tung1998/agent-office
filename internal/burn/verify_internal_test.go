package burn

import (
	"context"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// verifyCommands returns nothing for a project with no Burn-written check
// and no file Guess knows (no go.mod, no Cargo.toml): nothing is guessed, so
// nothing runs — the check stays "none known", not a silent pass mistaken
// for a real one.
func TestVerifyCommandsEmptyForAProjectWithNoKnownCheck(t *testing.T) {
	dir := t.TempDir() // no go.mod, no Cargo.toml
	b := storage.BurnSession{}
	if got := verifyCommands(b, dir); got != nil {
		t.Fatalf("verifyCommands = %v, want none", got)
	}
}

// checked, with no check to run, still passes the piece (ADR-131: Burn
// without a check does not fail-closed every Node/Python/new project) but
// marks the summary so the person reads it with care instead of trusting a
// result nothing actually ran.
func TestCheckedMarksUnverifiedWhenNothingToRun(t *testing.T) {
	s, b, repo := mergeTest(t)
	ctx := context.Background()
	it := piece(t, s, b, repo, "one", func(x string) string { return x + "nine\n" })
	it.Summary = "xong việc"
	it.Status = "done"

	if ok := s.checked(ctx, b, &it); !ok {
		t.Fatalf("checked = false, want true (no check to run)")
	}
	if !strings.Contains(it.Summary, unverified) {
		t.Fatalf("summary = %q, want it to carry the unverified marker", it.Summary)
	}
}

// checked does not mark a piece unverified twice, and does not touch a
// summary that already explains there was nothing to run.
func TestCheckedDoesNotDoubleMark(t *testing.T) {
	s, b, repo := mergeTest(t)
	ctx := context.Background()
	it := piece(t, s, b, repo, "one", func(x string) string { return x + "nine\n" })
	it.Summary = "xong việc " + unverified
	it.Status = "done"

	if ok := s.checked(ctx, b, &it); !ok {
		t.Fatalf("checked = false, want true")
	}
	if n := strings.Count(it.Summary, unverified); n != 1 {
		t.Fatalf("summary = %q, want the marker once", it.Summary)
	}
}

// checked runs the Burn's own written check even with no file Guess would
// otherwise key off of, and fails the piece when it fails.
func TestCheckedRunsTheBurnsOwnCheck(t *testing.T) {
	s, b, repo := mergeTest(t)
	b.Verify = "false"
	ctx := context.Background()
	it := piece(t, s, b, repo, "one", func(x string) string { return x + "nine\n" })
	it.Summary = "xong việc"
	it.Status = "done"

	if ok := s.checked(ctx, b, &it); ok {
		t.Fatalf("checked = true, want false (the check fails)")
	}
	if it.Status == "done" {
		t.Fatalf("status = %q, want it sent back", it.Status)
	}
}
