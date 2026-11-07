package mcpgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

const (
	defaultStdioIdle  = 10 * time.Minute
	stdioStartTimeout = 60 * time.Second // npx/uvx may download the server first
	stdioHealthy      = 30 * time.Second // a process that lived this long resets the backoff
	maxBackoff        = 60 * time.Second
)

// stdioMaxLine caps one line of a stdio server's stdout; a var so tests can
// shrink it instead of writing tens of MB.
var stdioMaxLine = 64 << 20

// SetStdioMaxLineForTest shrinks stdioMaxLine for a test and returns a func
// that restores it.
func SetStdioMaxLineForTest(n int) func() {
	prev := stdioMaxLine
	stdioMaxLine = n
	return func() { stdioMaxLine = prev }
}

// pool holds the stdio servers office runs, one process per server shared
// by every run (ADR-092).
type pool struct {
	mu     sync.Mutex
	procs  map[string]*proc       // by server id
	starts map[string]*sync.Mutex // one start at a time per server
	fails  map[string]*failure
	closed bool
}

type failure struct {
	n     int
	until time.Time
	err   error
}

// fail records a start or an early death: the next start waits 1s, 2s, 4s…
func (p *pool) fail(id string, err error) {
	f := p.fails[id]
	if f == nil {
		f = &failure{}
		p.fails[id] = f
	}
	f.n++
	wait := min(time.Second<<(f.n-1), maxBackoff)
	f.until, f.err = time.Now().Add(wait), err
}

func (g *Gateway) idle() time.Duration {
	if g.StdioIdle > 0 {
		return g.StdioIdle
	}
	return defaultStdioIdle
}

// proc is one running stdio server.
type proc struct {
	name, key string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	wmu       sync.Mutex
	stderr    ring
	secrets   []string // its env values, kept out of what office shows
	done      chan struct{}
	started   time.Time
	stopOnce  sync.Once

	mu       sync.Mutex
	pending  map[int64]chan json.RawMessage
	nextID   int64
	inflight int
	lastUsed time.Time
	exitErr  error
	readErr  error
	stopping bool
	init     json.RawMessage     // the server's answer to office's initialize
	holders  map[string]struct{} // runs that called it (Caller.Run, ADR-254)

	// elicit answers the server's elicitation/create requests (nil: office
	// has none wired and such requests still get -32601, ADR-264).
	elicit ElicitFunc
	// callers is who is waiting on an answer from this process right now, by
	// roundTrip id, each with the context its own wait uses: an
	// elicitation/create the server sends while they wait is asked of
	// whichever of them has a chat to ask in (ConversationID set), on that
	// call's own context (so it ends with it, or with the process dying,
	// ADR-264). With several such calls in flight at once on a shared
	// server, it picks any one of them — a known gap, see ADR-264.
	callers map[int64]callerWait
}

// callerWait is one roundTrip's caller and the context it waits under.
type callerWait struct {
	c   Caller
	ctx context.Context
}

// stdioKey changes when what the process runs with changes.
func stdioKey(m storage.MCPServer) string {
	return m.Command + "\x00" + strings.Join(m.Args, "\x00") + "\x00" + m.EnvEnc
}

