package chat

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Workspace.Search with ripgrep when the machine has one: many threads, a
// few ms where walking the folder in Go takes hundreds (10ms vs 230ms on
// this repo). The rg on PATH, else the one inside Claude Code (its binary
// runs as rg when called by that name, as its own shell does). Same rules as
// the walk: skipped folders and hidden ones, no secrets, nothing over 2MB, no
// binaries, .gitignore not followed (an ignored file is still found).
var (
	rgOnce sync.Once
	rgPath string // "": none, the walk in Go
)

func ripgrepPath() string {
	rgOnce.Do(func() {
		cands := []string{}
		if p, err := exec.LookPath("rg"); err == nil {
			cands = append(cands, p)
		}
		if p, err := exec.LookPath("claude"); err == nil {
			cands = append(cands, p)
		}
		if home, err := os.UserHomeDir(); err == nil {
			cands = append(cands, filepath.Join(home, ".local/bin/claude"))
		}
		for _, p := range cands {
			out, err := rgCommand(p, "--version").Output()
			if err == nil && bytes.HasPrefix(out, []byte("ripgrep ")) {
				rgPath = p
				return
			}
		}
	})
	return rgPath
}

// rgCommand runs bin as rg (argv[0] "rg": what Claude Code's binary looks at).
func rgCommand(bin string, args ...string) *exec.Cmd {
	cmd := exec.Command(bin, args...)
	cmd.Args[0] = "rg"
	return cmd
}

// the walk's skipDirs and secretName, as rg globs (secrets: --iglob, any case)
var rgExclude = func() []string {
	g := []string{"-g", "!.*/"} // hidden folders (hidden files are searched, as in the walk)
	dirs := make([]string, 0, len(skipDirs))
	for d := range skipDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		g = append(g, "-g", "!"+d+"/")
	}
	for _, f := range []string{".env", ".env.*", "*.pem", "*.key", "*.p12", "*.pfx", "id_rsa*", "id_ed25519*", "id_ecdsa*", "secret.key", "*credentials.json", "*.keystore"} {
		g = append(g, "--iglob", "!"+f)
	}
	return g
}()

// searchRipgrep: ok=false when there is no rg or it failed (a pattern Rust's
// regex reads otherwise…): the walk answers then.
func searchRipgrep(root, pattern, glob string) (string, bool) {
	bin := ripgrepPath()
	if bin == "" {
		return "", false
	}
	args := []string{"--no-config", "--no-ignore", "--hidden", "--no-messages", "--color", "never", "-n", "-i", "-0", "--max-filesize", "2M"}
	if glob != "" {
		args = append(args, "-g", glob)
	}
	args = append(args, rgExclude...)
	args = append(args, "-e", pattern, ".")
	cmd := rgCommand(bin, args...)
	cmd.Dir = root
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", false
	}
	if err := cmd.Start(); err != nil {
		return "", false
	}
	timer := time.AfterFunc(30*time.Second, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()
	type hit struct {
		file string
		ln   int
		text string
	}
	var hits []hit
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 4<<20)
	for sc.Scan() && len(hits) < 200 {
		file, rest, ok := strings.Cut(sc.Text(), "\x00")
		n, text, ok2 := strings.Cut(rest, ":")
		ln, err := strconv.Atoi(n)
		if !ok || !ok2 || err != nil {
			continue
		}
		hits = append(hits, hit{strings.TrimPrefix(file, "./"), ln, text})
	}
	full := len(hits) >= 200
	if full {
		_ = cmd.Process.Kill() // the rest is not read
	}
	werr := cmd.Wait()
	// rg: 0 found, 1 nothing found, 2 an error; killed at 200 is fine
	if !full && werr != nil {
		if ee, ok := werr.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			return "", false
		}
	}
	if len(hits) == 0 {
		return "No matches.", true
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].file != hits[j].file {
			return hits[i].file < hits[j].file
		}
		return hits[i].ln < hits[j].ln
	})
	var b strings.Builder
	for _, h := range hits {
		line := h.text
		if r := []rune(line); len(r) > 240 {
			line = string(r[:240]) + "…"
		}
		fmt.Fprintf(&b, "%s:%d: %s\n", h.file, h.ln, strings.TrimSpace(line))
	}
	if full {
		b.WriteString("… (stopped at 200 matches; narrow the search)\n")
	}
	return b.String(), true
}
