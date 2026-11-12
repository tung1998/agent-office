package fullexport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Version of the archive format.
const Version = 1

// Archive layout.
const (
	manifestName = "manifest.json"
	dbName       = "data/office.db"
	keyName      = "data/key"
	homePrefix   = "home/"
	codePrefix   = "code/"
)

// How a project's code travels.
const (
	KindRemote  = "remote"    // its remote, branch, commit; unpushed as a bundle; uncommitted as a patch
	KindGit     = "local-git" // no remote: a bundle of its whole history
	KindPlain   = "plain"     // no git: its files
	KindMissing = "missing"   // its folder is gone on this machine
	KindHelper  = "helper"    // a machine-wide helper, no folder
)

// Manifest is what an export holds, read first on import.
type Manifest struct {
	Version       int       `json:"version"`
	Kind          string    `json:"kind,omitempty"` // "" = the whole office, KindProjects = chosen projects
	OfficeVersion string    `json:"office_version,omitempty"`
	ExportedAt    time.Time `json:"exported_at"`
	HomeDir       string    `json:"home_dir"`               // the office's data folder there
	HomeAliases   []string  `json:"home_aliases,omitempty"` // the same folder by another path (a symlink: /var → /private/var)
	ProjectRoot   string    `json:"project_root,omitempty"` // the project owning it (local mode)
	Projects      []Project `json:"projects"`
}

