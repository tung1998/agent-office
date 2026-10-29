// Package packs is office's library of project packs (ADR-199): bundles of
// ready-made projects, each a folder here named @agentoffice/<folder>. A
// bundle has a root pack (a whole organisation's top, e.g. a company's board
// of directors) and unit packs it opens later (departments, products), listed
// in its bundle.json. A pack is named @agentoffice/<bundle> (its root) or
// @agentoffice/<bundle>/<unit>.
package packs

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed all:startup all:ngoai-ngu all:trading
var files embed.FS

// Scope prefixes every pack name of the library.
const Scope = "@agentoffice"

// Bundle is one folder of the library.
type Bundle struct {
	Name        string  `json:"name"` // @agentoffice/<folder>
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Packs       []Entry `json:"packs"`
	dir         string
}

// Entry is one pack of a bundle.
type Entry struct {
	Ref         string   `json:"ref"` // its name: @agentoffice/<bundle>[/<key>]
	Key         string   `json:"key"` // "" = the root
	Title       string   `json:"title"`
	Description string   `json:"description"`
	File        string   `json:"file"` // in the bundle
	Path        string   `json:"path"` // in office's repo, for a link to its source
	Root        bool     `json:"root,omitempty"`
	Skills      []string `json:"skills,omitempty"` // a unit's: the bundle's skills its repo gets
}

// IsRef: s names a pack of the library.
func IsRef(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), Scope+"/") }

// Bundles lists the library.
func Bundles() ([]Bundle, error) {
	dirs, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, err
	}
	var out []Bundle
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(files, path.Join(d.Name(), "bundle.json"))
		if err != nil {
			return nil, fmt.Errorf("bộ %s: %w", d.Name(), err)
		}
		b := Bundle{Name: Scope + "/" + d.Name(), dir: d.Name()}
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, fmt.Errorf("bộ %s: %w", d.Name(), err)
		}
		b.Name = Scope + "/" + d.Name()
		for i := range b.Packs {
			e := &b.Packs[i]
			e.Ref = b.Name
			if e.Key != "" {
				e.Ref += "/" + e.Key
			}
			e.Path = path.Join("templates/packs", d.Name(), e.File)
		}
		out = append(out, b)
	}
	return out, nil
}

// Renamed are bundles' old names: a ref by one still finds its pack.
var Renamed = map[string]string{Scope + "/company": Scope + "/startup"}

// Find is the pack ref names.
func Find(ref string) (Bundle, Entry, error) {
	ref = strings.TrimSpace(ref)
	for old, now := range Renamed {
		if ref == old || strings.HasPrefix(ref, old+"/") {
			ref = now + strings.TrimPrefix(ref, old)
		}
	}
	list, err := Bundles()
	if err != nil {
		return Bundle{}, Entry{}, err
	}
	for _, b := range list {
		for _, e := range b.Packs {
			if e.Ref == ref {
				return b, e, nil
			}
		}
	}
	return Bundle{}, Entry{}, fmt.Errorf("thư viện không có mẫu %s", ref)
}

// Read is the pack's JSON.
func Read(ref string) ([]byte, error) {
	b, e, err := Find(ref)
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(files, path.Join(b.dir, e.File))
}

// ErrExists: the folder already holds a file the pack would write; nothing was written.
var ErrExists = errors.New("thư mục đã có file trùng với mẫu")