// stdio returns m's running process, starting it when needed.
func (g *Gateway) stdio(ctx context.Context, m storage.MCPServer) (*proc, error) {
	key := stdioKey(m)
	pl := &g.pool
	pl.mu.Lock()
	if pl.closed {
		pl.mu.Unlock()
		return nil, errClosed
	}
	if pl.procs == nil {
		pl.procs, pl.starts, pl.fails = map[string]*proc{}, map[string]*sync.Mutex{}, map[string]*failure{}
	}
	lk := pl.starts[m.ID]
	if lk == nil {
		lk = &sync.Mutex{}
		pl.starts[m.ID] = lk
	}
	pl.mu.Unlock()

	lk.Lock()
	defer lk.Unlock()
	pl.mu.Lock()
	cur := pl.procs[m.ID]
	if cur != nil && cur.key == key && cur.alive() {
		pl.mu.Unlock()
		cur.touch()
		return cur, nil
	}
	if cur != nil { // changed on the form, or dead
		delete(pl.procs, m.ID)
		go cur.stop()
	}
	if f := pl.fails[m.ID]; f != nil && time.Now().Before(f.until) {
		err := &backoffError{err: f.err, wait: time.Until(f.until)}
		pl.mu.Unlock()
		return nil, err
	}
	pl.mu.Unlock()

	np, err := g.startProc(ctx, m, key)
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if err != nil {
		pl.fail(m.ID, err)
		return nil, err
	}
	if pl.closed {
		go np.stop()
		return nil, errClosed
	}
	pl.procs[m.ID] = np
	go g.watch(m.ID, np)
	return np, nil
}

// watch stops np when nobody called it for StdioIdle, and forgets it when
// it exits; an early death counts towards the backoff.
func (g *Gateway) watch(id string, np *proc) {
	idle := g.idle()
	tick := time.NewTicker(min(max(idle/5, 20*time.Millisecond), 30*time.Second))
	defer tick.Stop()
	pl := &g.pool
	for {
		select {
		case <-np.done:
			pl.mu.Lock()
			if pl.procs[id] == np {
				delete(pl.procs, id)
			}
			np.mu.Lock()
			stopping := np.stopping
			np.mu.Unlock()
			if time.Since(np.started) >= stdioHealthy {
				delete(pl.fails, id)
			} else if !stopping {
				pl.fail(id, np.deadErr())
			}
			pl.mu.Unlock()
			if !stopping {
				g.log().Warn("mcp gateway: stdio exited", "server", np.name, "err", np.deadErr().Error())
			}
			return
		case <-tick.C:
			// a run that called it and still lives may come back to its
			// browser tab or session: not stopped under it (ADR-254)
			if np.idleFor() > idle && !np.held(g.Alive) {
				pl.mu.Lock()
				if pl.procs[id] == np {
					delete(pl.procs, id)
				}
				pl.mu.Unlock()
				g.log().Info("mcp gateway: stdio idle, stopped", "server", np.name)
				np.stop()
			}
		}
	}
}

// Close stops every stdio server (office is shutting down).
func (g *Gateway) Close() {
	pl := &g.pool
	pl.mu.Lock()
	pl.closed = true
	all := make([]*proc, 0, len(pl.procs))
	for _, p := range pl.procs {
		all = append(all, p)
	}
	pl.procs = map[string]*proc{}
	pl.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range all {
		wg.Add(1)
		go func() { defer wg.Done(); p.stop() }()
	}
	wg.Wait()
}

// Forget stops the process of server id, if it runs (deleted or turned off).
func (g *Gateway) Forget(id string) {
	pl := &g.pool
	pl.mu.Lock()
	p := pl.procs[id]
	delete(pl.procs, id)
	delete(pl.fails, id)
	delete(pl.starts, id)
	pl.mu.Unlock()
	if p != nil {
		go p.stop()
	}
}

// Running lists the stdio servers office runs now, with their pids.
func (g *Gateway) Running() map[string]int {
	pl := &g.pool
	pl.mu.Lock()
	defer pl.mu.Unlock()
	out := map[string]int{}
	for _, p := range pl.procs {
		if p.alive() {
			out[p.name] = p.cmd.Process.Pid
		}
	}
	return out
}

