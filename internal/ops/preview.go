package ops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// A Burn's run is in a worktree of its own, not the project folder, so a
// dev server started there shows nothing it built until the run is merged.
// A process whose folder is RunCwd ("@burn", or "@burn/web") runs in the
// project's current Burn run instead: a builder Burn (ADR-139) restarts its
// "preview" process there after each piece merged, so the person sees the
// app as it grows.

// RunCwd is the folder of a process that runs in the Burn run's worktree.
const RunCwd = "@burn"

// PreviewSource marks the process a builder Burn keeps (one per project).
const PreviewSource = "burn-preview"

// previewName is that process's name.
const previewName = "preview"

// ErrNoRun: a process in the Burn run, but the project has no run yet.
var ErrNoRun = errors.New("project chưa có lần chạy Burn nào để chạy trong đó")

// SetRunDir sets where a project's current Burn run is ("" when none).
func (m *Manager) SetRunDir(fn func(ctx context.Context, projectID string) string) { m.runDir = fn }

// inRun: cwd names the Burn run's worktree, and the folder under it.
func inRun(cwd string) (string, bool) {
	cwd = filepath.ToSlash(strings.TrimSpace(cwd))
	if cwd == RunCwd {
		return ".", true
	}
	if rest, ok := strings.CutPrefix(cwd, RunCwd+"/"); ok {
		return rest, true
	}
	return "", false
}

// runResolve is resolve for a process in the Burn run.
func (m *Manager) runResolve(ctx context.Context, projectID, sub string) (string, error) {
	base := ""
	if m.runDir != nil {
		base = m.runDir(ctx, projectID)
	}
	if base == "" {
		return "", ErrNoRun
	}
	dir := filepath.Clean(filepath.Join(base, sub))
	if rel, err := filepath.Rel(base, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrBadCwd
	}
	return dir, nil
}

// Preview keeps a project's preview process on command, in its Burn run
// (made the first time, its command updated when it changed), and starts it
// again on the run's latest code.
func (m *Manager) Preview(ctx context.Context, projectID, command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("không có lệnh chạy")
	}
	list, err := m.store.Processes().List(ctx, projectID)
	if err != nil {
		return err
	}
	var def storage.Process
	for _, p := range list {
		if p.Source == PreviewSource {
			def = p
			break
		}
	}
	switch {
	case def.ID == "":
		def, err = m.store.Processes().Create(ctx, storage.Process{ProjectID: projectID, Name: previewName, Command: command, Cwd: RunCwd, Kind: "service", Source: PreviewSource})
		if errors.Is(err, storage.ErrConflict) { // the person has a process of that name: keep theirs
			def, err = m.store.Processes().Create(ctx, storage.Process{ProjectID: projectID, Name: previewName + " (Burn)", Command: command, Cwd: RunCwd, Kind: "service", Source: PreviewSource})
		}
		if err != nil {
			return err
		}
	case def.Command != command || def.Cwd != RunCwd:
		def.Command, def.Cwd = command, RunCwd
		if err := m.store.Processes().Update(ctx, def); err != nil {
			return err
		}
	}
	m.changed()
	return m.Restart(ctx, def.ID)
}
