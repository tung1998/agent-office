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
	stopping bool
	init     json.RawMessage // the server's answer to office's initialize
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
		return nil, errors.New("office đang tắt")
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
		err := fmt.Errorf("%v (thử chạy lại sau %d giây)", f.err, int(time.Until(f.until).Seconds())+1)
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
		return nil, errors.New("office đang tắt")
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
			if np.idleFor() > idle {
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
	np := &proc{name: m.Name, key: key, done: make(chan struct{}), pending: map[int64]chan json.RawMessage{}}
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
	res, err := np.call(ictx, "initialize", initParams())
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
func (p *proc) roundTrip(ctx context.Context, fields map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1)
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	p.pending[id] = ch
	p.inflight++
	p.lastUsed = time.Now()
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
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
func (p *proc) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	out, err := p.roundTrip(ctx, map[string]json.RawMessage{"method": json.RawMessage(strconv.Quote(method)), "params": raw})
	if err != nil {
		return nil, err
	}
	if e := out["error"]; len(e) > 0 && string(e) != "null" {
		var re struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(e, &re)
		return nil, fmt.Errorf("MCP báo lỗi khi %s: %s", method, firstLine(p.redact(re.Message), 200))
	}
	return out["result"], nil
}

// read routes the process's messages: answers to their request, its own
// requests get "not supported" (office has no sampling or elicitation).
func (p *proc) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
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
		_ = p.write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{
			"code": -32601, "message": "office chưa hỗ trợ yêu cầu " + msg.Method + " từ MCP (sampling, elicitation…)"}})
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

// deadErr says the process is gone, with the end of its stderr.
func (p *proc) deadErr() error {
	p.mu.Lock()
	why := p.exitErr
	p.mu.Unlock()
	msg := "tiến trình MCP " + p.name + " đã dừng"
	if why != nil {
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
	tools, err := listTools(ctx, p.call)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("MCP không trả lời tools/list trong %d giây", int(CheckTimeout.Seconds()))
	}
	return tools, err
}

// serveStdio answers a run's request from m's process: initialize from
// office's cache, requests under office's own ids, notifications dropped
// (office keeps the session with the process itself). Answers are plain
// JSON; there is no server stream (GET is 405).
func (g *Gateway) serveStdio(w http.ResponseWriter, r *http.Request, m storage.MCPServer, body []byte) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent) // the process stays for other runs
		return
	}
	t := bytes.TrimSpace(body)
	batch := len(t) > 0 && t[0] == '['
	var msgs []json.RawMessage
	if batch {
		if json.Unmarshal(t, &msgs) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	} else {
		msgs = []json.RawMessage{t}
	}
	rpc, tool := describe(body)
	start := time.Now()
	p, err := g.stdio(r.Context(), m)
	if err != nil {
		g.log().Warn("mcp gateway", "server", m.Name, "rpc", rpc, "err", err.Error())
		http.Error(w, "MCP "+m.Name+": "+err.Error(), http.StatusBadGateway)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), CallTimeout)
	defer cancel()
	var out []json.RawMessage
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
			resp, err := p.roundTrip(ctx, fields)
			if err != nil {
				out = append(out, rpcError(id, -32603, "MCP "+m.Name+": "+err.Error()))
				continue
			}
			resp["id"] = id // the client's own id back
			b, _ := json.Marshal(resp)
			out = append(out, b)
		}
	}
	g.log().Info("mcp gateway", "server", m.Name, "kind", "stdio", "rpc", rpc, "tool", tool, "ms", time.Since(start).Milliseconds())
	if len(out) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if batch {
		b, _ := json.Marshal(out)
		_, _ = w.Write(b)
		return
	}
	_, _ = w.Write(out[0])
}

func rpcResult(id, result json.RawMessage) json.RawMessage {
	b, _ := json.Marshal(map[string]json.RawMessage{"jsonrpc": json.RawMessage(`"2.0"`), "id": id, "result": result})
	return b
}

func rpcError(id json.RawMessage, code int, msg string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
	return b
}