func (g *Gateway) startProc(ctx context.Context, m storage.MCPServer, key string) (*proc, error) {
	if strings.TrimSpace(m.Command) == "" {
		return nil, errors.New("chưa có lệnh chạy MCP")
	}
	vars, err := OpenMap(g.Box, m.EnvEnc)
	if err != nil {
		return nil, errors.New("không mở được biến môi trường đã lưu: nhập lại")
	}
	env := append([]string(nil), g.Env...)
	if g.Env == nil {
		env = os.Environ()
	}
	np := &proc{name: m.Name, key: key, done: make(chan struct{}), pending: map[int64]chan json.RawMessage{}, elicit: g.Elicit}
	for k, v := range vars {
		env = append(env, k+"="+v)
		if len(v) >= 4 {
			np.secrets = append(np.secrets, v)
		}
	}
	path, err := lookCommand(m.Command, env)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(path, m.Args...)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own group: stopping kills its children too
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// os pipes, so Wait returns when the process exits even if a child
	// still holds them
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		return nil, fmt.Errorf("không chạy được lệnh %s: %v", m.Command, err)
	}
	outW.Close()
	errW.Close()
	np.cmd, np.stdin, np.started, np.lastUsed = cmd, stdin, time.Now(), time.Now()
	go func() { defer outR.Close(); np.read(outR) }()
	go func() { defer errR.Close(); _, _ = io.Copy(&np.stderr, errR) }()
	go func() {
		err := cmd.Wait()
		np.mu.Lock()
		np.exitErr = err
		np.mu.Unlock()
		close(np.done)
	}()

	ictx, cancel := context.WithTimeout(ctx, stdioStartTimeout)
	defer cancel()
	res, err := np.call(ictx, "initialize", initParams(), Caller{})
	if err != nil {
		np.stop()
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("MCP không trả lời initialize trong %d giây", int(stdioStartTimeout.Seconds()))
		}
		return nil, np.withStderr(fmt.Errorf("MCP %s không khởi động được: %w", m.Name, err))
	}
	np.mu.Lock()
	np.init = res
	np.mu.Unlock()
	_ = np.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	g.log().Info("mcp gateway: stdio started", "server", m.Name, "pid", cmd.Process.Pid)
	return np, nil
}

func (p *proc) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *proc) touch() {
	p.mu.Lock()
	p.lastUsed = time.Now()
	p.mu.Unlock()
}

// hold records that run called p: while it lives, p is not idle (ADR-254).
func (p *proc) hold(run string) {
	if run == "" {
		return
	}
	p.mu.Lock()
	if p.holders == nil {
		p.holders = map[string]struct{}{}
	}
	p.holders[run] = struct{}{}
	p.mu.Unlock()
}

// held reports whether a run that called p still lives, forgetting the
// ones that ended.
func (p *proc) held(alive func(string) bool) bool {
	if alive == nil {
		return false
	}
	p.mu.Lock()
	runs := make([]string, 0, len(p.holders))
	for r := range p.holders {
		runs = append(runs, r)
	}
	p.mu.Unlock()
	live := false
	for _, r := range runs {
		if alive(r) {
			live = true
			continue
		}
		p.mu.Lock()
		delete(p.holders, r)
		p.mu.Unlock()
	}
	return live
}

func (p *proc) idleFor() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inflight > 0 {
		return 0
	}
	return time.Since(p.lastUsed)
}

// stop ends the process and its children: stdin closed, SIGTERM to the
// group, SIGKILL after 2 seconds.
func (p *proc) stop() {
	p.stopOnce.Do(func() {
		p.mu.Lock()
		p.stopping = true
		p.mu.Unlock()
		_ = p.stdin.Close()
		pid := p.cmd.Process.Pid
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			<-p.done
		}
	})
}

func (p *proc) write(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, err = p.stdin.Write(append(raw, '\n'))
	return err
}

