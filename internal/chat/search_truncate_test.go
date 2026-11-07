package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Both the Go walk (workspace.go) and ripgrep (ripgrep.go) cut a matched
// line at 240 runes, not bytes: a multi-byte rune sitting at the cut
// boundary must not be split in half.
func TestSearchLineTruncationUTF8Boundary(t *testing.T) {
	rg := ripgrepPath() // resolve once, before any test in this file touches rgPath
	t.Cleanup(func() { rgPath = rg })
	if rg == "" {
		t.Skip("no ripgrep on this machine")
	}

	root := t.TempDir()
	ranRipgrep := false
	for pad := 0; pad < 3; pad++ {
		line := "needle " + strings.Repeat("x", 230+pad) + strings.Repeat("ệ", 20) + "\n"
		if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		w := Workspace{Root: root}

		rgPath = ""
		walkOut, err := w.Search("needle", "")
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(walkOut) {
			t.Fatalf("pad=%d walk search output not valid UTF-8: %q", pad, walkOut)
		}

		rgPath = rg
		rgOut, ok := searchRipgrep(root, "needle", "")
		if !ok {
			t.Fatalf("pad=%d searchRipgrep did not run (ok=false)", pad)
		}
		ranRipgrep = true
		if !utf8.ValidString(rgOut) {
			t.Fatalf("pad=%d ripgrep search output not valid UTF-8: %q", pad, rgOut)
		}
	}
	if !ranRipgrep {
		t.Fatal("ripgrep branch never ran")
	}
}
