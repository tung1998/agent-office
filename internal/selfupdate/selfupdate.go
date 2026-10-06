// Package selfupdate rebuilds office from its own source and swaps the new
// build in, for a supervisor (`office run`) to restart. The running build is
// never touched until the new one compiled (and, optionally, passed tests);
// the previous build is kept as *.prev so the supervisor can roll back if the
// new one does not come up.
package selfupdate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// RestartCode is the exit code with which the server asks the supervisor
	// to restart it (on the freshly swapped build).
	RestartCode = 75
	// EnvSupervised is set by the supervisor for its children.
	EnvSupervised = "OFFICE_SUPERVISED"
	module        = "module github.com/tung1998/agent-office"
	resultFile    = "update.json"
)

var ErrBusy = errors.New("đang cập nhật")

// Source is office's own source tree.
type Source struct {
	Root  string `json:"root"`   // folder with go.mod
	UIDir string `json:"ui_dir"` // dashboard folder ("" if absent)
}

// FindSource looks for the office source above the executable (bin/office →
// repo root). ok is false for an installed binary without its source.
func FindSource(exe string) (Source, bool) {
	dir := filepath.Dir(exe)
	for i := 0; i < 5; i++ {
		raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(raw), module) {
			s := Source{Root: dir}
			if st, err := os.Stat(filepath.Join(dir, "dashboard", "package.json")); err == nil && !st.IsDir() {
				s.UIDir = filepath.Join(dir, "dashboard")
			}
			return s, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return Source{}, false
}

// Binary is where the built server lives (what the supervisor runs).
func (s Source) Binary() string { return filepath.Join(s.Root, "bin", "office") }

// UIOutput is the built dashboard.
func (s Source) UIOutput() string { return filepath.Join(s.UIDir, ".output") }

// Result is the outcome of the last update, written by whoever finished it.
type Result struct {
	State   string    `json:"state"` // ok | rolled_back | failed
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// SaveResult records the last update's outcome in the office data folder.
func SaveResult(home string, r Result) {
	raw, _ := json.MarshalIndent(r, "", "  ")
	_ = os.WriteFile(filepath.Join(home, resultFile), raw, 0o600)
}

// LoadResult reads it (nil if none).
func LoadResult(home string) *Result {
	raw, err := os.ReadFile(filepath.Join(home, resultFile))
	if err != nil {
		return nil
	}
	var r Result
	if json.Unmarshal(raw, &r) != nil {
		return nil
	}
	return &r
}

// Line is one line of update output (same shape as process logs).
type Line struct {
	Seq    int       `json:"seq"`
	Text   string    `json:"text"`
	Stream string    `json:"stream"` // out | err | sys
	Time   time.Time `json:"time"`
}

// State is the current job.
type State struct {
	Status     string     `json:"status"` // idle | running | failed | restarting
	Step       string     `json:"step,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Options of an update.
type Options struct {
	Test bool // run go test before swapping
}

// Updater runs one update at a time.
type Updater struct {
	src     Source
	home    string
	env     []string
	restart func()

	mu    sync.Mutex
	state State
	lines []Line
	seq   int
	wake  chan struct{}
}

// New builds an Updater; restart is called after a successful swap.
func New(src Source, home string, env []string, restart func()) *Updater {
	if env == nil {
		env = os.Environ()
	}
	return &Updater{src: src, home: home, env: env, restart: restart, state: State{Status: "idle"}, wake: make(chan struct{})}
}

// Source returns the source tree.
func (u *Updater) Source() Source { return u.src }

func (u *Updater) add(stream, text string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.seq++
	u.lines = append(u.lines, Line{Seq: u.seq, Text: text, Stream: stream, Time: time.Now().UTC()})
	if len(u.lines) > 3000 {
		u.lines = u.lines[len(u.lines)-3000:]
	}
	close(u.wake)
	u.wake = make(chan struct{})
}

func (u *Updater) set(f func(*State)) {
	u.mu.Lock()
	f(&u.state)
	close(u.wake)
	u.wake = make(chan struct{})
	u.mu.Unlock()
}

// Since returns lines after seq, a channel closed on change, and the state.
func (u *Updater) Since(seq int) ([]Line, <-chan struct{}, State) {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := []Line{}
	for _, l := range u.lines {
		if l.Seq > seq {
			out = append(out, l)
		}
	}
	return out, u.wake, u.state
}

// Start runs the update in the background.
func (u *Updater) Start(o Options) error {
	u.mu.Lock()
	if u.state.Status == "running" || u.state.Status == "restarting" {
		u.mu.Unlock()
		return ErrBusy
	}
	now := time.Now().UTC()
	u.state = State{Status: "running", StartedAt: &now}
	u.lines, u.seq = nil, 0
	u.mu.Unlock()
	go u.run(o)
	return nil
}

type step struct {
	name string
	dir  string
	env  []string
	argv []string
	skip bool
}

func (u *Updater) run(o Options) {
	fail := func(step string, err error) {
		now := time.Now().UTC()
		u.add("sys", "✗ "+step+": "+err.Error()+" · bản đang chạy giữ nguyên")
		u.set(func(s *State) { s.Status, s.Error, s.FinishedAt = "failed", step+": "+err.Error(), &now })
		SaveResult(u.home, Result{State: "failed", Message: step + ": " + err.Error(), At: now})
	}
	root := u.src.Root
	newBin := filepath.Join(root, "bin", "office.new")
	build := []string{"go", "build", "-o", newBin}
	// stamp the source fingerprint so the update page can tell what changed
	if fp, err := SourceFingerprint(context.Background(), root); err == nil {
		build = append(build, "-ldflags", "-X "+fingerprintVar+"="+fp)
	}
	steps := []step{
		{name: "Build server", dir: root, argv: append(build, "./cmd/office")},
		{name: "Chạy test", dir: root, argv: []string{"go", "test", "./..."}, skip: !o.Test},
	}
	if u.src.UIDir != "" {
		if _, err := os.Stat(filepath.Join(u.src.UIDir, "node_modules")); err != nil {
			steps = append(steps, step{name: "Cài package dashboard", dir: u.src.UIDir, argv: []string{"pnpm", "install", "--frozen-lockfile"}})
		}
		if _, err := os.Stat(filepath.Join(u.src.UIDir, "scripts", "check-i18n.mjs")); err == nil {
			steps = append(steps, step{name: "Kiểm tra i18n", dir: u.src.UIDir, argv: []string{"node", "scripts/check-i18n.mjs"}, skip: !o.Test})
		}
		_ = os.RemoveAll(filepath.Join(u.src.UIDir, ".output.new"))
		steps = append(steps, step{name: "Build dashboard", dir: u.src.UIDir, env: []string{"OFFICE_UI_OUT_DIR=.output.new"}, argv: []string{"npx", "nuxi", "build"}})
	}
	for _, st := range steps {
		if st.skip {
			continue
		}
		u.set(func(s *State) { s.Step = st.name })
		u.add("sys", fmt.Sprintf("▶ %s: %s", st.name, strings.Join(st.argv, " ")))
		if err := u.exec(st.dir, st.env, st.argv); err != nil {
			fail(st.name, err)
			return
		}
	}
	u.set(func(s *State) { s.Step = "Đổi bản" })
	if err := u.swap(); err != nil {
		fail("Đổi bản", err)
		return
	}
	u.add("sys", "✓ Đã build xong và đổi bản; office sẽ khởi động lại…")
	u.set(func(s *State) { s.Status, s.Step = "restarting", "Khởi động lại" })
	if u.restart != nil {
		time.AfterFunc(500*time.Millisecond, u.restart) // let the UI read the last lines
	}
}

func (u *Updater) exec(dir string, env, argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(append([]string(nil), u.env...), env...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			u.add("out", sc.Text())
		}
		close(done)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return err
}

// swap puts the new builds in place, keeping the current ones as *.prev.
func (u *Updater) swap() error {
	bin := u.src.Binary()
	if err := replace(bin, bin+".new", bin+".prev"); err != nil {
		return err
	}
	if u.src.UIDir != "" {
		out := u.src.UIOutput()
		if err := replace(out, out+".new", out+".prev"); err != nil {
			// keep server and dashboard from the same build
			_ = replace(bin, bin+".prev", bin+".new")
			return err
		}
	}
	return nil
}

// replace moves cur → prev and next → cur.
func replace(cur, next, prev string) error {
	if _, err := os.Stat(next); err != nil {
		return fmt.Errorf("không thấy bản mới %s", next)
	}
	_ = os.RemoveAll(prev)
	if _, err := os.Stat(cur); err == nil {
		if err := os.Rename(cur, prev); err != nil {
			return err
		}
	}
	return os.Rename(next, cur)
}

// Rollback restores the *.prev builds (used by the supervisor).
func Rollback(src Source) error {
	bin := src.Binary()
	if _, err := os.Stat(bin + ".prev"); err != nil {
		return errors.New("không có bản trước để quay về")
	}
	if err := replace(bin, bin+".prev", bin+".failed"); err != nil {
		return err
	}
	if src.UIDir != "" {
		if _, err := os.Stat(src.UIOutput() + ".prev"); err == nil {
			_ = replace(src.UIOutput(), src.UIOutput()+".prev", src.UIOutput()+".failed")
		}
	}
	return nil
}