// roundTrip sends one request under an id of office's own (so clients
// sharing the process never mix their ids) and returns the answer's fields.
// c is who asks (Caller{} when nobody office could ask back): while it
// waits, an elicitation/create from the server is asked of it (ADR-264).
func (p *proc) roundTrip(ctx context.Context, fields map[string]json.RawMessage, c Caller) (map[string]json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1)
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	p.pending[id] = ch
	p.inflight++
	p.lastUsed = time.Now()
	if p.callers == nil {
		p.callers = map[int64]callerWait{}
	}
	p.callers[id] = callerWait{c: c, ctx: ctx}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		delete(p.callers, id)
		p.inflight--
		p.lastUsed = time.Now()
		p.mu.Unlock()
	}()
	fields["jsonrpc"], fields["id"] = json.RawMessage(`"2.0"`), json.RawMessage(strconv.FormatInt(id, 10))
	if err := p.write(fields); err != nil {
		return nil, p.deadErr()
	}
	parse := func(raw json.RawMessage) (map[string]json.RawMessage, error) {
		var out map[string]json.RawMessage
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, errors.New("MCP trả lời không đọc được")
		}
		return out, nil
	}
	select {
	case raw := <-ch:
		return parse(raw)
	case <-ctx.Done():
		_ = p.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id}})
		return nil, ctx.Err()
	case <-p.done:
		select {
		case raw := <-ch: // answered just before it exited
			return parse(raw)
		default:
			return nil, p.deadErr()
		}
	}
}

// call is office's own request to the process.
func (p *proc) call(ctx context.Context, method string, params any, c Caller) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	out, err := p.roundTrip(ctx, map[string]json.RawMessage{"method": json.RawMessage(strconv.Quote(method)), "params": raw}, c)
	if err != nil {
		return nil, err
	}
	if e := out["error"]; len(e) > 0 && string(e) != "null" {
		var re struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(e, &re)
		return nil, &callError{method: method, code: re.Code, message: p.redact(re.Message)}
	}
	return out["result"], nil
}

// read routes the process's messages: answers to their request; its own
// requests get elicitation/create answered (ADR-264, asynchronously so one
// slow answer never stalls reading this process's other replies) and
// anything else (sampling, still unwired) "not supported".
func (p *proc) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), stdioMaxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case '{':
			p.handle(append([]byte(nil), line...))
		case '[':
			var batch []json.RawMessage
			if json.Unmarshal(line, &batch) == nil {
				for _, m := range batch {
					p.handle(append([]byte(nil), m...))
				}
			}
		} // anything else is a server logging to stdout: ignored
	}
	if err := sc.Err(); err != nil {
		// A real scanner error (e.g. a line over stdioMaxLine) leaves the
		// process running but nobody reading its stdout any more; kill it
		// without marking it as a deliberate stop so watch() still counts
		// this as an abnormal death (backoff + log), not a silent hang.
		p.mu.Lock()
		p.readErr = err
		p.mu.Unlock()
		if proc := p.cmd.Process; proc != nil {
			_ = syscall.Kill(-proc.Pid, syscall.SIGTERM)
		}
	}
}

func (p *proc) handle(raw json.RawMessage) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return
	}
	if msg.Method != "" {
		if len(msg.ID) == 0 || string(msg.ID) == "null" {
			return // a notification: nobody to pass it to
		}
		if msg.Method == "ping" {
			_ = p.write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{}})
			return
		}
		if msg.Method == "elicitation/create" {
			// own goroutine: this can wait minutes on a person, and must
			// never block reading this process's other replies/requests.
			go p.handleElicit(raw, msg.ID)
			return
		}
		_ = p.write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{
			"code": -32601, "message": "office chưa hỗ trợ yêu cầu " + msg.Method + " từ MCP"}})
		return
	}
	id, err := strconv.ParseInt(string(msg.ID), 10, 64)
	if err != nil {
		return
	}
	p.mu.Lock()
	ch := p.pending[id]
	p.mu.Unlock()
	if ch != nil {
		select {
		case ch <- raw:
		default:
		}
	}
}

// currentCaller is whoever is waiting on this process right now with a chat
// to ask in (ConversationID set; a bare Caller{} of office's own initialize
// or a background CallTool never qualifies), and the context its wait uses
// — any one of them, when several such calls are in flight at once (a known
// gap, ADR-264).
func (p *proc) currentCaller() (Caller, context.Context, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.callers {
		if w.c.ConversationID != "" {
			return w.c, w.ctx, true
		}
	}
	return Caller{}, nil, false
}

