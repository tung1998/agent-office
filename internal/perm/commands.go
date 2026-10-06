package perm

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Pack is a named set of commands (e.g. "Go": go test, go vet…). Packs only
// group commands for picking; a project enables commands one by one.
type Pack struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Icon     string   `json:"icon,omitempty"`
	Commands []string `json:"commands"`
	Custom   bool     `json:"custom,omitempty"`
}

// A command pattern is a command line, optionally ending in " *" for "with any
// arguments". Commands run without a shell, so pipes, redirects, ";", "&&"
// and "$" are never allowed.
const shellChars = ";&|<>$`\\\n\r"

var ErrCommand = errors.New("lệnh không hợp lệ")

// SplitCommand splits a command line into arguments (single/double quotes).
func SplitCommand(line string) ([]string, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, shellChars) {
		return nil, ErrCommand
	}
	var (
		args  []string
		cur   strings.Builder
		quote rune
		has   bool
	)
	for _, r := range line {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, has = r, true
		case r == ' ' || r == '\t':
			if cur.Len() > 0 || has {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, ErrCommand
	}
	if cur.Len() > 0 || has {
		args = append(args, cur.String())
	}
	return args, nil
}

// JoinCommand is the inverse of SplitCommand: arguments with spaces or quotes
// are quoted so that SplitCommand(JoinCommand(args)) gives args back.
func JoinCommand(args []string) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && !strings.ContainsAny(arg, " \t'\"") {
			parts[i] = arg
			continue
		}
		var b strings.Builder
		if arg == "" {
			b.WriteString(`""`)
		}
		for _, seg := range strings.SplitAfter(arg, `"`) {
			body, quoted := strings.CutSuffix(seg, `"`)
			if body != "" {
				b.WriteString(`"` + body + `"`)
			}
			if quoted {
				b.WriteString(`'"'`)
			}
		}
		parts[i] = b.String()
	}
	return strings.Join(parts, " ")
}

// CleanPattern normalises a pattern (single spaces) or rejects it.
func CleanPattern(p string) (string, error) {
	p = strings.Join(strings.Fields(p), " ")
	base, _ := strings.CutSuffix(p, " *")
	if _, err := SplitCommand(base); err != nil || strings.Contains(base, "*") {
		return "", ErrCommand
	}
	return p, nil
}

// riskyFlags are flags that turn a command that reads or checks into one that
// writes files or runs a program of the caller's choice (go test -exec, git
// diff --output=, pytest -p/--basetemp…). "pattern *" never allows them: a
// command line naming one needs its own exact pattern.
var riskyFlags = map[string][]string{
	"go": {"exec", "toolexec", "o", "c", "C", "overlay", "modfile", "pkgdir", "pgo",
		"coverprofile", "cpuprofile", "memprofile", "blockprofile", "mutexprofile", "trace", "outputdir"},
	"git": {"output", "no-index"},
	"pytest": {"basetemp", "config-file", "inifile", "rootdir", "confcutdir", "override-ini",
		"junitxml", "junit-xml", "resultlog", "result-log", "log-file", "debug"},
}

// shortFlags: one-letter flags of a command that combine ("-vp x"). A letter
// that takes a value ends the cluster: what follows it (or the next argument)
// is its value, not more flags ("-rp" is -r p, "-ktest_cache" is -k).
type shortFlags struct {
	risky    string          // letters that are risky
	value    string          // letters that take a value
	harmless map[rune]string // a risky letter whose value starts so is fine
}

func (s shortFlags) riskyIn(cluster, next string) bool {
	for i, r := range cluster {
		if !strings.ContainsRune(s.value, r) {
			if strings.ContainsRune(s.risky, r) {
				return true
			}
			continue
		}
		if !strings.ContainsRune(s.risky, r) {
			return false
		}
		rest := cluster[i+len(string(r)):]
		val := strings.TrimPrefix(rest, "=")
		if rest == "" {
			val = next
		}
		pre, ok := s.harmless[r]
		return !ok || !strings.HasPrefix(val, pre)
	}
	return false
}

// riskyShort: pytest -p loads a plugin of the caller's choice, -c/-o change
// its config; "-p no:x" only turns plugin x off.
var riskyShort = map[string]shortFlags{
	"pytest": {risky: "pco", value: "pcokmrWn", harmless: map[rune]string{'p': "no:"}},
}

// outside reports whether v names a path out of the project folder (the run's
// working directory): absolute, from the home folder, or climbing out with
// "..". A revision range ("HEAD~1..HEAD") is not a path that climbs out.
func outside(v string) bool {
	if v == "" {
		return false
	}
	if filepath.IsAbs(v) || strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~") {
		return true
	}
	c := filepath.ToSlash(filepath.Clean(v))
	return c == ".." || strings.HasPrefix(c, "../")
}

