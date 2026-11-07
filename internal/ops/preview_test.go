package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// A process at "@burn" runs in the project's Burn run, not its folder; with
// no run it does not start, and "@burn/.." cannot leave the run (ADR-266).
func TestRunCwd(t *testing.T) {
	m, st, proj := setup(t)
	ctx := context.Background()
	p, err := st.Processes().Create(ctx, storageProcess(proj.ID, "pwd", RunCwd, "pwd > where.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx, p.ID); err != ErrNoRun {
		t.Fatalf("no run: %v", err)
	}
	run := t.TempDir()
	m.SetRunDir(func(_ context.Context, id string) string {
		if id == proj.ID {
			return run
		}
		return ""
	})
	if err := m.Start(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "job exit", func() bool { return m.State(p.ID).Status == "exited" })
	got, err := os.ReadFile(filepath.Join(run, "where.txt"))
	if err != nil {
		t.Fatalf("not run in the run's worktree: %v", err)
	}
	if real, _ := filepath.EvalSymlinks(run); !strings.Contains(string(got), filepath.Base(real)) {
		t.Fatalf("ran in %q, want %q", got, run)
	}
	escape, _ := st.Processes().Create(ctx, storageProcess(proj.ID, "x", RunCwd+"/../..", "true"))
	if err := m.Start(ctx, escape.ID); err != ErrBadCwd {
		t.Fatalf("escape from the run: %v", err)
	}
}

// Preview makes the project's preview process once, in the run, updates its
// command when it changes and starts it again each time.
func TestPreview(t *testing.T) {
	m, st, proj := setup(t)
	ctx := context.Background()
	run := t.TempDir()
	m.SetRunDir(func(context.Context, string) string { return run })
	if err := m.Preview(ctx, proj.ID, "  "); err == nil {
		t.Fatal("empty command accepted")
	}
	if err := m.Preview(ctx, proj.ID, "echo one > out.txt; sleep 30"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first preview", func() bool {
		b, _ := os.ReadFile(filepath.Join(run, "out.txt"))
		return strings.TrimSpace(string(b)) == "one"
	})
	if err := m.Preview(ctx, proj.ID, "echo two > out.txt; sleep 30"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restarted preview", func() bool {
		b, _ := os.ReadFile(filepath.Join(run, "out.txt"))
		return strings.TrimSpace(string(b)) == "two"
	})
	list, _ := st.Processes().List(ctx, proj.ID)
	if len(list) != 1 || list[0].Source != PreviewSource || list[0].Cwd != RunCwd || list[0].Name != previewName || !strings.HasPrefix(list[0].Command, "echo two") {
		t.Fatalf("processes: %+v", list)
	}
	if s := m.State(list[0].ID).Status; s != "running" {
		t.Fatalf("preview %s, want running", s)
	}
}

// The person's own process named "preview" is kept: the Burn's gets another name.
func TestPreviewKeepsPersonsProcess(t *testing.T) {
	m, st, proj := setup(t)
	ctx := context.Background()
	m.SetRunDir(func(context.Context, string) string { return t.TempDir() })
	if _, err := st.Processes().Create(ctx, storageProcess(proj.ID, previewName, ".", "true")); err != nil {
		t.Fatal(err)
	}
	if err := m.Preview(ctx, proj.ID, "sleep 30"); err != nil {
		t.Fatal(err)
	}
	list, _ := st.Processes().List(ctx, proj.ID)
	if len(list) != 2 {
		t.Fatalf("processes: %+v", list)
	}
	for _, p := range list {
		if p.Name == previewName && p.Source == PreviewSource {
			t.Fatalf("the person's process taken over: %+v", p)
		}
	}
}

func storageProcess(projectID, name, cwd, command string) storage.Process {
	return storage.Process{ProjectID: projectID, Name: name, Cwd: cwd, Kind: "service", Command: command}
}