// elicitTimeout bounds how long office waits on an answer to the server's
// elicitation/create before declining on the server's behalf.
const elicitTimeout = 10 * time.Minute

// handleElicit answers the server's elicitation/create request id: through
// p.elicit when office has it wired to ask someone, "declined" if that
// someone never answers in time (or the call waiting on this process ends,
// or the process itself dies), else the same "not supported" error every
// server request got before (ADR-264).
func (p *proc) handleElicit(raw json.RawMessage, id json.RawMessage) {
	var req struct {
		Params struct {
			Message         string          `json:"message"`
			RequestedSchema json.RawMessage `json:"requestedSchema"`
		} `json:"params"`
	}
	_ = json.Unmarshal(raw, &req)
	c, parent, hasCaller := p.currentCaller()
	if !hasCaller || p.elicit == nil {
		_ = p.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{
			"code": -32601, "message": "office chưa hỗ trợ yêu cầu elicitation/create từ MCP " + p.name}})
		return
	}
	ctx, cancel := context.WithTimeout(parent, elicitTimeout)
	defer cancel()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-p.done:
			cancel()
		case <-stop:
		}
	}()
	action, content, err := p.elicit(ctx, c, p.name, req.Params.Message, req.Params.RequestedSchema)
	if err != nil || action == "" {
		action, content = "decline", nil
	}
	result := map[string]any{"action": action}
	if content != nil {
		result["content"] = content
	}
	_ = p.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// deadErr says the process is gone, with the end of its stderr.
func (p *proc) deadErr() error {
	p.mu.Lock()
	readErr, why := p.readErr, p.exitErr
	p.mu.Unlock()
	msg := "tiến trình MCP " + p.name + " đã dừng"
	switch {
	case readErr != nil:
		msg += " (dòng trả lời quá dài hoặc lỗi đọc: " + readErr.Error() + ")"
	case why != nil:
		msg += " (" + why.Error() + ")"
	}
	return p.withStderr(errors.New(msg))
}

func (p *proc) withStderr(err error) error {
	if tail := p.redact(p.stderr.tail()); tail != "" {
		return fmt.Errorf("%w: %s", err, tail)
	}
	return err
}

// redact hides the env values the server got, should it print them.
func (p *proc) redact(s string) string {
	for _, v := range p.secrets {
		s = strings.ReplaceAll(s, v, "••••")
	}
	return s
}

// ring keeps the last bytes a process wrote to stderr.
type ring struct {
	mu  sync.Mutex
	buf []byte
}

const ringSize = 4 << 10

func (r *ring) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, b...)
	if over := len(r.buf) - ringSize; over > 0 {
		r.buf = append([]byte(nil), r.buf[over:]...)
	}
	return len(b), nil
}

// tail is its last few lines, on one line.
func (r *ring) tail() string {
	r.mu.Lock()
	lines := strings.Split(strings.TrimSpace(string(r.buf)), "\n")
	r.mu.Unlock()
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	out := []string{}
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	s := strings.Join(out, " ⏎ ")
	if r := []rune(s); len(r) > 600 {
		s = "…" + string(r[len(r)-600:])
	}
	return s
}

// installHint says how to get a missing command.
var installHint = map[string]string{
	"npx":     "cài Node.js (https://nodejs.org), có sẵn npx",
	"npm":     "cài Node.js (https://nodejs.org)",
	"node":    "cài Node.js (https://nodejs.org)",
	"uvx":     "cài uv: curl -LsSf https://astral.sh/uv/install.sh | sh",
	"uv":      "cài uv: curl -LsSf https://astral.sh/uv/install.sh | sh",
	"docker":  "cài Docker Desktop (https://docs.docker.com/get-docker/) và mở nó",
	"python":  "cài Python 3 (https://www.python.org/downloads/)",
	"python3": "cài Python 3 (https://www.python.org/downloads/)",
	"bunx":    "cài Bun (https://bun.sh)",
	"bun":     "cài Bun (https://bun.sh)",
	"deno":    "cài Deno (https://deno.com)",
}

