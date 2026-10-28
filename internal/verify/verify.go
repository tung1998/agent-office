// Package verify runs a project's checks in a working copy, with no model:
// what Burn runs on a piece reported done (ADR-131) and what office runs on
// a chat's diff before it merges it on its own (ADR-222). An agent saying it
// is done is not taken at its word.
package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Timeout caps one check.
const Timeout = 10 * time.Minute

// maxOut caps what a failed check hands back.
const maxOut = 3000

// maxRules caps the merge checks the person writes.
const maxRules = 2000

// Passed and Held open a diff's detail when office checked it before
// merging it (ADR-222): the harness review counts them.
const (
	Passed = "Kiểm tra trước khi gộp đã qua"
	Held   = "Kiểm tra trước khi gộp chưa qua, chưa gộp"
)

// Guess is the build checks a project's files suggest, fast ones only
// (tests can be long: the person adds them).
func Guess(dir string) []string {
	has := func(name string) bool { _, err := os.Stat(filepath.Join(dir, name)); return err == nil }
	var out []string
	if has("go.mod") {
		out = append(out, "go build ./...", "go vet ./...")
	}
	if has("Cargo.toml") {
		out = append(out, "cargo check")
	}
	return out
}

// guessFor is Guess limited to what files touch: a diff of docs runs no
// build.
func guessFor(dir string, files []string) []string {
	var out []string
	for _, c := range Guess(dir) {
		lang := map[bool][]string{true: {".go", "go.mod", "go.sum"}, false: {".rs", "Cargo.toml", "Cargo.lock"}}[strings.HasPrefix(c, "go ")]
		if touches(files, lang) {
			out = append(out, c)
		}
	}
	return out
}

func touches(files, ends []string) bool {
	for _, f := range files {
		for _, e := range ends {
			if strings.HasSuffix(f, e) {
				return true
			}
		}
	}
	return false
}

// Run runs cmds in dir one by one: "" when they pass (or there are none, or
// ctx is stopped meanwhile: not the work's fault), else which failed and
// its output's end.
func Run(ctx context.Context, cmds []string, dir string) string {
	if dir == "" {
		return ""
	}
	for _, c := range cmds {
		cctx, cancel := context.WithTimeout(ctx, Timeout)
		cmd := exec.CommandContext(cctx, "sh", "-c", c)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		timedOut := errors.Is(cctx.Err(), context.DeadlineExceeded)
		cancel()
		if ctx.Err() != nil {
			return ""
		}
		if err == nil {
			continue
		}
		why := err.Error()
		if timedOut {
			why = fmt.Sprintf("quá %s", Timeout)
		}
		text := strings.TrimSpace(string(out))
		if r := []rune(text); len(r) > maxOut {
			text = "…" + string(r[len(r)-maxOut:])
		}
		return fmt.Sprintf("`%s` lỗi (%s):\n```\n%s\n```", c, why, text)
	}
	return ""
}

// Rule is one of a project's merge checks (ADR-222): Command runs when a
// changed file ends in one of Ends or lies under one of Dirs; neither = on
// every diff.
type Rule struct {
	Ends, Dirs []string
	Command    string
}

// ParseRules reads a project's merge checks: one a line, "go test ./..."
// on every diff or "dashboard/ .vue .ts: pnpm --dir dashboard typecheck"
// for the files it names (an ending starts with a dot, a folder ends in /);
// empty lines and # comments skipped.
func ParseRules(s string) ([]Rule, error) {
	if len([]rune(s)) > maxRules {
		return nil, fmt.Errorf("lệnh kiểm tra trước khi gộp dài quá %d ký tự", maxRules)
	}
	var out []Rule
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		r := Rule{Command: l}
		if scope, cmd, ok := strings.Cut(l, ": "); ok && strings.TrimSpace(cmd) != "" {
			var ends, dirs []string
			scoped := true
			for _, f := range strings.Fields(strings.ReplaceAll(scope, ",", " ")) {
				if strings.HasPrefix(f, ".") && !strings.Contains(f, "/") {
					ends = append(ends, strings.ToLower(f))
				} else if strings.HasSuffix(f, "/") {
					dirs = append(dirs, strings.TrimPrefix(f, "./"))
				} else { // not a scope: a command with ": " in it
					scoped = false
					break
				}
			}
			if scoped && len(ends)+len(dirs) > 0 {
				r = Rule{Ends: ends, Dirs: dirs, Command: strings.TrimSpace(cmd)}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (r Rule) applies(files []string) bool {
	if len(r.Ends)+len(r.Dirs) == 0 {
		return true
	}
	for _, f := range files {
		for _, e := range r.Ends {
			if strings.HasSuffix(strings.ToLower(f), e) {
				return true
			}
		}
		for _, d := range r.Dirs {
			if strings.HasPrefix(f, d) {
				return true
			}
		}
	}
	return false
}

// ForDiff is what to run on a diff of files in dir before merging it: the
// project's merge checks that apply, else the build checks its files
// suggest for what changed.
func ForDiff(rules string, dir string, files []string) []string {
	list, _ := ParseRules(rules) // checked when saved
	if len(list) == 0 {
		return guessFor(dir, files)
	}
	var out []string
	for _, r := range list {
		if r.applies(files) {
			out = append(out, r.Command)
		}
	}
	return out
}
