package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

const (
	keepLines   = 3000
	maxLogBytes = 5 << 20
)

// stopGrace is how long Stop waits after SIGTERM before SIGKILL (a var so the
// test does not wait it out).
var stopGrace = 8 * time.Second

var (
	ErrRunning  = errors.New("tiến trình đang chạy")
	ErrNoFolder = errors.New("project không gắn thư mục nên không chạy lệnh được")
	ErrBadCwd   = errors.New("thư mục chạy phải nằm trong project")
)

// Line is one line of output.
type Line struct {
	Seq    int       `json:"seq"`
	Text   string    `json:"text"`
	Stream string    `json:"stream"` // out | err | sys
	Time   time.Time `json:"time"`
}

// State is a process's live state.
type State struct {
	Status     string     `json:"status"` // stopped | running | stopping | exited | crashed | restarting
	PID        int        `json:"pid,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Restarts   int        `json:"restarts"`
	Port       int        `json:"port,omitempty"`
	CPU        float64    `json:"cpu"`       // percent, whole process group
	MemBytes   int64      `json:"mem_bytes"` // resident, whole process group
}

type proc struct {
	mu        sync.Mutex
	def       storage.Process
	cmd       *exec.Cmd
	state     State
	lines     []Line
	seq       int
	wake      chan struct{}
	stopReq   bool
	done      chan struct{} // closed when the current run exits
	log       *os.File
	dir       string    // where the current run runs
	tail      *follower // reads the current run's output
	launching bool      // Start to launch: closes the window where 2 Start calls both pass the status check
	gen       uint64    // bumped by Stop on every call: lets launch() detect a Stop that landed between cmd.Start() and the state lock
}

// afterStart is a test-only hook invoked right after cmd.Start() succeeds and
// before launch() takes the lock to commit state. It lets tests land a Stop()
// call exactly inside that window.
var afterStart func()

// beforeStart is a test-only hook invoked right before cmd.Start(), after the
// caller (Start or the restart AfterFunc) has released its lock. It lets
// tests land a Stop() call in the earlier window between that unlock and
// cmd.Start() itself.
var beforeStart func()

// Manager supervises project processes.
type Manager struct {
	store  storage.Store
	logDir string
	env    []string
	runDir func(ctx context.Context, projectID string) string // a project's current Burn run ("" when none): processes at RunCwd run there

	mu    sync.Mutex
	procs map[string]*proc

	quit       chan struct{} // closed by Detach: office goes down, its processes run on
	detached   atomic.Bool
	detachOnce sync.Once
	readers    sync.WaitGroup // the followers reading the runs' output

	onChange atomic.Pointer[func()] // a process started, stopped or crashed
}

// OnChange tells fn when a process's status changes (kept in memory, not in
// a table): the dashboard follows it as a "processes" change, not by polling.
func (m *Manager) OnChange(fn func()) { m.onChange.Store(&fn) }

func (m *Manager) changed() {
	if fn := m.onChange.Load(); fn != nil {
		(*fn)()
	}
}

// NewManager builds a Manager; env is the environment commands run with.
func NewManager(store storage.Store, logDir string, env []string) *Manager {
	if env == nil {
		env = os.Environ()
	}
	if logDir == "" {
		logDir = filepath.Join(os.TempDir(), "office-processes")
	}
	return &Manager{store: store, logDir: logDir, env: env, procs: map[string]*proc{}, quit: make(chan struct{})}
}

func (m *Manager) get(id string) *proc {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[id]
	if !ok {
		p = &proc{state: State{Status: "stopped"}, wake: make(chan struct{})}
		m.procs[id] = p
	}
	return p
}

// State returns a process's state.
func (m *Manager) State(id string) State {
	p := m.get(id)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07]*\x07|\r`)

// ports printed by dev servers: "http://localhost:3000", "listening on :8080", "port 5173"
var portRe = regexp.MustCompile(`(?i)(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::\]|\[::1\]):(\d{2,5})\b|(?:listening|running|started)[^\n]*?\bport\D{0,3}(\d{2,5})\b`)