// riskyArg reports whether the arguments a " *" adds to args[0] carry a risky
// flag or, for a command that reads files (git, go, pytest), a path out of
// the project: "git diff --no-index /x ~/.ssh/id_rsa" would read any file.
func riskyArg(cmd string, extra []string) bool {
	flags := riskyFlags[cmd]
	short, combined := riskyShort[cmd]
	_, paths := riskyFlags[cmd]
	for i, a := range extra {
		if paths {
			v := a
			if strings.HasPrefix(a, "-") {
				_, v, _ = strings.Cut(a, "=")
			}
			if outside(v) {
				return true
			}
		}
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			continue
		}
		if combined && !strings.HasPrefix(a, "--") {
			next := ""
			if i+1 < len(extra) {
				next = extra[i+1]
			}
			if short.riskyIn(a[1:], next) {
				return true
			}
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if slices.Contains(flags, name) {
			return true
		}
		// git takes a long option by an unambiguous prefix ("--outp=x")
		if cmd == "git" && strings.HasPrefix(a, "--") && len(name) >= 4 &&
			slices.ContainsFunc(flags, func(f string) bool { return strings.HasPrefix(f, name) }) {
			return true
		}
	}
	return false
}

// MatchCommand returns the pattern that allows args, if any.
func MatchCommand(patterns []string, args []string) (string, bool) {
	line := strings.Join(args, " ")
	for _, p := range patterns {
		if base, ok := strings.CutSuffix(p, " *"); ok {
			if line == base || strings.HasPrefix(line, base+" ") {
				if n := len(strings.Fields(base)); len(args) > n && riskyArg(args[0], args[n:]) {
					continue
				}
				return p, true
			}
		} else if line == p {
			return p, true
		}
	}
	return "", false
}

// BuiltinPacks are offered in every project.
var BuiltinPacks = []Pack{
	{ID: "git-read", Label: "Git (đọc)", Icon: "i-lucide-git-branch", Commands: []string{"git status", "git diff *", "git log *", "git show *", "git branch"}},
	{ID: "go", Label: "Go", Icon: "i-simple-icons-go", Commands: []string{"go build ./...", "go vet ./...", "go test ./...", "go test *", "gofmt -l .", "go mod tidy"}},
	{ID: "node", Label: "Node", Icon: "i-simple-icons-nodedotjs", Commands: []string{"npm test", "npm run lint", "npm run build", "npx tsc --noEmit"}},
	{ID: "python", Label: "Python", Icon: "i-simple-icons-python", Commands: []string{"pytest", "pytest *", "ruff check .", "mypy ."}},
	{ID: "rust", Label: "Rust", Icon: "i-simple-icons-rust", Commands: []string{"cargo check", "cargo test", "cargo clippy", "cargo fmt --check"}},
	{ID: "docker-read", Label: "Docker (đọc)", Icon: "i-simple-icons-docker", Commands: []string{"docker compose ps", "docker compose logs *", "docker ps"}},
}

// safeScript: package.json scripts that only check or build.
var safeScript = regexp.MustCompile(`^(test|lint|typecheck|type-check|tsc|check|build|vet|format:check|fmt:check|prettier:check)([:.-].*)?$`)

// unsafeBuiltin: built-in commands that change files, left out of the default.
var unsafeBuiltin = map[string]bool{"go mod tidy": true}

// Catalog lists every command of packs, and the safe ones: built-in packs
// (read, test, lint, build) and check scripts, not the project's own packs.
func Catalog(packs []Pack) (all, safe []string) {
	all, safe = []string{}, []string{}
	for _, p := range packs {
		for _, c := range p.Commands {
			if slices.Contains(all, c) {
				continue
			}
			all = append(all, c)
			switch {
			case p.Custom:
			case p.ID == "scripts":
				if f := strings.Fields(c); len(f) > 0 && safeScript.MatchString(f[len(f)-1]) {
					safe = append(safe, c)
				}
			case !unsafeBuiltin[c]:
				safe = append(safe, c)
			}
		}
	}
	return all, safe
}

// ProjectPacks: the built-in packs relevant to a project folder, with a
// pack for its package.json scripts, then the project's own packs.
func ProjectPacks(root string, custom []Pack) []Pack {
	has := func(name string) bool {
		if root == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	relevant := map[string]bool{
		"git-read":    has(".git"),
		"go":          has("go.mod"),
		"node":        has("package.json"),
		"python":      has("pyproject.toml") || has("requirements.txt") || has("setup.py"),
		"rust":        has("Cargo.toml"),
		"docker-read": has("docker-compose.yml") || has("compose.yaml") || has("docker-compose.yaml") || has("compose.yml"),
	}
	out := []Pack{}
	if p, ok := scriptsPack(root, has); ok {
		out = append(out, p)
	}
	for _, p := range BuiltinPacks {
		if relevant[p.ID] || root == "" {
			out = append(out, p)
		}
	}
	for _, p := range custom {
		p.Custom = true
		out = append(out, p)
	}
	return out
}

// scriptsPack turns package.json scripts into commands for its package manager.
func scriptsPack(root string, has func(string) bool) (Pack, bool) {
	if root == "" {
		return Pack{}, false
	}
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return Pack{}, false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(raw, &pkg) != nil || len(pkg.Scripts) == 0 {
		return Pack{}, false
	}
	pm := "npm run"
	switch {
	case has("pnpm-lock.yaml"):
		pm = "pnpm run"
	case has("yarn.lock"):
		pm = "yarn run"
	case has("bun.lockb") || has("bun.lock"):
		pm = "bun run"
	}
	names := make([]string, 0, len(pkg.Scripts))
	for n := range pkg.Scripts {
		if !strings.ContainsAny(n, shellChars+" \"'") {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	p := Pack{ID: "scripts", Label: "Script trong package.json", Icon: "i-lucide-file-json"}
	for _, n := range names {
		p.Commands = append(p.Commands, pm+" "+n)
	}
	return p, len(p.Commands) > 0
}