// installCommand is a shell line that installs a missing command, for the
// dashboard to copy (ADR-250); only commands with one line are listed.
var installCommand = map[string]string{
	"uvx":  "curl -LsSf https://astral.sh/uv/install.sh | sh",
	"uv":   "curl -LsSf https://astral.sh/uv/install.sh | sh",
	"bunx": "curl -fsSL https://bun.sh/install | bash",
	"bun":  "curl -fsSL https://bun.sh/install | bash",
	"deno": "curl -fsSL https://deno.land/install.sh | sh",
}

// InstallCommand is the line that installs m's missing command, when m is a
// stdio server whose last check found its command missing ("" otherwise).
func InstallCommand(m storage.MCPServer) string {
	if m.Kind != "stdio" || m.LastCheckStatus != "error" || !strings.Contains(m.LastCheckError, "máy chưa có lệnh") {
		return ""
	}
	return installCommand[filepath.Base(m.Command)]
}

// lookCommand finds name on the PATH of env.
func lookCommand(name string, env []string) (string, error) {
	executable := func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
	}
	if strings.ContainsRune(name, '/') {
		if executable(name) {
			return name, nil
		}
		return "", missingCommand(name)
	}
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		if p := filepath.Join(dir, name); executable(p) {
			return p, nil
		}
	}
	return "", missingCommand(name)
}

func missingCommand(name string) error {
	hint := installHint[filepath.Base(name)]
	if hint == "" {
		hint = "kiểm tra lệnh đã cài và nằm trong PATH"
	}
	return fmt.Errorf("máy chưa có lệnh %s: %s", name, hint)
}

func (g *Gateway) checkStdio(ctx context.Context, m storage.MCPServer) ([]storage.MCPTool, error) {
	p, err := g.stdio(ctx, m)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	tools, err := listTools(ctx, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		return p.call(ctx, method, params, Caller{})
	})
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("MCP không trả lời tools/list trong %d giây", int(CheckTimeout.Seconds()))
	}
	return tools, err
}

// checkStdioResources lists a stdio server's resources, best effort: an
// optional MCP capability, so a process with none (or one that times out
// answering it) is not treated as broken.
func (g *Gateway) checkStdioResources(ctx context.Context, m storage.MCPServer) ([]storage.MCPResource, error) {
	p, err := g.stdio(ctx, m)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	return listResources(ctx, func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		return p.call(ctx, method, params, Caller{})
	})
}

