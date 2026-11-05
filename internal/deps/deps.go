// Package deps finds the dependency changes of a diff (package.json, a
// lockfile, uv.lock, requirements) and installs them in a folder (ADR-251):
// in an agent's worktree, where they go into a folder of its own, or in the
// project once the diff is merged.
package deps

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/ops"
)

// Change is a package whose dependencies a diff changes: Dir is where its
// install runs, relative to the project ("." = the top), Manager what runs it
// (pnpm, npm, yarn, bun, uv, pip; "" = not known from the files alone).
// Files (pip only) are the requirements files it installs, relative to Dir,
// when not requirements.txt: the ones the diff changed.
type Change struct {
	Dir     string   `json:"dir"`
	Manager string   `json:"manager,omitempty"`
	Files   []string `json:"files,omitempty"`
}

// defaultRequirements is the file pip installs when a change names none.
const defaultRequirements = "requirements.txt"

// requirementsFile: f (a path relative to the project) is a pip
// requirements file: requirements*.txt, or a .txt in a requirements/
// folder. Its package folder and its path from there.
func requirementsFile(f string) (dir, rel string, ok bool) {
	name, d := path.Base(f), path.Dir(f)
	switch {
	case strings.HasPrefix(name, "requirements") && strings.HasSuffix(name, ".txt"):
		return d, name, true
	case path.Base(d) == "requirements" && strings.HasSuffix(name, ".txt"):
		return path.Dir(d), "requirements/" + name, true
	}
	return "", "", false
}

// lockfiles name the manager their file belongs to.
var lockfiles = map[string]string{
	"pnpm-lock.yaml": "pnpm", "package-lock.json": "npm", "yarn.lock": "yarn",
	"bun.lock": "bun", "bun.lockb": "bun", "uv.lock": "uv",
}

// kindOf: "node", "python" or "" (not a dependency file).
func kindOf(name string) string {
	switch {
	case name == "package.json":
		return "node"
	case name == "uv.lock" || name == "pyproject.toml":
		return "python"
	case lockfiles[name] != "":
		return "node"
	}
	return ""
}

// Changes lists the packages files (a diff's, relative to the project)
// change the dependencies of. root, when given, is the folder they are read
// in: a package.json of a workspace installs from the folder above it with
// the lockfile, and the manager is read from that lockfile. pyproject.toml
// counts only next to a uv.lock (else it is any tool's settings).
func Changes(root string, files []string) []Change {
	var out []Change
	add := func(c Change) {
		if i := slices.IndexFunc(out, func(x Change) bool { return x.Dir == c.Dir }); i >= 0 {
			if out[i].Manager == "" {
				out[i].Manager = c.Manager
			}
			if out[i].Manager != "pip" {
				out[i].Files = nil
				return
			}
			// both pip: every requirements file of both (none named = the default)
			files := reqFiles(out[i])
			for _, f := range reqFiles(c) {
				if !slices.Contains(files, f) {
					files = append(files, f)
				}
			}
			out[i].Files = files
			if len(files) == 1 && files[0] == defaultRequirements {
				out[i].Files = nil
			}
			return
		}
		if c.Manager != "pip" {
			c.Files = nil
		}
		out = append(out, c)
	}
	for _, f := range files {
		f = path.Clean(filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(f), "./")))
		if strings.Contains("/"+f, "/node_modules/") || strings.Contains("/"+f, "/.venv/") {
			continue
		}
		name, dir := path.Base(f), path.Dir(f)
		if d, rel, ok := requirementsFile(f); ok {
			c := Change{Dir: d, Manager: "pip"}
			if root != "" && exists(filepath.Join(root, d, "uv.lock")) {
				c.Manager = "uv"
			}
			if rel != defaultRequirements {
				c.Files = []string{rel}
			}
			add(c)
			continue
		}
		switch kindOf(name) {
		case "node":
			if root != "" {
				dir = nodeRoot(root, dir)
			}
			m := lockfiles[name]
			if m == "" && root != "" {
				if _, err := os.Stat(filepath.Join(root, dir, "package.json")); err == nil {
					m = ops.PackageManager(filepath.Join(root, dir))
				}
			}
			add(Change{Dir: dir, Manager: m})
		case "python":
			switch {
			case name == "uv.lock":
				add(Change{Dir: dir, Manager: "uv"})
			case name == "pyproject.toml":
				if root != "" && exists(filepath.Join(root, dir, "uv.lock")) || slices.Contains(files, path.Join(dir, "uv.lock")) {
					add(Change{Dir: dir, Manager: "uv"})
				}
			}
		}
	}
	return out
}