// Project is one project's code in the archive.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Slug      string `json:"slug,omitempty"`
	Kind      string `json:"kind"`
	Own       bool   `json:"own,omitempty"` // the project owning the office's data: it goes to the new office's
	Remote    string `json:"remote,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Head      string `json:"head,omitempty"`
	Bundle    bool   `json:"bundle,omitempty"`    // code/<slug>/repo.bundle
	Patch     bool   `json:"patch,omitempty"`     // code/<slug>/changes.patch
	Untracked int    `json:"untracked,omitempty"` // files under code/<slug>/untracked/
	Files     int    `json:"files,omitempty"`     // files under code/<slug>/files/ (plain)
	Note      string `json:"note,omitempty"`
}

// ProjectRef is a project to export.
type ProjectRef struct{ ID, Name, Path string }

// ExportOptions says what to export.
type ExportOptions struct {
	HomeDir, ProjectRoot string
	OfficeVersion        string
	KeyPath              string                        // the office's encryption key
	Snapshot             func(dest string) error       // a consistent copy of the database at dest
	Projects             []ProjectRef                  // every project of the office
	Passphrase           string                        // encrypts the archive
	Log                  func(format string, a ...any) // progress, nil = quiet
}

// homeDirs are the folders of the office's data that go (worktrees, logs,
// backups and trash stay: copies, history or what was thrown away).
var homeDirs = []string{"attachments", "library", "assistant", "backgrounds", "accounts"}

// accountSkip: what of an AI account's folder stays behind. Its logins (the
// other machine signs in again) and what a run leaves (caches, sessions,
// shell snapshots); its settings, instructions and hooks go.
var accountSkip = []string{
	".credentials.json", ".claude.json", "auth.json", "oauth_creds.json", "google_accounts.json",
	"cache", "session-env", "shell-snapshots", "sessions", "backups", "state", "statsig", "telemetry", "logs", "todos",
	"mcp-needs-auth-cache.json", ".last-cleanup",
}

func skipAccount(rel string, d fs.DirEntry) bool {
	parts := strings.Split(rel, "/")
	if len(parts) < 2 { // accounts/<id> itself
		return false
	}
	name := parts[len(parts)-1]
	if len(parts) == 2 && (slices.Contains(accountSkip, name) || strings.HasPrefix(name, "policy-limits") || strings.HasPrefix(name, "remote-settings")) {
		return true
	}
	return strings.HasSuffix(name, ".lock")
}

// plainSkip: what a folder without git leaves out (what a build or an
// install makes again).
var plainSkip = []string{"node_modules", ".venv", "venv", "__pycache__", "dist", ".nuxt", ".output", ".next", "target", ".cache", ".turbo", ".DS_Store", ".office"}

// Export writes the archive to w.
func Export(ctx context.Context, w io.Writer, o ExportOptions) (Manifest, error) {
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	m := Manifest{Version: Version, OfficeVersion: o.OfficeVersion, ExportedAt: time.Now().UTC(), HomeDir: o.HomeDir, ProjectRoot: o.ProjectRoot}
	if r := realDir(o.HomeDir); r != o.HomeDir {
		m.HomeAliases = []string{r}
	}
	tmp, err := os.MkdirTemp("", "office-export-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)

	// the code first, into tmp: the manifest that leads the archive says what it holds
	type pending struct{ name, path string }
	var files []pending
	used := map[string]bool{}
	for _, ref := range o.Projects {
		if err := ctx.Err(); err != nil {
			return m, err
		}
		p := Project{ID: ref.ID, Name: ref.Name, Path: ref.Path}
		if ref.Path == "" {
			p.Kind = KindHelper
			m.Projects = append(m.Projects, p)
			continue
		}
		p.Slug = uniqueSlug(ref.Name, used)
		p.Own = o.ProjectRoot != "" && realDir(ref.Path) == realDir(o.ProjectRoot)
		dir := filepath.Join(tmp, p.Slug)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return m, err
		}
		logf("• %s (%s)", ref.Name, ref.Path)
		if err := describeProject(ctx, &p, dir, o.HomeDir); err != nil {
			return m, fmt.Errorf("%s: %w", ref.Name, err)
		}
		m.Projects = append(m.Projects, p)
		if p.Bundle {
			files = append(files, pending{codePrefix + p.Slug + "/repo.bundle", filepath.Join(dir, "repo.bundle")})
		}
		if p.Patch {
			files = append(files, pending{codePrefix + p.Slug + "/changes.patch", filepath.Join(dir, "changes.patch")})
		}
	}

	aw, err := newArchiveWriter(w, o.Passphrase)
	if err != nil {
		return m, err
	}
	man, _ := json.MarshalIndent(m, "", "  ")
	if err := aw.addBytes(manifestName, man); err != nil {
		return m, err
	}
	logf("• database")
	dbCopy := filepath.Join(tmp, "office.db")
	if err := o.Snapshot(dbCopy); err != nil {
		return m, fmt.Errorf("chụp database: %w", err)
	}
	if err := aw.addFile(dbName, dbCopy); err != nil {
		return m, err
	}
	_ = os.Remove(dbCopy)
	if o.KeyPath != "" {
		if _, err := os.Stat(o.KeyPath); err == nil {
			if err := aw.addFile(keyName, o.KeyPath); err != nil {
				return m, err
			}
		}
	}
	for _, d := range homeDirs {
		var skip func(string, fs.DirEntry) bool
		if d == "accounts" {
			skip = skipAccount
		}
		n, err := aw.addTree(filepath.Join(o.HomeDir, d), homePrefix+d, false, skip)
		if err != nil {
			return m, err
		}
		if n > 0 {
			logf("• %s: %d file", d, n)
		}
	}
	for _, f := range files {
		if err := aw.addFile(f.name, f.path); err != nil {
			return m, err
		}
	}
	for _, p := range m.Projects {
		switch {
		case p.Kind == KindPlain:
			root := p.Path
			if _, err := aw.addTree(root, codePrefix+p.Slug+"/files", true, func(rel string, d fs.DirEntry) bool {
				return slices.Contains(plainSkip, d.Name()) || inside(filepath.Join(root, filepath.FromSlash(rel)), o.HomeDir)
			}); err != nil {
				return m, err
			}
		case p.Untracked > 0:
			for _, rel := range untrackedFiles(ctx, p.Path, o.HomeDir) {
				if err := aw.addFile(codePrefix+p.Slug+"/untracked/"+rel, filepath.Join(p.Path, filepath.FromSlash(rel))); err != nil {
					return m, err
				}
			}
		}
	}
	return m, aw.Close()
}

// describeProject fills p's kind and git state, writing its bundle and patch
// into dir.
// home (the office's data folder) never goes as code, even inside a project
// that does not ignore it: it holds the database and the key.
func describeProject(ctx context.Context, p *Project, dir, home string) error {
	if _, err := os.Stat(p.Path); err != nil {
		p.Kind, p.Note = KindMissing, "thư mục không còn trên máy"
		return nil
	}
	if out, err := git(ctx, p.Path, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		p.Kind = KindPlain
		n, err := countFiles(p.Path, home)
		p.Files = n
		return err
	}
	if out, err := git(ctx, p.Path, "remote"); err == nil {
		if names := strings.Fields(out); len(names) > 0 {
			name := names[0]
			if slices.Contains(names, "origin") {
				name = "origin"
			}
			url, _ := git(ctx, p.Path, "remote", "get-url", name)
			p.Remote = strings.TrimSpace(url)
		}
	}
	if b, err := git(ctx, p.Path, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		p.Branch = strings.TrimSpace(b)
	}
	if h, err := git(ctx, p.Path, "rev-parse", "HEAD"); err == nil {
		p.Head = strings.TrimSpace(h)
	}
	bundle := filepath.Join(dir, "repo.bundle")
	if p.Remote != "" {
		p.Kind = KindRemote
		// what no remote has: every local branch's commits not pushed
		if p.Head != "" {
			if _, err := git(ctx, p.Path, "bundle", "create", bundle, "--branches", "--not", "--remotes"); err == nil {
				p.Bundle = true
			}
		}
	} else {
		p.Kind = KindGit
		if p.Head != "" {
			if out, err := git(ctx, p.Path, "bundle", "create", bundle, "--all"); err != nil {
				return fmt.Errorf("git bundle: %s", strings.TrimSpace(out))
			}
			p.Bundle = true
		}
	}
	if p.Head != "" {
		diff, err := gitRaw(ctx, p.Path, "diff", "--binary", "HEAD")
		if err != nil {
			return fmt.Errorf("git diff: %w", err)
		}
		if len(diff) > 0 {
			if err := os.WriteFile(filepath.Join(dir, "changes.patch"), diff, 0o600); err != nil {
				return err
			}
			p.Patch = true
		}
	}
	p.Untracked = len(untrackedFiles(ctx, p.Path, home))
	return nil
}

// untrackedFiles: the files git does not track nor ignore, relative,
// slash-separated; none under home.
func untrackedFiles(ctx context.Context, dir, home string) []string {
	out, err := gitRaw(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" {
			continue
		}
		abs := filepath.Join(dir, filepath.FromSlash(f))
		if inside(abs, home) {
			continue
		}
		if st, err := os.Lstat(abs); err == nil && st.Mode().IsRegular() {
			files = append(files, f)
		}
	}
	return files
}

func countFiles(root, home string) (int, error) {
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p != root && (slices.Contains(plainSkip, d.Name()) || inside(p, home)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n, err
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// gitRaw is git's stdout only (a patch or a NUL list must not get stderr).
func gitRaw(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Output()
}

var slugBad = regexp.MustCompile(`[^a-z0-9._-]+`)

// uniqueSlug: an archive folder name for a project called name.
func uniqueSlug(name string, used map[string]bool) string {
	s := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(name), "-"), "-.")
	if s == "" {
		s = "project"
	}
	base := s
	for i := 2; used[s]; i++ {
		s = fmt.Sprintf("%s-%d", base, i)
	}
	used[s] = true
	return s
}

// inside: p is dir or within it (dir "" is nothing), by their real paths.
func inside(p, dir string) bool {
	if dir == "" {
		return false
	}
	for _, pp := range []string{filepath.Clean(p), realDir(p)} {
		for _, d := range []string{filepath.Clean(dir), realDir(dir)} {
			if pp == d || strings.HasPrefix(pp, d+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// realDir is p with its symlinks resolved (p itself when it cannot be).
func realDir(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