// Write puts pack ref into dir (which exists), never over a file: a root
// gets its whole bundle (its own pack as office.pack.json) but the other packs (they stay in the library, named
// by ref); a unit gets its pack as office.pack.json and the skills it lists.
// With commit, dir is a git repo and the files are committed.
func Write(ctx context.Context, ref, dir string, commit bool) ([]string, error) {
	b, e, err := Find(ref)
	if err != nil {
		return nil, err
	}
	want := map[string]string{} // dir's path → the bundle's
	if e.Root {
		err = fs.WalkDir(files, b.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel := strings.TrimPrefix(p, b.dir+"/")
			if rel == "bundle.json" || strings.HasPrefix(rel, "packs/") {
				return nil
			}
			want[rel] = p
			return nil
		})
		if err != nil {
			return nil, err
		}
		want["office.pack.json"] = path.Join(b.dir, e.File) // a bundle may have more roots, each its own pack
	} else {
		want["office.pack.json"] = path.Join(b.dir, e.File)
		for _, s := range e.Skills {
			if _, err := fs.Stat(files, path.Join(b.dir, ".claude/skills", s, "SKILL.md")); err != nil {
				return nil, fmt.Errorf("mẫu %s cần skill %s mà bộ không có", ref, s)
			}
			if err := skillFiles(b.dir, s, want); err != nil {
				return nil, err
			}
		}
	}
	var paths []string
	for rel := range want {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			return nil, fmt.Errorf("%w: %s", ErrExists, rel)
		}
		paths = append(paths, rel)
	}
	slices.Sort(paths)
	for _, rel := range paths {
		raw, err := fs.ReadFile(files, want[rel])
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			return nil, err
		}
	}
	if !commit {
		return paths, nil
	}
	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "user.name=office", "-c", "user.email=office@localhost"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := git(append([]string{"add", "--"}, paths...)...); err != nil {
		return paths, err
	}
	return paths, git("commit", "-q", "-m", "Mẫu "+ref+" của agent-office")
}

// skillFiles puts every file of skill s of the bundle at dir into want
// (dir's path → the bundle's): its SKILL.md and what it reads or runs.
func skillFiles(dir, s string, want map[string]string) error {
	root := path.Join(dir, ".claude/skills", s)
	return fs.WalkDir(files, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want[strings.TrimPrefix(p, dir+"/")] = p
		return nil
	})
}

// SyncFiles brings dir's copy of pack ref's files up to the library (ADR-206):
// its pack (office.pack.json) and its skills (.claude/), overwritten where
// they differ; documents (docs/, README) are the project's own once written.
// It returns the files that differ; unless dryRun they are written and, in a
// git repo, committed on their own.
func SyncFiles(ctx context.Context, ref, dir string, dryRun bool) ([]string, error) {
	b, e, err := Find(ref)
	if err != nil {
		return nil, err
	}
	want := map[string]string{"office.pack.json": path.Join(b.dir, e.File)}
	if e.Root {
		err = fs.WalkDir(files, path.Join(b.dir, ".claude"), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			want[strings.TrimPrefix(p, b.dir+"/")] = p
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	} else {
		for _, s := range e.Skills {
			if err := skillFiles(b.dir, s, want); err != nil {
				return nil, err
			}
		}
	}
	var changed []string
	for rel, src := range want {
		raw, err := fs.ReadFile(files, src)
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if cur, err := os.ReadFile(dst); err == nil && string(cur) == string(raw) {
			continue
		}
		changed = append(changed, rel)
		if dryRun {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			return nil, err
		}
	}
	slices.Sort(changed)
	if dryRun || len(changed) == 0 {
		return changed, nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return changed, nil // not a git repo: the files are written, nothing to commit
	}
	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "user.name=office", "-c", "user.email=office@localhost"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := git(append([]string{"add", "--"}, changed...)...); err != nil {
		return changed, err
	}
	st := exec.CommandContext(ctx, "git", append([]string{"status", "--porcelain", "--"}, changed...)...)
	st.Dir = dir
	if out, err := st.Output(); err == nil && strings.TrimSpace(string(out)) == "" {
		return changed, nil // back to what the repo has committed: nothing to commit
	}
	// only these files: what else the repo has staged or changed stays as it is
	return changed, git(append([]string{"commit", "-q", "-m", "Đồng bộ mẫu " + ref + " của agent-office", "--only", "--"}, changed...)...)
}
