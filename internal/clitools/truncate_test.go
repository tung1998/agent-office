package clitools

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// firstLine cuts by rune count, not byte count: a multi-byte rune sitting
// at the cut boundary must not be split in half.
func TestFirstLineUTF8Boundary(t *testing.T) {
	s := strings.Repeat("a", 199) + "ệ" + "bbbb"
	if got := firstLine(s); !utf8.ValidString(got) {
		t.Fatalf("firstLine not valid UTF-8: %q", got)
	}
}
