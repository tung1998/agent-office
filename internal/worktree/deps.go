package worktree

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
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

// keepPrefix names the folder a Keep copy sits in, next to the worktrees.
const keepPrefix = ".deps-keep-"

// KeepMaxAge: a Keep copy older than this was left by an office that died
// mid-install (an install is bounded far below it): Sweep deletes it.
const KeepMaxAge = 2 * time.Hour

// Kept are the dependency folders of a package a worktree already had of
// its own (an earlier install there), moved aside while a new install runs
// (Keep): Restore puts them back when it failed, Drop deletes them when it
// worked. The copy sits next to the worktree (same disk: a rename, no copy).
type Kept struct {
	dir, tmp string
	paths    []string // relative to dir, in the order they were moved
}

// Keep moves aside the dependency folders of the package rel of the
// worktree dir that are its own (not links to the project's); a
// node_modules is left as an empty folder for the install.
func Keep(dir, rel string) (*Kept, error) {
	var own []string
	if err := walkDeps(dir, rel, func(p string, link bool) error {
		if !link {
			r, _ := filepath.Rel(dir, p)
			own = append(own, r)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	k := &Kept{dir: dir}
	if len(own) == 0 {
		return k, nil
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), keepPrefix+"*")
	if err != nil {
		return nil, err
	}
	k.tmp = tmp
	for i, r := range own {
		src := filepath.Join(dir, r)
		if err := os.Rename(src, filepath.Join(tmp, strconv.Itoa(i))); err != nil {
			_ = k.Restore()
			return nil, err
		}
		k.paths = append(k.paths, r)
		if filepath.Base(r) == "node_modules" {
			if err := os.Mkdir(src, 0o755); err != nil {
				_ = k.Restore()
				return nil, err
			}
		}
	}
	return k, nil
}

// Saved is how many folders Keep moved aside.
func (k *Kept) Saved() int {
	if k == nil {
		return 0
	}
	return len(k.paths)
}

// Restore puts the folders Keep moved aside back where they were, over
// what the install left there.
func (k *Kept) Restore() error {
	if k == nil {
		return nil
	}
	var first error
	for i, r := range k.paths {
		p, saved := filepath.Join(k.dir, r), filepath.Join(k.tmp, strconv.Itoa(i))
		_, err := os.Lstat(saved) // the copy gone: what the install left stays rather than nothing
		if err == nil {
			err = os.RemoveAll(p)
		}
		if err == nil {
			err = os.Rename(saved, p)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	if first == nil && k.tmp != "" {
		_ = os.RemoveAll(k.tmp)
	}
	k.paths = nil
	return first
}

// Drop deletes the folders Keep moved aside (the install worked).
func (k *Kept) Drop() {
	if k == nil {
		return
	}
	if k.tmp != "" {
		_ = os.RemoveAll(k.tmp)
	}
	k.paths = nil
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
