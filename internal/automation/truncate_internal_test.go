package automation

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Finding.Excerpt is cut by rune count, not byte count, in both callers
// (hiddenFindings and the per-line rule scan): a multi-byte rune sitting at
// the cut boundary must not be split in half, regardless of how the prefix
// before it lines up.
func TestFindingExcerptUTF8Boundary(t *testing.T) {
	for pad := 0; pad < 3; pad++ {
		prefix := strings.Repeat("x", pad)

		// hiddenFindings: tagChars match forces an invisible-smuggling finding
		// whose excerpt is the rest of that line.
		content := "\U000E0001" + prefix + strings.Repeat("ệ", 80)
		for _, f := range hiddenFindings("skill.md", content) {
			if !utf8.ValidString(f.Excerpt) {
				t.Fatalf("pad=%d hiddenFindings excerpt not valid UTF-8: %q", pad, f.Excerpt)
			}
		}

		// CheckContent: a line matching the "sudo" rule.
		line := "sudo " + prefix + strings.Repeat("ệ", 80)
		for _, f := range CheckContent(map[string]string{"skill.md": line}) {
			if !utf8.ValidString(f.Excerpt) {
				t.Fatalf("pad=%d CheckContent excerpt not valid UTF-8: %q", pad, f.Excerpt)
			}
		}
	}
}
