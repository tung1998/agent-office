package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIFailureReadsTheEndOfTheJSON(t *testing.T) {
	// the reason sits after a long usage block, as Claude Code prints it
	out := `{"duration_api_ms":0,"usage":{"input_tokens":0,` + strings.Repeat(`"pad":"x",`, 80) + `"output_tokens":0},"is_error":true,"subtype":"error","result":"Invalid API key · Please run /login"}`
	if got := cliFailure(out, ""); !strings.Contains(got, "/login") {
		t.Fatalf("lost the reason: %q", got)
	}
	if got := cliFailure("", "boom"); got != "boom" {
		t.Fatalf("stderr: %q", got)
	}
}

// fakeBin writes an executable script answering --version and auth status.
func fakeBin(t *testing.T, loggedIn string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ncase \"$1\" in\n--version) echo '9.9.9 (Claude Code)';;\nauth) printf '%s' '{\"loggedIn\":" + loggedIn + ",\"email\":\"a@b.c\"}';;\nesac\n"
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClaudeCheckSignedOut(t *testing.T) {
	c := &claudeCLI{bin: fakeBin(t, "false")}
	if _, err := c.Check(context.Background()); !errors.Is(err, ErrNeedsLogin) {
		t.Fatalf("want ErrNeedsLogin, got %v", err)
	}
	c = &claudeCLI{bin: fakeBin(t, "true")}
	if res, err := c.Check(context.Background()); err != nil || res.Version == "" {
		t.Fatalf("signed in: %v %+v", err, res)
	}
}
