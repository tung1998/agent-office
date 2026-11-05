package llm

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/fakebin"
)

// Kết nối AI's Kiểm tra (and the background recheck of a broken connection)
// on a codex whose npm install lost its native binary: it said a Node stack
// trace, not that the CLI is broken nor how to fix it.
func TestCLIBrokenCheck(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	script := `#!/bin/sh
echo "Error: spawn /x/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/codex/codex ENOENT" >&2
echo "    at ChildProcess._handle.onexit (node:internal/child_process:285:19)" >&2
exit 1
`
	if err := fakebin.Write(bin, []byte(script)); err != nil {
		t.Fatal(err)
	}
	_, err := (&codexCLI{bin: bin}).Check(context.Background())
	if !errors.Is(err, ErrCLIBroken) {
		t.Fatalf("err = %v, want ErrCLIBroken", err)
	}
	if !strings.Contains(err.Error(), "npm install -g @openai/codex") || strings.Contains(err.Error(), "ChildProcess") {
		t.Fatalf("err = %q: want the fix, not the stack", err)
	}
	if _, err := (&codexCLI{bin: filepath.Join(dir, "gone")}).Check(context.Background()); !errors.Is(err, ErrCLIBroken) {
		t.Fatalf("missing file: err = %v, want ErrCLIBroken", err)
	}
	if _, err := (&claudeCLI{bin: "no-such-claude-cli"}).Check(context.Background()); !errors.Is(err, ErrCLIBroken) {
		t.Fatalf("not on PATH: err = %v, want ErrCLIBroken", err)
	}
	// a CLI that ran and failed on its own is not a broken install
	fakebin.Write(bin, []byte("#!/bin/sh\necho 'model not supported' >&2\nexit 2\n"))
	if _, err := run(context.Background(), bin, "", "exec"); err == nil || errors.Is(err, ErrCLIBroken) {
		t.Fatalf("err = %v: a failed run is not ErrCLIBroken", err)
	}
	// it started and wrote its output; an MCP server it spawns is missing
	fakebin.Write(bin, []byte("#!/bin/sh\necho '{\"type\":\"thread.started\"}'\necho 'spawn npx ENOENT' >&2\nexit 1\n"))
	if _, err := run(context.Background(), bin, "", "exec"); err == nil || errors.Is(err, ErrCLIBroken) {
		t.Fatalf("err = %v: a CLI that wrote output started", err)
	}
}

// A run's folder that is gone fails the start with ENOENT too: the run's
// problem, not a broken CLI.
func TestStartErrorMissingDir(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	err := StartErrorIn("codex", gone, &fs.PathError{Op: "fork/exec", Path: "/bin/codex", Err: fs.ErrNotExist})
	if err == nil || errors.Is(err, ErrCLIBroken) || !strings.Contains(err.Error(), gone) {
		t.Fatalf("err = %v", err)
	}
	if err := StartError("codex", &fs.PathError{Op: "chdir", Path: gone, Err: fs.ErrNotExist}); errors.Is(err, ErrCLIBroken) {
		t.Fatalf("chdir: err = %v", err)
	}
	if err := StartErrorIn("codex", t.TempDir(), &fs.PathError{Op: "fork/exec", Path: "/bin/codex", Err: fs.ErrNotExist}); !errors.Is(err, ErrCLIBroken) {
		t.Fatalf("binary gone, folder there: err = %v, want ErrCLIBroken", err)
	}
}
