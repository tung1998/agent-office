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
	restartFile   = "restart.json"
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

// Restart is why office last went down on purpose (ADR-255): an update, a
// stop. The next office reads it once, to tell the chats whose work it cut
// what happened; none means it went down on its own (a crash, a kill).
type Restart struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// SaveRestart records why office is about to go down.
func SaveRestart(home string, r Restart) {
	raw, _ := json.MarshalIndent(r, "", "  ")
	_ = os.WriteFile(filepath.Join(home, restartFile), raw, 0o600)
}

// Unexpected is what TakeRestart says when office did not record why it went down.
const Unexpected = "office dừng đột ngột (sập, bị tắt ngang hoặc máy khởi động lại)"

// TakeRestart reads why office last went down and forgets it (Unexpected
// when nothing was recorded: each start takes the record of the stop before).
func TakeRestart(home string) string {
	path := filepath.Join(home, restartFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return Unexpected
	}
	_ = os.Remove(path)
	var r Restart
	if json.Unmarshal(raw, &r) != nil || r.Reason == "" {
		return Unexpected
	}
	return r.Reason
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
	// idle | running | waiting (built, the new build waits for office to be
	// idle, ADR-255) | failed | cancelled | restarting
	Status     string     `json:"status"`
	Step       string     `json:"step,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// WhenIdle: the new build is swapped in only once nothing runs (ADR-255)
	WhenIdle bool `json:"when_idle,omitempty"`
	// By: who asked (an admin's email, an agent's run, "auto")
	By string `json:"by,omitempty"`
	// waiting: since when, until when at most, and what still runs
	WaitingSince *time.Time `json:"waiting_since,omitempty"`
	WaitUntil    *time.Time `json:"wait_until,omitempty"`
	Busy         string     `json:"busy,omitempty"`
}

// Options of an update.
type Options struct {
	Test bool // run go test before swapping
	Pull bool // first bring in the source's branch on the remote (fast-forward only)
	// WhenIdle: build now, swap in and restart only once no work runs (the
	// Busy func), at most MaxWait; past it the update is dropped and the
	// running build stays (ADR-255).
	WhenIdle bool
	MaxWait  time.Duration // 0 = DefaultMaxWait
	By       string        // who asked
}

// DefaultMaxWait is how long an update waits for office to be idle.
const DefaultMaxWait = 2 * time.Hour

// MaxMaxWait bounds what a caller may ask for.
const MaxMaxWait = 24 * time.Hour

// Busy says what work a restart would cut off now ("" = none).
type Busy func(ctx context.Context) string

// Updater runs one update at a time.
type Updater struct {
	src     Source
	home    string
	env     []string
	restart func()
	busy    Busy
	poll    time.Duration    // how often a waiting update looks again
	now     func() time.Time // the clock (tests)

	mu     sync.Mutex
	state  State
	lines  []Line
	seq    int
	wake   chan struct{}
	cancel chan struct{} // closed: drop the update in progress
	hurry  chan struct{} // closed: stop waiting, swap in now
}

// New builds an Updater; restart is called after a successful swap.
func New(src Source, home string, env []string, restart func()) *Updater {
	if env == nil {
		env = os.Environ()
	}
	return &Updater{src: src, home: home, env: env, restart: restart, poll: 15 * time.Second, now: time.Now,
		state: State{Status: "idle"}, wake: make(chan struct{})}
}

// SetBusy tells the updater what work a restart would cut off; without it
// an update never waits.
func (u *Updater) SetBusy(b Busy) { u.busy = b }

// State is the current job's state.
func (u *Updater) State() State {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state
}

// Waiting: a built update waits for office to be idle. New work that would
// only make it wait longer (automations, Burn pieces) holds meanwhile.
func (u *Updater) Waiting() bool { return u.State().Status == "waiting" }

// ErrNothing: no update to cancel or hurry.
var ErrNothing = errors.New("không có bản cập nhật nào đang chờ")

// Cancel drops the update in progress (built, or still building): the
// running build stays.
func (u *Updater) Cancel() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if (u.state.Status != "running" && u.state.Status != "waiting") || u.cancel == nil {
		return ErrNothing
	}
	select {
	case <-u.cancel:
	default:
		close(u.cancel)
	}
	return nil
}

// Hurry swaps a waiting update in now, cutting off what runs.
func (u *Updater) Hurry() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.state.Status != "waiting" || u.hurry == nil {
		return ErrNothing
	}
	select {
	case <-u.hurry:
	default:
		close(u.hurry)
	}
	return nil
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
	if u.state.Status == "running" || u.state.Status == "waiting" || u.state.Status == "restarting" {
		u.mu.Unlock()
		return ErrBusy
	}
	now := u.now().UTC()
	u.state = State{Status: "running", StartedAt: &now, WhenIdle: o.WhenIdle, By: o.By}
	u.lines, u.seq = nil, 0
	u.cancel, u.hurry = make(chan struct{}), make(chan struct{})
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
	// the remote's new commits first: the build below stamps what it pulled
	if o.Pull {
		up := u.src.Upstream(context.Background())
		if up == "" {
			fail("Kéo mã mới", errors.New("không thấy nhánh main hay master trên origin"))
			return
		}
		for _, st := range []step{
			{name: "Kéo mã mới", dir: root, env: gitEnv, argv: []string{"git", "fetch", "origin"}},
			{name: "Kéo mã mới", dir: root, env: gitEnv, argv: []string{"git", "merge", "--ff-only", up}},
		} {
			if !u.step(st) {
				fail(st.name, errors.New("không kéo được từ "+up+" (xem log)"))
				return
			}
		}
	}
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
	u.settle(o)
}

// settle swaps the new build in and restarts, once office is idle when the
// update waits for it; a cancelled or timed-out wait drops the new build.
func (u *Updater) settle(o Options) {
	cancel, hurry := u.chans()
	if o.WhenIdle && u.busy != nil && !u.waitIdle(o, cancel, hurry) {
		return
	}
	select {
	case <-cancel:
		u.drop("Đã hủy bản cập nhật · bản đang chạy giữ nguyên")
		return
	default:
	}
	u.set(func(s *State) { s.Step = "Đổi bản" })
	if err := u.swap(); err != nil {
		now := u.now().UTC()
		u.add("sys", "✗ Đổi bản: "+err.Error()+" · bản đang chạy giữ nguyên")
		u.set(func(s *State) { s.Status, s.Error, s.FinishedAt = "failed", "Đổi bản: "+err.Error(), &now })
		SaveResult(u.home, Result{State: "failed", Message: "Đổi bản: " + err.Error(), At: now})
		return
	}
	// why office restarts, for what it tells the chats whose work it cut (ADR-255)
	why := "cập nhật lên bản mới"
	if o.WhenIdle {
		why += " khi office rảnh"
	}
	if o.By != "" {
		why += ", do " + o.By + " yêu cầu"
	}
	if u.busy != nil {
		if what := u.busy(context.Background()); what != "" {
			why += "; ngắt việc đang chạy: " + what
		}
	}
	SaveRestart(u.home, Restart{Reason: why, At: u.now().UTC()})
	u.add("sys", "✓ Đã build xong và đổi bản; office sẽ khởi động lại…")
	u.set(func(s *State) { s.Status, s.Step = "restarting", "Khởi động lại" })
	if u.restart != nil {
		time.AfterFunc(500*time.Millisecond, u.restart) // let the UI read the last lines
	}
}

func (u *Updater) chans() (cancel, hurry chan struct{}) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cancel == nil {
		u.cancel = make(chan struct{})
	}
	if u.hurry == nil {
		u.hurry = make(chan struct{})
	}
	return u.cancel, u.hurry
}

// waitIdle waits until no work runs; false when the update was cancelled or
// waited past its MaxWait (the new build is dropped, the running one stays).
func (u *Updater) waitIdle(o Options, cancel, hurry <-chan struct{}) bool {
	maxWait := o.MaxWait
	if maxWait <= 0 {
		maxWait = DefaultMaxWait
	}
	maxWait = min(maxWait, MaxMaxWait)
	since := u.now().UTC()
	until := since.Add(maxWait)
	last := ""
	for {
		what := u.busy(context.Background())
		if what == "" {
			if last != "" {
				u.add("sys", "✓ Hết việc đang chạy: đổi bản")
			}
			return true
		}
		if what != last {
			if last == "" {
				u.add("sys", fmt.Sprintf("⏳ Đã build xong; chờ hết việc đang chạy (tối đa %s) rồi mới đổi bản: %s", maxWait.Round(time.Minute), what))
			} else {
				u.add("sys", "⏳ Còn chạy: "+what)
			}
			last = what
			u.set(func(s *State) {
				s.Status, s.Step, s.Busy = "waiting", "Chờ hết việc", what
				s.WaitingSince, s.WaitUntil = &since, &until
			})
		}
		if !u.now().Before(until) {
			msg := fmt.Sprintf("Chờ %s mà vẫn còn việc (%s): bỏ bản mới · bản đang chạy giữ nguyên", maxWait.Round(time.Minute), what)
			u.discard()
			now := u.now().UTC()
			u.add("sys", "✗ "+msg)
			u.set(func(s *State) { s.Status, s.Error, s.FinishedAt = "failed", msg, &now })
			SaveResult(u.home, Result{State: "failed", Message: msg, At: now})
			return false
		}
		select {
		case <-cancel:
			u.drop("Đã hủy bản cập nhật đang chờ · bản đang chạy giữ nguyên")
			return false
		case <-hurry:
			u.add("sys", "⚡ Đổi bản ngay, không chờ nữa")
			return true
		case <-time.After(u.poll):
		}
	}
}

// drop ends a cancelled update: its new build goes, the running one stays.
func (u *Updater) drop(msg string) {
	u.discard()
	now := u.now().UTC()
	u.add("sys", "✗ "+msg)
	u.set(func(s *State) { s.Status, s.Error, s.FinishedAt = "cancelled", msg, &now })
}

// discard removes the builds an update made and did not swap in.
func (u *Updater) discard() {
	_ = os.Remove(filepath.Join(u.src.Root, "bin", "office.new"))
	if u.src.UIDir != "" {
		_ = os.RemoveAll(u.src.UIOutput() + ".new")
	}
}

// step runs one step with its output in the log; false when it failed (logged).
func (u *Updater) step(st step) bool {
	u.set(func(s *State) { s.Step = st.name })
	u.add("sys", fmt.Sprintf("▶ %s: %s", st.name, strings.Join(st.argv, " ")))
	if err := u.exec(st.dir, st.env, st.argv); err != nil {
		u.add("sys", "✗ "+err.Error())
		return false
	}
	return true
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
		if err := sc.Err(); err != nil {
			u.add("sys", "⚠ đọc log lỗi: "+err.Error())
		}
		// drain to EOF even after a scan error so the child can't block
		// writing to a full pipe and leave cmd.Wait() hanging forever.
		_, _ = io.Copy(io.Discard, pr)
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
