package agentinfo

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// errorKey cuts by rune count, not byte count: a multi-byte rune sitting at
// the cut boundary must not be split in half.
func TestErrorKeyUTF8Boundary(t *testing.T) {
	msg := strings.Repeat("a", 119) + "ệ" + "bbbb"
	got := errorKey(msg, "500")
	if !utf8.ValidString(got) {
		t.Fatalf("errorKey not valid UTF-8: %q", got)
	}
}
