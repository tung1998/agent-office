package repos

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DirEntry is one sub-folder shown in the folder picker.
type DirEntry struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Markers   []string `json:"markers"`    // project files found: .git, package.json, go.mod…
	IsProject bool     `json:"is_project"` // has at least one marker
	HasChild  bool     `json:"has_children"`
}

// Listing is the content of one folder.
type Listing struct {
	Path      string     `json:"path"`
	Parent    string     `json:"parent"` // "" at the filesystem root
	Entries   []DirEntry `json:"entries"`
	Truncated bool       `json:"truncated"`
}

// ErrNotDir means the path is not a readable directory.
var ErrNotDir = errors.New("không phải thư mục")

var projectMarkers = []string{".git", "package.json", "go.mod", "composer.json", "pyproject.toml", "requirements.txt", "Cargo.toml", "pom.xml", "build.gradle", "Gemfile", "Dockerfile"}

// skipDirs are never useful as projects and are often huge.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true, "__pycache__": true, "Library": true, ".Trash": true}

const maxEntries = 500

// ListDirs returns the sub-folders of dir (names only, no file contents).
func ListDirs(dir string, showHidden bool) (Listing, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Listing{}, err
	}
	abs = filepath.Clean(abs)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real // match paths stored by Detect, which resolves symlinks too
	}
	st, err := os.Stat(abs)
	if err != nil {
		return Listing{}, err
	}
	if !st.IsDir() {
		return Listing{}, ErrNotDir
	}
	items, err := os.ReadDir(abs)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Path: abs, Entries: []DirEntry{}}
	if parent := filepath.Dir(abs); parent != abs {
		out.Parent = parent
	}
	for _, it := range items {
		name := it.Name()
		if (!showHidden && strings.HasPrefix(name, ".")) || skipDirs[name] {
			continue
		}
		full := filepath.Join(abs, name)
		if !isDir(it, full) {
			continue
		}
		if len(out.Entries) >= maxEntries {
			out.Truncated = true
			break
		}
		e := DirEntry{Name: name, Path: full, Markers: []string{}}
		for _, m := range projectMarkers {
			if _, err := os.Stat(filepath.Join(full, m)); err == nil {
				e.Markers = append(e.Markers, m)
			}
		}
		e.IsProject = len(e.Markers) > 0
		e.HasChild = hasSubdir(full, showHidden)
		out.Entries = append(out.Entries, e)
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}

func isDir(it os.DirEntry, full string) bool {
	if it.IsDir() {
		return true
	}
	if it.Type()&os.ModeSymlink != 0 {
		st, err := os.Stat(full)
		return err == nil && st.IsDir()
	}
	return false
}

// hasSubdir peeks at a folder to decide whether to show an expand arrow.
func hasSubdir(dir string, showHidden bool) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	for {
		items, err := f.ReadDir(64)
		for _, it := range items {
			name := it.Name()
			if (!showHidden && strings.HasPrefix(name, ".")) || skipDirs[name] {
				continue
			}
			if isDir(it, filepath.Join(dir, name)) {
				return true
			}
		}
		if err != nil || len(items) == 0 {
			return false
		}
	}
}

// Shortcut is a quick-jump location in the picker.
type Shortcut struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

// Shortcuts returns common starting folders that exist on this machine.
func Shortcuts(extra ...Shortcut) []Shortcut {
	var out []Shortcut
	seen := map[string]bool{}
	add := func(label, p string) {
		if p == "" || seen[p] {
			return
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			seen[p] = true
			out = append(out, Shortcut{Label: label, Path: p})
		}
	}
	for _, e := range extra {
		add(e.Label, e.Path)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add("Home", home)
		add("Desktop", filepath.Join(home, "Desktop"))
		add("Documents", filepath.Join(home, "Documents"))
		add("Projects", filepath.Join(home, "Projects"))
		add("code", filepath.Join(home, "code"))
	}
	add("/", "/")
	return out
}
