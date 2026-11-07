package burn

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/worktree"
)

const agentsMD = "# App\n\nStack: Go.\n\n## Checks\n\nRun these:\n\n```sh\n# build first\ngo build ./...\n\ngo test ./...\n```\n\n```\nnot this block\n```\n\n### Notes\n- `ignored when a block exists`\n\n## Run\n\n- `go run . --port $PORT`: the app\n- plain text, no code\n\n## Later\n\n```\nnot a check\n```\n"

// AGENTS.md's "## Checks" and "## Run" are read as commands (ADR-266): the
// section's first fenced block, else its `code` list items; nothing else.
func TestAgentsCommands(t *testing.T) {
	dir := t.TempDir()
	if got := agentsCommands(dir, checksHeads...); got != nil {
		t.Fatalf("no file: %v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, agentsFile), []byte(agentsMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := agentsCommands(dir, checksHeads...); !slices.Equal(got, []string{"go build ./...", "go test ./..."}) {
		t.Fatalf("checks: %q", got)
	}
	if got := agentsCommands(dir, runHeads...); !slices.Equal(got, []string{"go run . --port $PORT"}) {
		t.Fatalf("run: %q", got)
	}
	if got := agentsCommands(dir, "missing"); got != nil {
		t.Fatalf("no such section: %q", got)
	}
	vi := "## Kiểm chứng\n- `npm test`\n"
	if err := os.WriteFile(filepath.Join(dir, agentsFile), []byte(vi), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := agentsCommands(dir, checksHeads...); !slices.Equal(got, []string{"npm test"}) {
		t.Fatalf("vietnamese heading: %q", got)
	}
}

// The Burn's own checks win; without them AGENTS.md's, before a guess.
func TestVerifyCommandsFromAgents(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644)
	os.WriteFile(filepath.Join(dir, agentsFile), []byte("## Checks\n```\nmake check\n```\n"), 0o644)
	if got := verifyCommands(storage.BurnSession{}, dir); !slices.Equal(got, []string{"make check"}) {
		t.Fatalf("from AGENTS.md: %q", got)
	}
	if got := verifyCommands(storage.BurnSession{Verify: "go vet ./..."}, dir); !slices.Equal(got, []string{"go vet ./..."}) {
		t.Fatalf("the Burn's own: %q", got)
	}
	os.Remove(filepath.Join(dir, agentsFile))
	if got := verifyCommands(storage.BurnSession{}, dir); len(got) == 0 || !strings.HasPrefix(got[0], "go ") {
		t.Fatalf("guessed: %q", got)
	}
}

// A builder piece merged starts the preview with AGENTS.md's run command;
// another template, or no command yet, starts nothing.
func TestPreviewRun(t *testing.T) {
	trees := worktree.New(t.TempDir())
	s := &Service{trees: trees}
	b := storage.BurnSession{ProjectID: "p1", RunBranch: "burn/2026-11-07-100000", Template: "builder"}
	run := trees.Path(b.ProjectID, runTree(b))
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(run, ".git"), []byte("gitdir: x\n"), 0o644)
	var got []string
	s.SetPreview(func(_ context.Context, projectID, command string) error {
		got = append(got, projectID+": "+command)
		return nil
	})
	s.previewRun(context.Background(), b) // no AGENTS.md yet
	if len(got) != 0 {
		t.Fatalf("started with no command: %v", got)
	}
	os.WriteFile(filepath.Join(run, agentsFile), []byte(agentsMD), 0o644)
	s.previewRun(context.Background(), b)
	if !slices.Equal(got, []string{"p1: go run . --port $PORT"}) {
		t.Fatalf("preview: %v", got)
	}
	other := b
	other.Template = "general"
	s.previewRun(context.Background(), other)
	if len(got) != 1 {
		t.Fatalf("a general Burn started a preview: %v", got)
	}
}
