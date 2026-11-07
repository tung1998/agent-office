package llm

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// detail and the HTTP body are both cut by rune count, not byte count:
// a multi-byte rune sitting at the cut boundary must not be split in half.
func TestTruncationIsUTF8Safe(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n-1) + "ệ" + "bbbb" }

	if err := CLIBrokenError("codex", long(200)); !utf8.ValidString(err.Error()) {
		t.Fatalf("CLIBrokenError detail not valid UTF-8: %q", err.Error())
	}

	raw := []byte(long(300))
	if msg := errorMessage(raw); !utf8.ValidString(msg) {
		t.Fatalf("errorMessage not valid UTF-8: %q", msg)
	}
}
