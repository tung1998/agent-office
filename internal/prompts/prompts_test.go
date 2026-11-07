package prompts

import (
	"testing"
)

// Every prompt file parses as a template (Render's panics are found here).
func TestEveryPromptParses(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("no prompt embedded")
	}
	for _, n := range names {
		if Text(n) == "" {
			t.Errorf("%s is empty", n)
		}
		parsed(n)
	}
}

func TestVersionStableAndUnique(t *testing.T) {
	versions := Versions()
	if len(versions) != len(Names()) {
		t.Fatalf("got %d versions, want %d", len(versions), len(Names()))
	}
	seen := map[string]string{}
	for n, v := range versions {
		if v != Version(n) {
			t.Errorf("%s: Version not stable across calls", n)
		}
		if other, ok := seen[v]; ok {
			t.Errorf("%s and %s share version %s (identical content, or hash collision)", n, other, v)
		}
		seen[v] = n
	}
}

// Every prompt's current content must have a matching entry in the
// changelog (internal/prompts/changelog.go): editing a .md without adding
// one here is a missed "what changed" note, caught instead of silently lost.
func TestChangelogCoversCurrentVersion(t *testing.T) {
	if missing := UnrecordedVersions(); len(missing) > 0 {
		t.Errorf("no changelog entry for current version of: %v — add a ChangeEntry in changelog.go describing the edit", missing)
	}
}
