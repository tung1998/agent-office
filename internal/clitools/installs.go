package clitools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// The installs a CLI's updates reach (ADR-279): the binaries office's
// connections run, not only the first on PATH. A CLI no connection uses is
// neither shown on the update card nor watched; two connections on two
// copies of the same CLI (one with a path of its own) get both updated.

// Install is one copy of a CLI that connections run.
type Install struct {
	Path      string `json:"path"`
	Version   string `json:"version,omitempty"`
	Outdated  bool   `json:"outdated,omitempty"`
	UpdateCmd string `json:"update_command,omitempty"`
}

// SetUses tells the manager the binaries the enabled connections run, per
// tool id (absolute paths). Without it every installed CLI counts as used,
// on the copy found first on PATH.
func (m *Manager) SetUses(fn func(ctx context.Context) map[string][]string) { m.uses = fn }

// target is one install an update runs on.
type target struct {
	path string
	meth Method
	argv []string
	dflt bool // the copy found first on PATH
}

// installs are the copies of t its connections run, each once (links
// followed), the one first on PATH first; used says whether any connection
// runs t at all (always, without SetUses, once installed).
func (m *Manager) installs(ctx context.Context, t Tool) (paths []string, used bool) {
	dflt := m.lookPath(t.Bin)
	if m.uses == nil {
		if dflt == "" {
			return nil, false
		}
		return []string{dflt}, true
	}
	bins := m.uses(ctx)[t.ID]
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || !executable(p) {
			return
		}
		k := realPath(p)
		if seen[k] {
			return
		}
		seen[k] = true
		paths = append(paths, p)
	}
	if dflt != "" && containsReal(bins, dflt) {
		add(dflt)
	}
	for _, b := range bins {
		add(b)
	}
	return paths, len(bins) > 0
}

// targets is what an update of t runs: per install, the way it was
// installed (the copy first on PATH when no connection runs t); an install
// whose way is unknown is left out (unknown says how many were).
func (m *Manager) targets(ctx context.Context, t Tool) (out []target, unknown int) {
	paths, _ := m.installs(ctx, t)
	if len(paths) == 0 { // asked for by name (a bot, an agent) though no connection runs it
		if p := m.lookPath(t.Bin); p != "" {
			paths = []string{p}
		}
	}
	dflt := realPath(m.lookPath(t.Bin))
	for _, p := range paths {
		meth, ok := updateMethod(t, p)
		if !ok {
			unknown++
			continue
		}
		isDflt := realPath(p) == dflt
		out = append(out, target{path: p, meth: meth, argv: m.updateArgv(t, meth, p, isDflt), dflt: isDflt})
	}
	return out, unknown
}

// updateArgv is meth's update for the copy at path: the copy first on PATH
// takes it as it is; another one is named itself (its own binary for the
// official installer, its own npm prefix for npm), or the update would land
// on the first copy again.
func (m *Manager) updateArgv(t Tool, meth Method, path string, dflt bool) []string {
	if dflt || len(meth.update) == 0 {
		return meth.update
	}
	switch meth.ID {
	case "native":
		if meth.update[0] == t.Bin {
			return append([]string{path}, meth.update[1:]...)
		}
	case "npm":
		if argv := npmAt(path, pkgs[t.ID].npm, "latest"); argv != nil {
			return argv
		}
	}
	return meth.update
}

// npmAt installs pkg@version into the npm prefix the binary at path lives
// in (nil when path is no npm global install, or pkg is not on npm).
func npmAt(path, pkg, version string) []string {
	prefix := npmPrefix(path)
	if prefix == "" || pkg == "" {
		return nil
	}
	npm := filepath.Join(prefix, "bin", "npm")
	if !executable(npm) {
		npm = "npm"
	}
	return []string{npm, "install", "-g", "--prefix", prefix, pkg + "@" + version}
}

// npmPrefix is the npm prefix of a global install at path ("" = none):
// <prefix>/lib/node_modules/<pkg>/….
func npmPrefix(path string) string {
	real := realPath(path)
	if i := strings.Index(real, "/lib/node_modules/"); i > 0 {
		return real[:i]
	}
	return ""
}

func executable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// realPath is p with its links followed ("" stays "").
func realPath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func containsReal(paths []string, p string) bool {
	r := realPath(p)
	for _, x := range paths {
		if realPath(x) == r {
			return true
		}
	}
	return false
}

// statusInstalls fills s.Used and, with more than one copy in use,
// s.Installs; a copy behind the newest version makes s outdated.
func (m *Manager) statusInstalls(ctx context.Context, t Tool, s *Status) {
	paths, used := m.installs(ctx, t)
	s.Used = used
	if len(paths) == 0 || len(paths) == 1 && realPath(paths[0]) == realPath(s.Path) {
		return
	}
	for _, tg := range paths {
		in := Install{Path: tg}
		if tg == s.Path {
			in.Version = VersionOf(s.Version)
		} else {
			in.Version = m.versionAt(tg)
		}
		in.Outdated = Newer(in.Version, s.Latest)
		if meth, ok := updateMethod(t, tg); ok {
			in.UpdateCmd = displayCommand(m.updateArgv(t, meth, tg, realPath(tg) == realPath(s.Path)))
		}
		s.Outdated = s.Outdated || in.Outdated
		s.Installs = append(s.Installs, in)
	}
}

// brokenOf is why one of the copies at paths does not start ("" = all do);
// with several, it names the copy.
func (m *Manager) brokenOf(t Tool, paths []string) string {
	for _, p := range paths {
		if why := m.brokenAt(t, p); why != "" {
			if len(paths) > 1 {
				return p + ": " + why
			}
			return why
		}
	}
	return ""
}

// oldest is the lowest version the copies at paths say: the one an update
// is told for.
func (m *Manager) oldest(paths []string) string {
	low := ""
	for _, p := range paths {
		v := m.versionAt(p)
		if low == "" || Newer(v, low) { // v is older than low
			low = v
		}
	}
	return low
}