// nodeRoot is the folder a package's install runs from: the nearest one
// up from dir (within root) with a lockfile, or dir itself.
func nodeRoot(root, dir string) string {
	for d := dir; ; d = path.Dir(d) {
		for f := range lockfiles {
			if f != "uv.lock" && exists(filepath.Join(root, d, f)) {
				return d
			}
		}
		if d == "." || d == "/" {
			return dir
		}
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Folder is what an install of manager fills in its package folder.
func Folder(manager string) string {
	if manager == "uv" || manager == "pip" {
		return ".venv"
	}
	return "node_modules"
}

// Command is the install that runs for c, non-interactive (pnpm never asks
// to remove a node_modules it did not make). "" = none known.
func Command(c Change) []string {
	switch c.Manager {
	case "pnpm":
		return []string{"pnpm", "install", "--config.confirmModulesPurge=false"}
	case "npm":
		return []string{"npm", "install", "--no-audit", "--no-fund"}
	case "yarn":
		return []string{"yarn", "install"}
	case "bun":
		return []string{"bun", "install"}
	case "uv":
		return []string{"uv", "sync"}
	case "pip":
		argv := []string{filepath.Join(".venv", "bin", "python"), "-m", "pip", "install"}
		for _, f := range reqFiles(c) {
			argv = append(argv, "-r", f)
		}
		return argv
	}
	return nil
}

// reqFiles are the requirements files a pip change installs.
func reqFiles(c Change) []string {
	if len(c.Files) == 0 {
		return []string{defaultRequirements}
	}
	return c.Files
}

// Label is c as a card shows it: "dashboard (pnpm)", "ml (pip:
// requirements-dev.txt)".
func (c Change) Label() string {
	if c.Manager == "" {
		return c.Dir
	}
	if c.Manager == "pip" && len(c.Files) > 0 {
		return c.Dir + " (pip: " + strings.Join(c.Files, ", ") + ")"
	}
	return c.Dir + " (" + c.Manager + ")"
}

// Timeout bounds one install.
var Timeout = 15 * time.Minute

// outMax bounds the output an install keeps (its end: where errors are).
const outMax = 8000

// ErrNoManager: what installs this package is not known.
var ErrNoManager = errors.New("không rõ trình quản lý gói (pnpm, npm, uv…) của thư mục này")

// envKeep are the variables an install keeps of office's environment (and
// those with envPrefixes): what the package tools need to find themselves,
// their store and caches. Office's own settings and secrets (API keys,
// tokens) never reach a package's install scripts.
var (
	envKeep     = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP", "LANG", "TERM", "PNPM_HOME", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}
	envPrefixes = []string{"LC_", "XDG_", "npm_config_", "NPM_CONFIG_", "COREPACK_", "VOLTA_", "NVM_", "UV_", "PIP_"}
)

// Env is the environment an install runs with: office's, kept to envKeep,
// plus CI=true (no tool asks anything).
func Env() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		keep := slices.Contains(envKeep, k)
		for _, p := range envPrefixes {
			keep = keep || strings.HasPrefix(k, p)
		}
		if keep && k != "CI" {
			out = append(out, kv)
		}
	}
	return append(out, "CI=true")
}

// Install runs c's install in root (the project or a worktree) and returns
// the end of its output.
func Install(ctx context.Context, root string, c Change) (string, error) {
	if c.Manager == "" {
		c.Manager = ops.PackageManager(filepath.Join(root, c.Dir))
	}
	argv := Command(c)
	if len(argv) == 0 {
		return "", ErrNoManager
	}
	dir := filepath.Join(root, filepath.FromSlash(c.Dir))
	if rel, err := filepath.Rel(root, dir); err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("thư mục gói nằm ngoài project")
	}
	for _, f := range c.Files {
		if f = path.Clean(filepath.ToSlash(f)); f == ".." || strings.HasPrefix(f, "../") || path.IsAbs(f) || strings.HasPrefix(f, "-") {
			return "", errors.New("file requirements nằm ngoài thư mục gói: " + f)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var buf bytes.Buffer
	run := func(name string, args ...string) error {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = dir
		cmd.Env = Env()
		cmd.Stdout, cmd.Stderr = &buf, &buf
		return cmd.Run()
	}
	var err error
	if c.Manager == "pip" && !exists(filepath.Join(dir, ".venv", "bin", "python")) {
		// a worktree's own virtualenv (its link to the project's was taken
		// away), or a package that never had one: made first
		err = run("python3", "-m", "venv", ".venv")
	}
	if err == nil {
		if c.Manager == "pip" {
			err = run(filepath.Join(dir, argv[0]), argv[1:]...)
		} else {
			err = run(argv[0], argv[1:]...)
		}
	}
	out := strings.TrimSpace(buf.String())
	if len(out) > outMax {
		out = "…\n" + out[len(out)-outMax:]
	}
	if ctx.Err() == context.DeadlineExceeded {
		err = errors.New("cài thư viện quá " + Timeout.String())
	}
	return out, err
}
