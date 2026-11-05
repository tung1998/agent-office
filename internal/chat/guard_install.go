package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// installVerbs are the subcommands of each package tool that write its
// dependency folder (ADR-251). yarn with none installs too.
var installVerbs = map[string][]string{
	"npm":  {"install", "i", "in", "ci", "add", "update", "up", "upgrade", "uninstall", "un", "remove", "rm", "r", "dedupe", "prune", "link", "rebuild"},
	"pnpm": {"install", "i", "add", "update", "up", "upgrade", "uninstall", "un", "remove", "rm", "dedupe", "prune", "link", "rebuild", "import"},
	"yarn": {"install", "add", "upgrade", "up", "remove", "dedupe", "link"},
	"bun":  {"install", "i", "add", "remove", "rm", "update", "link"},
	"uv":   {"sync", "add", "remove"},
	"pip":  {"install", "uninstall"},
}

// dirFlags take the folder a tool works in; valueFlags take a value to skip.
var (
	dirFlags   = []string{"-C", "--dir", "--prefix", "--cwd", "--directory", "--project"}
	valueFlags = []string{"--filter", "-F", "--registry", "--python", "-p", "--index-url", "-i", "--extra-index-url", "--workspace", "-w", "--target", "-t"}
)

// installInLinkedTree: a shell command that installs packages (pnpm/npm/
// yarn/bun install, uv sync, pip install…) where the dependency folder is
// a link to the project's (a worktree's, ADR-251): it would write into the
// project's libraries. Why it is refused, or "". linked tells whether, from
// dir, the folder (node_modules, .venv) is the project's. Best effort on the
// text, segment by segment, following cd and -C/--dir/--prefix.
func installInLinkedTree(cmd, cwd string, linked func(dir, folder string) bool) string {
	split := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "(", "\n", ")", "\n", "`", "\n", "$(", "\n")
	dir := cwd
	at := func(p string) string {
		p = strings.Trim(p, `"'`)
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Join(dir, p)
	}
	for _, seg := range strings.Split(split.Replace(cmd), "\n") {
		f := strings.Fields(seg)
		i := 0 // the command itself: past VAR=x, sudo, env, command
		for i < len(f) && (strings.Contains(f[i], "=") && !strings.HasPrefix(f[i], "-") || f[i] == "sudo" || f[i] == "env" || f[i] == "command" || f[i] == "exec") {
			i++
		}
		if i >= len(f) {
			continue
		}
		prog := strings.Trim(f[i], `"'`)
		if prog == "cd" {
			if i+1 < len(f) {
				dir = at(f[i+1])
			}
			continue
		}
		tool, base, args := "", filepath.Base(prog), f[i+1:]
		where := dir
		if v, _, ok := strings.Cut(prog, ".venv/"); ok && strings.Contains(prog, ".venv/") { // a virtualenv's own pip or python
			where = at(strings.TrimSuffix(v, "/"))
		}
		switch {
		case base == "pip" || base == "pip3":
			tool = "pip"
		case strings.HasPrefix(base, "python") && len(args) >= 2 && args[0] == "-m" && (args[1] == "pip" || args[1] == "pip3"):
			tool, args = "pip", args[2:]
		case installVerbs[base] != nil:
			tool = base
		default:
			continue
		}
		verb := ""
		var rest []string
		for j := 0; j < len(args); j++ {
			a := strings.Trim(args[j], `"'`)
			name, val, eq := strings.Cut(a, "=")
			switch {
			case contains(dirFlags, a) && j+1 < len(args):
				where = at(args[j+1])
				j++
			case eq && contains(dirFlags, name):
				where = at(val)
			case contains(valueFlags, a):
				j++
			case strings.HasPrefix(a, "-"):
			case verb == "":
				verb = a
			default:
				rest = append(rest, a)
			}
		}
		installs := contains(installVerbs[tool], verb) || tool == "yarn" && verb == ""
		if tool == "uv" && verb == "pip" {
			installs = len(rest) > 0 && (rest[0] == "install" || rest[0] == "uninstall" || rest[0] == "sync")
		}
		if !installs {
			continue
		}
		folder := "node_modules"
		if tool == "uv" || tool == "pip" {
			folder = ".venv"
		}
		if linked(where, folder) {
			return "không cài thư viện bằng lệnh trong worktree này: " + folder + " của nó là link sang project, cài vào sẽ ghi đè thư viện của project. Dùng tool deps_install (worktree có thư viện riêng rồi cài), hoặc nhờ người cài trong project" // i18n-ignore
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// linkedDeps: dir is in a git worktree (not the main checkout) and the
// folder (node_modules, .venv) there or in a folder above it, up to the
// worktree's top, is a link: the project's, which an install would write.
func linkedDeps(dir, folder string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-dir", "--git-common-dir").Output()
	if err != nil {
		return false
	}
	l := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(l) != 3 || filepath.Clean(l[1]) == filepath.Clean(l[2]) {
		return false // the main checkout: its libraries are its own
	}
	top := filepath.Clean(l[0])
	d := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		d = r
	}
	for {
		if fi, err := os.Lstat(filepath.Join(d, folder)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return true
		}
		parent := filepath.Dir(d)
		if d == top || parent == d {
			return false
		}
		d = parent
	}
}