func (p *proc) add(stream, text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	l := Line{Seq: p.seq, Text: text, Stream: stream, Time: time.Now().UTC()}
	p.lines = append(p.lines, l)
	if len(p.lines) > keepLines {
		p.lines = append([]Line(nil), p.lines[len(p.lines)-keepLines:]...)
	}
	if p.state.Port == 0 && stream != "sys" {
		if mm := portRe.FindStringSubmatch(text); mm != nil {
			n, _ := strconv.Atoi(mm[1] + mm[2])
			if n > 0 && n < 65536 {
				p.state.Port = n
			}
		}
	}
	if p.log != nil {
		fmt.Fprintf(p.log, "%s %s %s\n", l.Time.Format(time.RFC3339), stream, text)
	}
	close(p.wake)
	p.wake = make(chan struct{})
}

// Since returns lines after seq, a channel closed on the next line, and the state.
func (m *Manager) Since(id string, seq int) ([]Line, <-chan struct{}, State) {
	p := m.get(id)
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []Line{}
	for _, l := range p.lines {
		if l.Seq > seq {
			out = append(out, l)
		}
	}
	return out, p.wake, p.state
}

// Tail returns the last n lines as text (for "ask an agent").
func (m *Manager) Tail(id string, n int) string {
	p := m.get(id)
	p.mu.Lock()
	defer p.mu.Unlock()
	start := max(0, len(p.lines)-n)
	var b strings.Builder
	for _, l := range p.lines[start:] {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// resolve checks a definition can run and returns its working directory.
func (m *Manager) resolve(ctx context.Context, def storage.Process) (string, error) {
	project, err := m.store.Repos().Get(ctx, def.ProjectID)
	if err != nil {
		return "", err
	}
	if project.Path == "" {
		return "", ErrNoFolder
	}
	if sub, ok := inRun(def.Cwd); ok {
		return m.runResolve(ctx, def.ProjectID, sub)
	}
	dir := filepath.Clean(filepath.Join(project.Path, def.Cwd))
	if rel, err := filepath.Rel(project.Path, dir); err != nil || strings.HasPrefix(rel, "..") {
		return "", ErrBadCwd
	}
	return dir, nil
}

// Start runs a process.
func (m *Manager) Start(ctx context.Context, id string) error {
	def, err := m.store.Processes().Get(ctx, id)
	if err != nil {
		return err
	}
	dir, err := m.resolve(ctx, def)
	if err != nil {
		return err
	}
	p := m.get(id)
	p.mu.Lock()
	if p.state.Status == "running" || p.state.Status == "stopping" || p.state.Status == "restarting" || p.launching {
		p.mu.Unlock()
		return ErrRunning
	}
	p.launching = true
	p.def = def
	startGen := p.gen
	p.mu.Unlock()
	err = m.launch(p, dir, startGen)
	p.mu.Lock()
	p.launching = false
	p.mu.Unlock()
	return err
}

// runWrapper runs the command and leaves its exit code in a file: the run
// outlives office, and the next office (not its parent) learns how it ended.
const runWrapper = `/bin/sh -c "$1"; c=$?; echo $c > "$2"; exit $c`

func (m *Manager) file(id, ext string) string { return filepath.Join(m.logDir, id+"."+ext) }

func (m *Manager) launch(p *proc, dir string, startGen uint64) error {
	id := p.def.ID
	_ = os.MkdirAll(m.logDir, 0o700)
	_ = os.Remove(m.file(id, "exit"))
	// its output goes to files, not pipes to office: it writes on while office restarts
	open := func(ext string) (*os.File, error) {
		return os.OpenFile(m.file(id, ext), os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0o600)
	}
	stdout, err := open("out")
	if err != nil {
		return err
	}
	defer stdout.Close()
	stderr, err := open("err")
	if err != nil {
		return err
	}
	defer stderr.Close()
	cmd := exec.Command("/bin/sh", "-c", runWrapper, "office-run", p.def.Command, m.file(id, "exit"))
	cmd.Dir = dir
	cmd.Env = append(append([]string(nil), m.env...), "FORCE_COLOR=0", "CI=")
	for _, k := range slices.Sorted(maps.Keys(p.def.Env)) { // the last of a key wins: its own over the office's
		cmd.Env = append(cmd.Env, k+"="+p.def.Env[k])
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// a session of its own: neither office's stop signal nor its terminal
	// closing reach it; Stop still ends its children (the group)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	logFile := m.openLog(id)
	if beforeStart != nil {
		beforeStart()
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("không chạy được lệnh: %w", err)
	}
	if afterStart != nil {
		afterStart()
	}
	now := time.Now().UTC()
	f := m.newFollower(p, 0, 0)
	p.mu.Lock()
	restarts := p.state.Restarts
	stoppedMeanwhile := p.gen != startGen
	p.cmd, p.log, p.done, p.dir, p.tail = cmd, logFile, make(chan struct{}), dir, f
	if stoppedMeanwhile {
		p.stopReq = true
		p.state = State{Status: "stopping", PID: cmd.Process.Pid, StartedAt: &now, Restarts: restarts}
	} else {
		p.stopReq = false
		p.state = State{Status: "running", PID: cmd.Process.Pid, StartedAt: &now, Restarts: restarts}
	}
	pid, done := cmd.Process.Pid, p.done
	p.mu.Unlock()
	m.saveRun(p)
	m.changed()
	p.add("sys", fmt.Sprintf("$ %s  (trong %s)", p.def.Command, dir))
	if stoppedMeanwhile {
		// a Stop() landed in the window between cmd.Start() and this lock: honor it now
		// instead of letting the unconditional state overwrite above resurrect the process.
		go func() {
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(stopGrace):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}()
	}
	ended := make(chan int, 1)
	go func() { ended <- exitCode(cmd.Wait()) }()
	m.follow(p, f, func() (int, bool) {
		select {
		case code := <-ended:
			return code, true
		default:
			return 0, false
		}
	})
	return nil
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	} else if err != nil {
		return -1
	}
	return 0
}

// follower reads a run's output from its files as it is written.
type follower struct {
	p     *proc
	files [2]*tailFile
}

type tailFile struct {
	path, stream string
	off          int64
	part         []byte // a line not ended yet
}

func (m *Manager) newFollower(p *proc, outOff, errOff int64) *follower {
	errStream := "err"
	if strings.HasPrefix(p.def.ID, "compose:") {
		errStream = "out" // docker compose writes its normal progress to stderr
	}
	return &follower{p: p, files: [2]*tailFile{
		{path: m.file(p.def.ID, "out"), stream: "out", off: outOff},
		{path: m.file(p.def.ID, "err"), stream: errStream, off: errOff},
	}}
}

// read adds the lines written since the last read.
func (f *follower) read() {
	for _, t := range f.files {
		fh, err := os.Open(t.path)
		if err != nil {
			continue
		}
		if st, err := fh.Stat(); err == nil && st.Size() < t.off {
			t.off, t.part = 0, nil // cut by its size limit
		}
		buf := make([]byte, 64<<10)
		for {
			n, err := fh.ReadAt(buf, t.off)
			t.off += int64(n)
			t.part = append(t.part, buf[:n]...)
			for {
				i := bytes.IndexByte(t.part, '\n')
				if i < 0 {
					break
				}
				f.p.add(t.stream, ansiRe.ReplaceAllString(string(t.part[:i]), ""))
				t.part = t.part[i+1:]
			}
			if len(t.part) > 1<<20 { // a line too long: shown in pieces
				f.p.add(t.stream, ansiRe.ReplaceAllString(string(t.part), ""))
				t.part = nil
			}
			if n == 0 || err != nil {
				break
			}
		}
		fh.Close()
		if t.off > maxLogBytes { // a long-running server: its file starts again (what was read is in the log)
			if os.Truncate(t.path, 0) == nil {
				t.off = 0
			}
		}
	}
}

// flush adds what is left of unfinished lines (the run ended).
func (f *follower) flush() {
	for _, t := range f.files {
		if len(t.part) > 0 {
			f.p.add(t.stream, ansiRe.ReplaceAllString(string(t.part), ""))
			t.part = nil
		}
	}
}

// follow reads a run's output until ended says it is over (and how), then
// ends it; office going down (Detach) stops reading, the run goes on.
func (m *Manager) follow(p *proc, f *follower, ended func() (int, bool)) {
	m.readers.Add(1)
	go func() {
		defer m.readers.Done()
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			code, over := ended()
			f.read()
			if over {
				f.flush()
				m.exited(p, code)
				return
			}
			select {
			case <-m.quit:
				return
			case <-t.C:
			}
		}
	}()
}

func (m *Manager) exited(p *proc, code int) {
	if m.detached.Load() { // office is going down: the next one learns how it ended
		return
	}
	now := time.Now().UTC()
	p.mu.Lock()
	dir := p.dir
	p.tail = nil
	ran := now.Sub(*p.state.StartedAt)
	p.state.PID, p.state.CPU, p.state.MemBytes = 0, 0, 0
	p.state.FinishedAt, p.state.ExitCode = &now, &code
	switch {
	case p.stopReq:
		p.state.Status = "stopped"
	case code == 0:
		p.state.Status = "exited"
	default:
		p.state.Status = "crashed"
	}
	restart := p.state.Status == "crashed" && p.def.Autorestart && p.def.Kind == "service"
	if ran > time.Minute {
		p.state.Restarts = 0 // it was healthy for a while: reset the backoff
	}
	delay := time.Duration(1<<min(p.state.Restarts, 5)) * time.Second
	if restart {
		p.state.Status = "restarting"
		p.state.Restarts++
	}
	done := p.done
	p.mu.Unlock()
	m.changed()
	msg := fmt.Sprintf("kết thúc với mã %d", code)
	if restart {
		msg += fmt.Sprintf(", chạy lại sau %s", delay)
	}
	p.add("sys", msg)
	p.mu.Lock()
	if p.log != nil {
		p.log.Close()
		p.log = nil
	}
	p.mu.Unlock()
	_ = os.Remove(m.file(p.def.ID, "run"))
	_ = os.Remove(m.file(p.def.ID, "exit"))
	close(done)
	if restart {
		time.AfterFunc(delay, func() {
			p.mu.Lock()
			still := p.state.Status == "restarting"
			var startGen uint64
			if still {
				p.launching = true
				startGen = p.gen
			}
			p.mu.Unlock()
			if still {
				err := m.launch(p, dir, startGen)
				p.mu.Lock()
				p.launching = false
				p.mu.Unlock()
				if err != nil {
					p.add("sys", err.Error())
					p.mu.Lock()
					p.state.Status = "crashed"
					p.mu.Unlock()
					m.changed()
				}
			}
		})
	}
}

// Stop stops a process: SIGTERM to its group, SIGKILL after a grace period.
func (m *Manager) Stop(id string) error {
	p := m.get(id)
	p.mu.Lock()
	p.gen++ // tells a launch() in flight (Start or restart) that it must not resurrect the process
	if p.state.Status == "restarting" {
		p.state.Status = "stopped"
		p.mu.Unlock()
		m.changed()
		return nil
	}
	if p.state.Status != "running" || p.state.PID <= 0 {
		p.mu.Unlock()
		return nil
	}
	p.stopReq = true
	p.state.Status = "stopping"
	pid, done := p.state.PID, p.done
	p.mu.Unlock()
	m.changed()
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	// the whole group, not only its shell: a child ignoring SIGTERM is killed
	deadline := time.Now().Add(stopGrace)
	for groupRuns(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if groupRuns(pid) {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	<-done
	return nil
}

func groupRuns(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Restart stops then starts.
func (m *Manager) Restart(ctx context.Context, id string) error {
	if err := m.Stop(id); err != nil {
		return err
	}
	return m.Start(ctx, id)
}

// Forget stops a process and drops its state (definition deleted).
func (m *Manager) Forget(id string) {
	_ = m.Stop(id)
	m.mu.Lock()
	delete(m.procs, id)
	m.mu.Unlock()
}

// runFile is a run kept on disk: the next office takes it over (Adopt).
type runFile struct {
	PID       int             `json:"pid"`
	Dir       string          `json:"dir"`
	Def       storage.Process `json:"def"`
	StartedAt time.Time       `json:"started_at"`
	Restarts  int             `json:"restarts"`
	Out       int64           `json:"out"` // how much of its output office has read
	Err       int64           `json:"err"`
}

func (m *Manager) saveRun(p *proc) {
	p.mu.Lock()
	r := runFile{PID: p.state.PID, Dir: p.dir, Def: p.def, Restarts: p.state.Restarts}
	if p.state.StartedAt != nil {
		r.StartedAt = *p.state.StartedAt
	}
	if p.tail != nil {
		r.Out, r.Err = p.tail.files[0].off, p.tail.files[1].off
	}
	p.mu.Unlock()
	if raw, err := json.Marshal(r); err == nil && r.PID > 0 {
		_ = os.WriteFile(m.file(r.Def.ID, "run"), raw, 0o600)
	}
}

// Detach lets the processes run on as office goes down: what was read of
// their output is noted for the next office, which takes them over.
func (m *Manager) Detach() {
	m.detachOnce.Do(func() {
		m.detached.Store(true)
		close(m.quit)
		m.readers.Wait()
		m.mu.Lock()
		procs := slices.Collect(maps.Values(m.procs))
		m.mu.Unlock()
		for _, p := range procs {
			p.mu.Lock()
			live := p.state.PID > 0 && p.tail != nil
			if p.log != nil {
				p.log.Close()
				p.log = nil
			}
			p.mu.Unlock()
			if live {
				m.saveRun(p)
			}
		}
	})
}

// Adopt takes over the runs the last office left (Detach); those that ended
// meanwhile end now, as they ended.
func (m *Manager) Adopt() {
	files, _ := filepath.Glob(filepath.Join(m.logDir, "*.run"))
	for _, path := range files {
		raw, err := os.ReadFile(path)
		var r runFile
		if err != nil || json.Unmarshal(raw, &r) != nil || r.PID <= 0 || r.Def.ID == "" {
			_ = os.Remove(path)
			continue
		}
		exitPath := m.file(r.Def.ID, "exit")
		alive := ours(r.PID, exitPath)
		p := m.get(r.Def.ID)
		started := r.StartedAt
		p.mu.Lock()
		p.def, p.dir, p.done = r.Def, r.Dir, make(chan struct{})
		p.state = State{Status: "running", PID: r.PID, StartedAt: &started, Restarts: r.Restarts}
		f := m.newFollower(p, 0, 0)
		p.tail = f
		p.mu.Unlock()
		f.replay(r.Out, r.Err) // what it wrote before, shown again (the log has it already)
		p.mu.Lock()
		p.log = m.openLog(r.Def.ID)
		p.mu.Unlock()
		p.add("sys", "office khởi động lại, tiến trình vẫn chạy")
		m.follow(p, f, func() (int, bool) {
			if alive && running(r.PID) {
				return 0, false
			}
			alive = false // once gone, a new process with its pid is not it
			raw, err := os.ReadFile(exitPath)
			if err != nil {
				return -1, true // killed: no code left
			}
			code, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				code = -1
			}
			return code, true
		})
	}
	if len(files) > 0 {
		m.changed()
	}
}

// replay reads the output up to the offsets (before the log is open: it has them).
func (f *follower) replay(outOff, errOff int64) {
	for i, end := range []int64{outOff, errOff} {
		t := f.files[i]
		if raw, err := os.ReadFile(t.path); err == nil {
			end = min(end, int64(len(raw)))
			lines := strings.Split(string(raw[:end]), "\n")
			for _, line := range lines[:len(lines)-1] {
				f.p.add(t.stream, ansiRe.ReplaceAllString(line, ""))
			}
			t.off, t.part = end, []byte(lines[len(lines)-1]) // a line not ended yet goes on with what follows
		}
	}
}

func running(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// ours: pid still runs, and it is the run whose exit code goes to exitPath
// (not another process that got its pid since).
func ours(pid int, exitPath string) bool {
	if !running(pid) {
		return false
	}
	out, err := exec.Command("ps", "-ww", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && strings.Contains(string(out), exitPath)
}

// Autostart starts processes marked to start with office.
func (m *Manager) Autostart(ctx context.Context) {
	list, err := m.store.Processes().List(ctx, "")
	if err != nil {
		return
	}
	for _, d := range list {
		if d.Autostart {
			_ = m.Start(ctx, d.ID) // one taken over already runs
		}
	}
}

// Shutdown stops everything (tests; office itself detaches).
func (m *Manager) Shutdown() {
	if m.detached.Load() {
		return
	}
	m.mu.Lock()
	ids := make([]string, 0, len(m.procs))
	for id := range m.procs {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() { defer wg.Done(); _ = m.Stop(id) }()
	}
	wg.Wait()
}

func (m *Manager) openLog(id string) *os.File {
	if m.logDir == "" {
		return nil
	}
	_ = os.MkdirAll(m.logDir, 0o700)
	p := filepath.Join(m.logDir, id+".log")
	if st, err := os.Stat(p); err == nil && st.Size() > maxLogBytes {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// Sample updates CPU and memory of running processes (whole process group)
// from one `ps` call. Run it periodically.
func (m *Manager) Sample() {
	m.mu.Lock()
	running := map[int]*proc{}
	for _, p := range m.procs {
		p.mu.Lock()
		if p.state.Status == "running" && p.state.PID > 0 {
			running[p.state.PID] = p
		}
		p.mu.Unlock()
	}
	m.mu.Unlock()
	if len(running) == 0 {
		return
	}
	out, err := exec.Command("ps", "-A", "-o", "pgid=,pcpu=,rss=").Output()
	if err != nil {
		return
	}
	cpu, mem := map[int]float64{}, map[int]int64{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		g, _ := strconv.Atoi(f[0])
		if _, ok := running[g]; !ok {
			continue
		}
		c, _ := strconv.ParseFloat(f[1], 64)
		r, _ := strconv.ParseInt(f[2], 10, 64)
		cpu[g] += c
		mem[g] += r * 1024
	}
	for pid, p := range running {
		p.mu.Lock()
		if p.state.PID == pid {
			p.state.CPU, p.state.MemBytes = cpu[pid], mem[pid]
		}
		p.mu.Unlock()
	}
}

// RunSampler samples every interval until ctx ends.
func (m *Manager) RunSampler(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Sample()
		}
	}
}

// Running is a project process that runs now, for the dashboard's process list.
type Running struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	PID       int    `json:"pid"`
}

// Running lists the project processes that run now.
func (m *Manager) Running() []Running {
	m.mu.Lock()
	procs := make([]*proc, 0, len(m.procs))
	for _, p := range m.procs {
		procs = append(procs, p)
	}
	m.mu.Unlock()
	var out []Running
	for _, p := range procs {
		p.mu.Lock()
		if p.state.PID > 0 && (p.state.Status == "running" || p.state.Status == "stopping") {
			out = append(out, Running{ID: p.def.ID, ProjectID: p.def.ProjectID, Name: p.def.Name, PID: p.state.PID})
		}
		p.mu.Unlock()
	}
	return out
}
