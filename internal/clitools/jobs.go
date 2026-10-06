package clitools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/tung1998/agent-office/internal/ids"
)

// JobView is a job as the dashboard sees it.
type JobView struct {
	ID        string     `json:"id"`
	Tool      string     `json:"tool"`
	Action    string     `json:"action"` // install | login
	Command   string     `json:"command"`
	State     string     `json:"state"` // running | succeeded | failed | cancelled
	Output    string     `json:"output"`
	URLs      []string   `json:"urls"`
	Codes     []string   `json:"codes"` // one-time device codes found in the output
	ExitCode  int        `json:"exit_code"`
	Error     string     `json:"error,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// Job is one install or login run, executed in a pseudo-terminal so CLIs that
// expect a TTY (login prompts) behave as they would in a terminal.
type Job struct {
	ID        string
	Tool      string
	Action    string
	Command   string
	State     string
	ExitCode  int
	Error     string
	StartedAt time.Time
	EndedAt   *time.Time

	mu     sync.Mutex
	buf    bytes.Buffer
	tty    *os.File
	cancel context.CancelFunc
	done   chan struct{}
}

const maxOutput = 64 << 10

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*(\x07|\x1b\\)|\x1b[()][A-Z0-9]|\r`)
	urlRe  = regexp.MustCompile(`https://[^\s"'<>\x1b]+`)
	// device codes look like ABCD-EFGH or ABCD-1234
	codeRe = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4,5}\b`)
)

// Snapshot returns a copy safe to serialise.
func (j *Job) Snapshot() JobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := cleanOutput(j.buf.String())
	s := JobView{ID: j.ID, Tool: j.Tool, Action: j.Action, Command: j.Command, State: j.State, Output: out,
		ExitCode: j.ExitCode, Error: j.Error, StartedAt: j.StartedAt, EndedAt: j.EndedAt, URLs: []string{}, Codes: []string{}}
	seen := map[string]bool{}
	for _, u := range urlRe.FindAllString(out, -1) {
		u = strings.TrimRight(u, ".,;)")
		if !seen[u] {
			seen[u] = true
			s.URLs = append(s.URLs, u)
		}
	}
	for _, c := range codeRe.FindAllString(out, -1) {
		if !seen[c] {
			seen[c] = true
			s.Codes = append(s.Codes, c)
		}
	}
	return s
}

func cleanOutput(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	// collapse runs of blank lines left by TUI redraws
	lines := strings.Split(s, "\n")
	out := lines[:0]
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Input writes text (plus Enter) to the job's terminal, e.g. a pasted login code.
func (j *Job) Input(text string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.State != "running" || j.tty == nil {
		return errors.New("tiến trình đã kết thúc")
	}
	_, err := io.WriteString(j.tty, text+"\r")
	return err
}

// Cancel stops the job.
func (j *Job) Cancel() {
	j.mu.Lock()
	if j.State == "running" {
		j.State = "cancelled"
	}
	j.mu.Unlock()
	j.cancel()
}

// Wait blocks until the job ends (tests).
func (j *Job) Wait() { <-j.done }

func (j *Job) write(p []byte) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.buf.Len()+len(p) > maxOutput {
		// keep the tail: the latest output matters most
		rest := j.buf.Bytes()[j.buf.Len()/2:]
		j.buf = *bytes.NewBuffer(append([]byte("…\n"), rest...))
	}
	j.buf.Write(p)
}

// start runs argv in a PTY with env; finish is called after exit (for status refresh).
func startJob(tool, action string, argv []string, env []string, timeout time.Duration) (*Job, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	j := &Job{ID: ids.New("job"), Tool: tool, Action: action, Command: displayCommand(argv), State: "running",
		StartedAt: time.Now().UTC(), cancel: cancel, done: make(chan struct{})}
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("không chạy được %s: %w", argv[0], err)
	}
	j.tty = tty
	go func() {
		defer close(j.done)
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				j.write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		werr := cmd.Wait()
		_ = tty.Close()
		now := time.Now().UTC()
		j.mu.Lock()
		defer j.mu.Unlock()
		j.tty, j.EndedAt = nil, &now
		if cmd.ProcessState != nil {
			j.ExitCode = cmd.ProcessState.ExitCode()
		}
		switch {
		case j.State == "cancelled":
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			j.State, j.Error = "failed", "quá thời gian chờ"
		case werr != nil:
			j.State, j.Error = "failed", werr.Error()
		default:
			j.State = "succeeded"
		}
	}()
	return j, nil
}

func displayCommand(argv []string) string {
	if len(argv) == 3 && argv[0] == "/bin/sh" && argv[1] == "-c" {
		return argv[2]
	}
	return strings.Join(argv, " ")
}
