package worktree

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// depDepth bounds how deep dependency folders are looked for under a package.
const depDepth = 4

// walkDeps calls fn for each dependency folder (node_modules, .venv, venv)
// under rel of dir, link or folder, without going into them.
func walkDeps(dir, rel string, fn func(path string, link bool) error) error {
	top := filepath.Join(dir, filepath.FromSlash(rel))
	if r, err := filepath.Rel(dir, top); err != nil || strings.HasPrefix(r, "..") {
		return os.ErrInvalid
	}
	return filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // a folder gone or unreadable: nothing of it
		}
		r, _ := filepath.Rel(top, p)
		if d.Name() == ".git" || strings.Count(filepath.ToSlash(r), "/") > depDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if p == top || !slices.Contains(linkDirs, d.Name()) {
			return nil
		}
		link := d.Type()&os.ModeSymlink != 0
		if err := fn(p, link); err != nil {
			return err
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}

// Detach gives the package rel of the worktree dir dependency folders of
// its own: each one linked from the project (node_modules, .venv, those of
// its workspace packages too) is unlinked (node_modules left as an empty
// folder, a virtualenv for its tool to make), so an install there
// never writes into the project's (ADR-251). A folder already its own is
// kept. The worktree's info/exclude still ignores them (its patterns match
// a folder as well as a link). It returns those it detached, relative to dir.
func Detach(dir, rel string) ([]string, error) {
	var out []string
	err := walkDeps(dir, rel, func(p string, link bool) error {
		if !link {
			return nil
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		// a virtualenv is made by its tool (uv, python -m venv), which wants
		// none there; node_modules starts empty
		if filepath.Base(p) == "node_modules" {
			if err := os.Mkdir(p, 0o755); err != nil {
				return err
			}
		}
		r, _ := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(r))
		return nil
	})
	return out, err
}

// Relink links back the dependency folders of the package rel that Detach
// made the worktree's own, once the project installed the same ones (its
// diff merged): the worktree uses the project's again, and the space of its
// copy is freed. Only those the project has. It returns them.
func Relink(repo, dir, rel string) ([]string, error) {
	var out []string
	err := walkDeps(dir, rel, func(p string, link bool) error {
		if link {
			return nil
		}
		r, _ := filepath.Rel(dir, p)
		src := filepath.Join(repo, r)
		if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
			return nil
		}
		if err := os.RemoveAll(p); err != nil {
			return err
		}
		if err := os.Symlink(src, p); err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(r))
		return nil
	})
	return out, err
}
