package llm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrCLIBroken means a CLI connection cannot start at all: its command is gone
// from PATH, or its install is broken (npm's wrapper finds no native binary,
// as after an update cut short). No run on it can answer until it is
// reinstalled, so the office treats the connection as broken, not the task.
var ErrCLIBroken = errors.New("CLI không chạy được")

// cliBrokenText is how a CLI says it never started: Node's spawn of the
// native binary failing (codex, gemini), a module or optional dependency
// missing, a shebang whose interpreter is gone, a shell not finding it.
var cliBrokenText = regexp.MustCompile(`(?i)spawn \S+ ENOENT|cannot find module|missing optional dependency|env: \S+: no such file or directory|command not found`)

// CLIBroken: msg (a CLI's stderr or error) says the CLI never started.
func CLIBroken(msg string) bool { return cliBrokenText.MatchString(msg) }

// cliInstall is the command that reinstalls a CLI, by its command name: the
// fallback when installFix is not wired (tests) or has no answer for it.
// npm is a guess, not always the one actually in use (codex and claude are
// usually installed via Homebrew or a native installer instead): telling
// someone to npm install there installs a second, unused copy and leaves
// the real broken one in place, so SetInstallFix should be wired whenever
// the office knows how each CLI is really installed (clitools' methods).
var cliInstall = map[string]string{
	"codex":  "npm install -g @openai/codex",
	"claude": "npm install -g @anthropic-ai/claude-code",
	"gemini": "npm install -g @google/gemini-cli",
	"dsh":    "npm install -g @deepseek-ai/dsh",
}

// installFix, once wired by SetInstallFix, says the command that actually
// reinstalls the CLI at bin, read from bin's own path — an npm package, a
// Homebrew Cellar/Caskroom, or neither ("" = it does not know, fall back to
// cliInstall's guess).
var installFix func(bin string) string

// SetInstallFix wires CLIBrokenError's fix command to fn, which reads how
// the CLI at the path it is given is actually installed
// (clitools.Manager.InstallFixFor): replaces the npm-only guess in
// cliInstall, which is wrong whenever the real install is Homebrew or a
// native installer, or when the broken binary is an npm install missing a
// platform-specific optional dependency (so reinstalling it is still npm,
// not whatever a static "recommended" method would say).
func SetInstallFix(fn func(bin string) string) { installFix = fn }

// duplicates, once wired by SetDuplicates, lists every other executable on
// PATH under the same command name as bin ("" slice = it does not know, or
// there is only the one). More than one install of the same CLI is the
// common way reinstalling one leaves the copy office actually runs
// untouched and still broken.
var duplicates func(bin string) []string

// SetDuplicates wires CLIBrokenError to name every other install of the
// same CLI found on PATH (clitools.Manager.Duplicates), so its error does
// not just say "reinstall" into a PATH that may still pick up the broken
// copy afterwards — it names the other ones to remove too.
func SetDuplicates(fn func(bin string) []string) { duplicates = fn }

// CLIBrokenError says bin cannot start and how to fix it; detail is what the
// CLI or the system said (its first line only: Node adds a stack trace).
func CLIBrokenError(bin, detail string) error {
	name := filepath.Base(bin)
	detail = strings.TrimSpace(detail)
	if i := strings.IndexByte(detail, '\n'); i >= 0 {
		detail = strings.TrimSpace(detail[:i])
	}
	if len(detail) > 200 {
		detail = detail[:200] + "…"
	}
	fix := "cài lại CLI"
	c := cliInstall[name]
	if installFix != nil {
		if real := installFix(bin); real != "" {
			c = real
		}
	}
	if c != "" {
		fix = "cài lại: " + c
	}
	if duplicates != nil {
		if dups := duplicates(bin); len(dups) > 0 {
			fix += "; máy còn bản khác của " + name + " trên PATH, có thể bị dùng nhầm: " + strings.Join(dups, ", ")
		}
	}
	if detail == "" {
		return fmt.Errorf("%s: %w (%s, rồi bấm Kiểm tra ở Kết nối AI)", name, ErrCLIBroken, fix)
	}
	return fmt.Errorf("%s: %w: %s (%s, rồi bấm Kiểm tra ở Kết nối AI)", name, ErrCLIBroken, detail, fix)
}

// StartError reads why a CLI could not even start: not on PATH or not a
// file any more is ErrCLIBroken; anything else is returned as it is. A run's
// folder that is gone (a worktree removed under it: chdir fails with the
// same ENOENT) is the run's problem, not the connection's.
func StartError(bin string, err error) error { return StartErrorIn(bin, "", err) }

// StartErrorIn is StartError for a command started in dir. With its own
// SysProcAttr (a process group) Go does not say chdir: the start fails as
// "fork/exec <bin>: no such file or directory" whether the binary or the
// folder is gone, so the folder is looked at.
func StartErrorIn(bin, dir string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Op == "chdir" {
		return fmt.Errorf("không chạy được %s: thư mục làm việc %s không còn: %w", bin, pe.Path, err)
	}
	if dir != "" && errors.Is(err, fs.ErrNotExist) {
		if _, serr := os.Stat(dir); serr != nil {
			return fmt.Errorf("không chạy được %s: thư mục làm việc %s không còn: %w", bin, dir, err)
		}
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) || CLIBroken(err.Error()) {
		return CLIBrokenError(bin, err.Error())
	}
	return fmt.Errorf("không chạy được %s: %w", bin, err)
}