// serveStdio answers a run's request from m's process: initialize from
// office's cache, requests under office's own ids, notifications dropped
// (office keeps the session with the process itself). Answers are plain
// JSON; there is no server stream (GET is 405). Office opens the process
// with the legacy initialize, so it answers as a legacy server: a modern
// (2026-07-28) request gets a 400 without a modern error, and the client
// falls back to initialize.
func (g *Gateway) serveStdio(w http.ResponseWriter, r *http.Request, m storage.MCPServer, body []byte, c Caller, size *callSize) (int, string) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return http.StatusMethodNotAllowed, ""
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent) // the process stays for other runs
		return http.StatusNoContent, ""
	}
	t := bytes.TrimSpace(body)
	batch := len(t) > 0 && t[0] == '['
	var msgs []json.RawMessage
	if batch {
		if json.Unmarshal(t, &msgs) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return http.StatusBadRequest, "bad request"
		}
	} else {
		msgs = []json.RawMessage{t}
	}
	if modernRequest(msgs) {
		http.Error(w, "MCP "+m.Name+" (stdio qua office) chỉ nói MCP "+protocolVersion+": mở bằng initialize", http.StatusBadRequest)
		return http.StatusBadRequest, ""
	}
	rpc, tool := describe(body)
	start := time.Now()
	p, err := g.stdio(r.Context(), m)
	if err != nil {
		var b *backoffError
		if errors.As(err, &b) { // the same failure, already warned of when it happened
			g.log().Debug("mcp gateway", "server", m.Name, "rpc", rpc, "err", err.Error())
		} else {
			g.log().Warn("mcp gateway", "server", m.Name, "rpc", rpc, "err", err.Error())
		}
		g.note(r.Context(), m, err)
		http.Error(w, "MCP "+m.Name+": "+err.Error(), http.StatusBadGateway)
		return http.StatusBadGateway, err.Error()
	}
	g.note(r.Context(), m, nil) // it runs (and answered office's initialize)
	p.hold(c.Run)
	ctx, cancel := context.WithTimeout(r.Context(), CallTimeout)
	defer cancel()
	var out []json.RawMessage
	errMsg := ""
	for _, raw := range msgs {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			out = append(out, rpcError(json.RawMessage("null"), -32700, "parse error"))
			continue
		}
		id, hasID := fields["id"]
		var method string
		_ = json.Unmarshal(fields["method"], &method)
		if method == "" || !hasID || string(id) == "null" {
			continue // answers and notifications
		}
		switch method {
		case "initialize":
			p.mu.Lock()
			res := p.init
			p.mu.Unlock()
			out = append(out, rpcResult(id, res))
		case "ping":
			out = append(out, rpcResult(id, json.RawMessage("{}")))
		default:
			delete(fields, "id")
			resp, err := p.roundTrip(ctx, fields, c)
			if err != nil {
				if errMsg == "" {
					errMsg = "MCP " + m.Name + ": " + err.Error()
				}
				out = append(out, rpcError(id, -32603, "MCP "+m.Name+": "+err.Error()))
				continue
			}
			if method == "tools/call" {
				if em := callErrMsg(resp); em != "" && errMsg == "" {
					errMsg = em
				}
				if res, ok := resp["result"]; ok && !batch {
					var n int
					resp["result"], n = g.cutResult(c, m.Name, tool, res)
					size.cut += int64(n)
				}
			}
			resp["id"] = id // the client's own id back
			b, _ := json.Marshal(resp)
			out = append(out, b)
		}
	}
	g.log().Info("mcp gateway", "server", m.Name, "kind", "stdio", "rpc", rpc, "tool", tool, "ms", time.Since(start).Milliseconds())
	if len(out) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return http.StatusAccepted, ""
	}
	w.Header().Set("Content-Type", "application/json")
	if batch {
		b, _ := json.Marshal(out)
		_, _ = w.Write(b)
		size.out = int64(len(b))
		return http.StatusOK, errMsg
	}
	_, _ = w.Write(out[0])
	size.out = int64(len(out[0]))
	return http.StatusOK, errMsg
}

// modernRequest says whether a message carries the MCP 2026-07-28 version in
// its _meta (a client that skipped initialize).
func modernRequest(msgs []json.RawMessage) bool {
	for _, raw := range msgs {
		var m struct {
			Params struct {
				Meta map[string]json.RawMessage `json:"_meta"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &m) == nil && len(m.Params.Meta["io.modelcontextprotocol/protocolVersion"]) > 0 {
			return true
		}
	}
	return false
}

// callErrMsg reports a tools/call result's own error: a JSON-RPC error, or a
// result whose content says isError (ADR-093's resultText, mirrored for the
// stdio path so its audit log sees the same failures the HTTP path logs).
func callErrMsg(resp map[string]json.RawMessage) string {
	if raw, ok := resp["error"]; ok {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		if e.Message != "" {
			return e.Message
		}
		return "error"
	}
	raw, ok := resp["result"]
	if !ok {
		return ""
	}
	text, isErr := resultText(raw)
	if !isErr {
		return ""
	}
	if text == "" {
		return "error"
	}
	return text
}

func rpcResult(id, result json.RawMessage) json.RawMessage {
	b, _ := json.Marshal(map[string]json.RawMessage{"jsonrpc": json.RawMessage(`"2.0"`), "id": id, "result": result})
	return b
}

func rpcError(id json.RawMessage, code int, msg string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	return b
}
