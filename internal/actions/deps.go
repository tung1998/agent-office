package actions

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tung1998/agent-office/internal/deps"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/worktree"
)

// checkDeps checks a deps_install proposal (ADR-251): its package folder is
// inside the project, and it goes in the run's own worktree when it has one
// (Args.Dir), else in the project. Target becomes "dashboard (pnpm)".
func (s *Service) checkDeps(ctx context.Context, sc Scope, a *storage.Action) error {
	d := a.Args.Deps
	if d == nil {
		d = &storage.DepsArgs{Dir: a.Target}
		a.Args.Deps = d
	}
	d.Dir = path.Clean(filepath.ToSlash(strings.Trim(strings.TrimSpace(d.Dir), "/")))
	if d.Dir == "" || d.Dir == ".." || strings.HasPrefix(d.Dir, "../") || filepath.IsAbs(d.Dir) {
		return errors.New("thư mục gói phải nằm trong project (\".\" = thư mục gốc)")
	}
	root, err := s.projectPath(ctx, sc.ProjectID)
	if err != nil {
		return err
	}
	if sc.Dir != "" && a.Args.Patch == "" { // a run's own worktree; a merged diff's install is the project's
		a.Args.Dir, root = sc.Dir, sc.Dir
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(d.Dir))); err != nil {
		return errors.New("không có thư mục " + d.Dir + " trong project")
	}
	if d.Manager == "" {
		switch {
		case exists(filepath.Join(root, d.Dir, "package.json")): // a workspace package installs from its lockfile's folder
			if c := deps.Changes(root, []string{path.Join(d.Dir, "package.json")}); len(c) > 0 {
				d.Dir, d.Manager = c[0].Dir, c[0].Manager
			}
		case exists(filepath.Join(root, d.Dir, "uv.lock")):
			d.Manager = "uv"
		case exists(filepath.Join(root, d.Dir, "requirements.txt")):
			d.Manager = "pip"
		}
	}
	if len(deps.Command(deps.Change{Dir: d.Dir, Manager: d.Manager})) == 0 {
		return deps.ErrNoManager
	}
	a.Target = deps.Change{Dir: d.Dir, Manager: d.Manager}.Label()
	return nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// installDeps carries out an approved deps_install: in a worktree, the
// package's dependency folders become its own first (worktree.Detach) so the
// project's are never touched; in the project, the worktree its diff came
// from links back to the project's once they are installed. Its status and
// the end of the install's output.
func (s *Service) installDeps(ctx context.Context, a storage.Action) (string, string) {
	if a.Args.Deps == nil {
		return "failed", "thiếu thư mục gói"
	}
	c := deps.Change{Dir: a.Args.Deps.Dir, Manager: a.Args.Deps.Manager}
	root, err := s.projectPath(ctx, a.ProjectID)
	if err != nil {
		return "failed", err.Error()
	}
	project, where := root, "project"
	if a.Args.Dir != "" {
		root, where = a.Args.Dir, "worktree"
		if _, err := os.Stat(root); err != nil {
			return "failed", "worktree của lượt này đã được dọn nên không cài được nữa"
		}
		if _, err := worktree.Detach(root, c.Dir); err != nil {
			return "failed", "tách thư viện của worktree: " + err.Error()
		}
	}
	out, err := deps.Install(ctx, root, c)
	status, detail := "done", "Đã cài thư viện "+c.Label()+" trong "+where
	if err != nil {
		status, detail = "failed", "Cài thư viện "+c.Label()+" lỗi: "+err.Error()
		if a.Args.Dir != "" { // the worktree is not left without any: the project's again
			if back, rerr := worktree.Relink(project, root, c.Dir); rerr == nil && len(back) > 0 {
				detail += " (worktree dùng lại thư viện của project)"
			}
		}
	} else if a.Args.Dir == "" && a.Args.Tree != "" {
		if _, serr := os.Stat(a.Args.Tree); serr == nil {
			_, _ = worktree.Relink(root, a.Args.Tree, c.Dir) // its copy is the project's now
		}
	}
	if out != "" {
		detail += "\n" + out
	}
	return status, detail
}
