package chat

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/llm"
)

// fakeCLI writes an executable script standing in for an agent's CLI.
func fakeCLI(t *testing.T, name, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	if err := fakebin.Write(bin, []byte("#!/bin/sh\n"+script)); err != nil {
		t.Fatal(err)
	}
	return bin
}

// Claude Code signed out elsewhere still says it is signed in locally: the
// service's 401 reads as needing a login.
func TestClaudeRejectedSessionNeedsLogin(t *testing.T) {
	t.Parallel()
	bin := fakeCLI(t, "claude", `if [ "$1" = auth ]; then echo '{"loggedIn":true}'; exit 0; fi
cat >/dev/null
echo '{"type":"result","subtype":"success","is_error":true,"result":"Failed to authenticate. API Error: 401 OAuth access token has been revoked"}'
exit 1
`)
	_, err := claudeRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: t.TempDir(), Prompt: "hi"}, func(Event) {})
	if !errors.Is(err, llm.ErrNeedsLogin) {
		t.Fatalf("err = %v, want ErrNeedsLogin", err)
	}
}
