package llm

import (
	"errors"
	"fmt"
	"io/fs"
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

// cliInstall is the command that reinstalls a CLI, by its command name.
var cliInstall = map[string]string{
	"codex":  "npm install -g @openai/codex",
	"claude": "npm install -g @anthropic-ai/claude-code",
	"gemini": "npm install -g @google/gemini-cli",
	"dsh":    "npm install -g @deepseek-ai/dsh",
}

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
	if c := cliInstall[name]; c != "" {
		fix = "cài lại: " + c
	}
	if detail == "" {
		return fmt.Errorf("%s: %w (%s, rồi bấm Kiểm tra ở Kết nối AI)", name, ErrCLIBroken, fix)
	}
	return fmt.Errorf("%s: %w: %s (%s, rồi bấm Kiểm tra ở Kết nối AI)", name, ErrCLIBroken, detail, fix)
}

// StartError reads why a CLI could not even start: not on PATH or not a
// file any more is ErrCLIBroken; anything else is returned as it is.
func StartError(bin string, err error) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) || CLIBroken(err.Error()) {
		return CLIBrokenError(bin, err.Error())
	}
	return fmt.Errorf("không chạy được %s: %w", bin, err)
}
