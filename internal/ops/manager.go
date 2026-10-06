package ops

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

const (
	keepLines   = 3000
	maxLogBytes = 5 << 20
	stopGrace   = 8 * time.Second
)

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
	launching bool // Start to launch: closes the window where 2 Start calls both pass the status check
}

// Manager supervises project processes.
type Manager struct {
	store  storage.Store
	logDir string
	env    []string

	mu    sync.Mutex
	procs map[string]*proc
}

// NewManager builds a Manager; env is the environment commands run with.
func NewManager(store storage.Store, logDir string, env []string) *Manager {
	if env == nil {
		env = os.Environ()
	}
	return &Manager{store: store, logDir: logDir, env: env, procs: map[string]*proc{}}
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
	p.mu.Unlock()
	err = m.launch(p, dir)
	p.mu.Lock()
	p.launching = false
	p.mu.Unlock()
	return err
}

func (m *Manager) launch(p *proc, dir string) error {
	cmd := exec.Command("/bin/sh", "-c", p.def.Command)
	cmd.Dir = dir
	cmd.Env = append(append([]string(nil), m.env...), "FORCE_COLOR=0", "CI=")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own group: stop kills children too
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	logFile := m.openLog(p.def.ID)
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("không chạy được lệnh: %w", err)
	}
	now := time.Now().UTC()
	p.mu.Lock()
	restarts := p.state.Restarts
	p.cmd, p.stopReq, p.log, p.done = cmd, false, logFile, make(chan struct{})
	p.state = State{Status: "running", PID: cmd.Process.Pid, StartedAt: &now, Restarts: restarts}
	p.mu.Unlock()
	p.add("sys", fmt.Sprintf("$ %s  (trong %s)", p.def.Command, dir))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); pump(p, "out", stdout) }()
	errStream := "err"
	if strings.HasPrefix(p.def.ID, "compose:") {
		errStream = "out" // docker compose writes its normal progress to stderr
	}
	go func() { defer wg.Done(); pump(p, errStream, stderr) }()
	go func() {
		wg.Wait()
		err := cmd.Wait()
		m.exited(p, dir, err)
	}()
	return nil
}

func pump(p *proc, stream string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		p.add(stream, ansiRe.ReplaceAllString(sc.Text(), ""))
	}
}

func (m *Manager) exited(p *proc, dir string, err error) {
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		code = -1
	}
	now := time.Now().UTC()
	p.mu.Lock()
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
	close(done)
	if restart {
		time.AfterFunc(delay, func() {
			p.mu.Lock()
			still := p.state.Status == "restarting"
			if still {
				p.launching = true
			}
			p.mu.Unlock()
			if still {
				err := m.launch(p, dir)
				p.mu.Lock()
				p.launching = false
				p.mu.Unlock()
				if err != nil {
					p.add("sys", err.Error())
					p.mu.Lock()
					p.state.Status = "crashed"
					p.mu.Unlock()
				}
			}
		})
	}
}

// Stop stops a process: SIGTERM to its group, SIGKILL after a grace period.
func (m *Manager) Stop(id string) error {
	p := m.get(id)
	p.mu.Lock()
	if p.state.Status == "restarting" {
		p.state.Status = "stopped"
		p.mu.Unlock()
		return nil
	}
	if p.state.Status != "running" || p.cmd == nil {
		p.mu.Unlock()
		return nil
	}
	p.stopReq = true
	p.state.Status = "stopping"
	pid, done := p.cmd.Process.Pid, p.done
	p.mu.Unlock()
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(stopGrace):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-done
	}
	return nil
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

// Autostart starts processes marked to start with office.
func (m *Manager) Autostart(ctx context.Context) {
	list, err := m.store.Processes().List(ctx, "")
	if err != nil {
		return
	}
	for _, d := range list {
		if d.Autostart {
			_ = m.Start(ctx, d.ID)
		}
	}
}

// Shutdown stops everything (office is exiting).
func (m *Manager) Shutdown() {
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
