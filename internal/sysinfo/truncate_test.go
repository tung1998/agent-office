package sysinfo

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A command line with a multi-byte rune straddling the 300-byte cut must
// still come back as valid UTF-8, not a byte-sliced half-rune.
func TestTreeCmdlineUTF8Boundary(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())

	prefix := "/bin/sh -c sleep 5 & wait "
	pad := strings.Repeat("a", 300-len(prefix)-1) + "ệ" + "bbbb"
	cmd := exec.Command("/bin/sh", "-c", "sleep 5 & wait", pad)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	var self Proc
	deadline := time.Now().Add(3 * time.Second)
	for {
		var err error
		self, _, err = s.Tree(ctx, int32(cmd.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		if len(self.Cmd) > 300 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(self.Cmd) <= 300 {
		t.Fatalf("cmdline too short to exercise the cut: %q", self.Cmd)
	}
	if !utf8.ValidString(self.Cmd) {
		t.Fatalf("truncated cmdline is not valid UTF-8: %q", self.Cmd)
	}
}
