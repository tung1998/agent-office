package chat

import (
	"bufio"
	"bytes"
	"cmp"
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

	"github.com/tung1998/agent-office/internal/cliuse"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/proctrack"
	"github.com/tung1998/agent-office/internal/storage"
)

// dshRunner drives DeepSeek Harness (`dsh --profile acp`) over the Agent
// Client Protocol on stdio. The connection's API (any Chat Completions or
// Anthropic Messages endpoint, DeepSeek's own by default) is declared in a
// patch layer written for the run, its key passed through the environment;
// the chat's session goes on with session/resume.
type dshRunner struct{}

const (
	dshKeyEnv = "OFFICE_DSH_API_KEY" // the connection's key, named in the patch, never written to it
	dshRoute  = "office"             // the provider route the patch declares
	// dshDefaultModel is DeepSeek's model when a DeepSeek connection names none.
	dshDefaultModel = "deepseek-v4-flash"
)

// dshNoTools are the patch ids of dsh's own tools, turned off for a run
// people outside office drive (NoTools).
var dshNoTools = []string{"tool-bash", "tool-pwsh", "tool-jobs", "tool-fs", "tool-fs-search", "tool-web", "tool-skill",
	"tool-subagent", "tool-subagent-fork", "tool-workflow", "tool-working-directory", "tool-todo", "tool-goal"}

func (r dshRunner) Run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	res, err := r.run(ctx, req, emit)
	// a session dsh no longer has: start over with the transcript; not when
	// the first attempt already acted (a tool call would run again for real)
	if err != nil && req.SessionID != "" && res.Text == "" && len(res.Tools) == 0 && ctx.Err() == nil && !errors.Is(err, ErrSilent) {
		req.SessionID = ""
		res, err = r.run(ctx, req, emit)
	}
	if err == nil && res.Text == "" {
		err = errors.New("dsh kết thúc lượt mà không trả lời")
	}
	return res, err
}

// dshPermission is dsh's sandbox preset for the run (DSH_PERMISSION_MODE).
func dshPermission(req RunRequest) string {
	switch {
	case req.NoTools:
		return "read-only"
	case req.FullAccess:
		return "danger-full-access"
	case req.Write:
		return "workspace-write"
	}
	return "read-only"
}

// dshModel is the model the run asks for.
func dshModel(req RunRequest) (string, error) {
	if req.Model != "" {
		return req.Model, nil
	}
	if _, base := llm.HarnessAPI(req.Provider.BaseURL); base == llm.DeepSeekAPI {
		return dshDefaultModel, nil
	}
	return "", errors.New("kết nối DeepSeek Harness chưa có model: điền model ở Kết nối AI hoặc ở agent")
}

// dshPatch is the run's patch layer (JSON is YAML): the connection's API as
// the only route, DeepSeek's session-log upload off, and dsh's own tools
// narrowed to what the run may use.
func dshPatch(req RunRequest, model string) ([]byte, error) {
	api, base := llm.HarnessAPI(req.Provider.BaseURL)
	route := map[string]any{"provider": dshRoute, "model": model}
	patch := []map[string]any{
		{"id": "llm-pi-ai", "config": map[string]any{"providers": map[string]any{dshRoute: map[string]any{
			"displayName": cmp.Or(req.Provider.Name, "agent-office"), "apiKeyEnv": dshKeyEnv,
			"api": api, "baseURL": base, "models": []map[string]any{{"id": model}},
		}}}},
		{"id": "acp", "config": route},
		{"id": "agent-default-model", "config": route},
		{"id": "session-log-deepseek", "disabled": true},
	}
	off := func(ids ...string) {
		for _, id := range ids {
			patch = append(patch, map[string]any{"id": id, "disabled": true})
		}
	}
	switch {
	case req.NoTools:
		off(dshNoTools...)
	default:
		if !req.Web {
			off("tool-web")
		}
		if req.NoSubagents {
			off("tool-subagent", "tool-subagent-fork", "tool-workflow")
		}
	}
	return json.MarshalIndent(patch, "", " ")
}

// acpHTTPServer is an MCP server entry of session/new and session/resume.
type acpHTTPServer struct {
	Type    string          `json:"type"`
	Name    string          `json:"name"`
	URL     string          `json:"url"`
	Headers []acpHTTPHeader `json:"headers"`
}

type acpHTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// dshMCPServers are office's MCP server and the gateway's, over HTTP with
// the run's token (ADR-093), like Codex.
func dshMCPServers(req RunRequest) []acpHTTPServer {
	o := req.Office
	if o == nil || req.NoTools || o.MCPURL == "" {
		return []acpHTTPServer{}
	}
	auth := acpHTTPHeader{Name: "Authorization", Value: "Bearer " + o.Token}
	out := []acpHTTPServer{{Type: "http", Name: mcpserver.ServerName, URL: o.MCPURL, Headers: []acpHTTPHeader{auth}}}
	if o.Other {
		out = append(out, acpHTTPServer{Type: "http", Name: officetools.OtherServer, URL: o.MCPURL + mcpserver.OtherPath, Headers: []acpHTTPHeader{auth}})
	}
	for _, n := range directGateway(o) {
		out = append(out, acpHTTPServer{Type: "http", Name: n, URL: o.MCPURL + "/s/" + n,
			Headers: []acpHTTPHeader{auth, {Name: ClientHeader, Value: "dsh"}}})
	}
	return out
}

// dshPrompt is the turn's text: instructions and the transcript for a new
// session, only the new message for a resumed one. dsh's ACP takes no image
// on a hand-declared route, so images are named by path (its read_image
// tool opens them).
func dshPrompt(req RunRequest) string {
	prompt, images := codexPrompt(req.Prompt, req.Attachments)
	for _, p := range images {
		prompt += "\n(Attached image: " + p + ")"
	}
	if req.SessionID == "" {
		return req.System + "\n\n" + transcript(req.History, prompt)
	}
	if brief := gatewayBrief(req.Office); !req.NoTools && brief != "" {
		return strings.TrimSpace(brief) + "\n\n" + prompt
	}
	return prompt
}

// dshEffort is dsh's reasoning level for office's (off, low, high, max).
func dshEffort(e string) string {
	switch e {
	case "low":
		return "low"
	case "medium", "high":
		return "high"
	case "xhigh", "max":
		return "max"
	}
	return ""
}

func (dshRunner) run(ctx context.Context, req RunRequest, emit func(Event)) (res RunResult, _ error) {
	if req.APIKey == "" {
		return res, llm.ErrNoAPIKey
	}
	model, err := dshModel(req)
	if err != nil {
		return res, err
	}
	patch, err := dshPatch(req, model)
	if err != nil {
		return res, err
	}
	dir, err := os.MkdirTemp("", "office-dsh-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(dir)
	patchFile := filepath.Join(dir, "office.patch.yml")
	if err := os.WriteFile(patchFile, patch, 0o600); err != nil {
		return res, err
	}

	bin := firstNonEmpty(req.Bin, "dsh")
	start := time.Now()
	runCtx, quiet, stop := watchSilence(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, bin, "--profile", "acp", "--patch", patchFile)
	ownGroup(cmd)
	cmd.Dir = req.WorkDir // dsh's workspace root
	cmd.Env = append(os.Environ(), dshKeyEnv+"="+req.APIKey, "DSH_TELEMETRY_MODE=DISABLED", "DSH_PERMISSION_MODE="+dshPermission(req))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return res, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return res, err
	}
	inUse, err := cliuse.Begin(ctx, bin) // an update of this CLI holds new runs back (ADR-259)
	if err != nil {
		return res, err
	}
	defer inUse()
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("không chạy được %s (cài: npm install -g @deepseek-ai/dsh): %w", bin, err)
	}
	defer proctrack.Track(ctx, cmd.Process.Pid)()

	res.Usage.Model = model
	turn := &dshTurn{emit: emit, fullAccess: req.FullAccess}
	conn := newACPConn(in, turn.update, turn.permission)
	readDone := make(chan struct{})
	go func() { conn.read(quiet.out(out)); close(readDone) }()

	runErr := turn.drive(runCtx, conn, req, model)
	in.Close() // dsh ends on stdin EOF
	select {
	case <-readDone:
	case <-time.After(10 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Cancel()
		}
		<-readDone
	}
	werr := cmd.Wait()

	res.Usage.DurationMS = time.Since(start).Milliseconds()
	res.Text, res.SessionID, res.Tools, res.Context = strings.TrimSpace(turn.answer.String()), turn.session, turn.tools, turn.context
	if err := quiet.err("DeepSeek Harness"); err != nil {
		return res, err
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		msg := runErr.Error()
		if tail := strings.TrimSpace(stderr.String()); tail != "" && conn.closed() {
			msg += ": " + truncate(lastLines(tail, 5), 400)
		}
		return res, fmt.Errorf("dsh: %s", msg)
	}
	if res.Text == "" && werr != nil && ctx.Err() == nil {
		return res, fmt.Errorf("dsh: %s", truncate(cmp.Or(strings.TrimSpace(stderr.String()), werr.Error()), 500))
	}
	if res.Text == "" && turn.stopReason != "" && turn.stopReason != "end_turn" {
		msg := "dừng lượt: " + turn.stopReason
		if turn.toolErr != "" {
			msg += " (" + truncate(turn.toolErr, 300) + ")"
		}
		return res, fmt.Errorf("dsh: %s", msg)
	}
	return res, nil
}

// lastLines is the end of a CLI's stderr, where its error is.
func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// dshTurn follows one prompt: what dsh says, the tools it calls, the context.
type dshTurn struct {
	emit       func(Event)
	fullAccess bool

	mu         sync.Mutex
	session    string
	answer     strings.Builder
	lastMsg    string
	tools      []storage.ToolCall
	toolNames  map[string]string // toolCallId → tool name
	toolErr    string
	context    ContextUse
	stopReason string
}

// drive opens (or resumes) the session and sends the prompt.
func (t *dshTurn) drive(ctx context.Context, conn *acpConn, req RunRequest, model string) error {
	if _, err := conn.call(ctx, "initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	mcp := dshMCPServers(req)
	open := func(servers []acpHTTPServer) (json.RawMessage, error) {
		if req.SessionID != "" {
			return conn.call(ctx, "session/resume", map[string]any{"sessionId": req.SessionID, "cwd": req.WorkDir, "mcpServers": servers})
		}
		return conn.call(ctx, "session/new", map[string]any{"cwd": req.WorkDir, "mcpServers": servers})
	}
	raw, err := open(mcp)
	if err != nil && len(mcp) > 1 && ctx.Err() == nil {
		// one gateway server that does not answer fails the whole session:
		// go on with office's own server only
		raw, err = open(mcp[:1])
	}
	if err != nil {
		return err
	}
	var opened struct {
		SessionID     string            `json:"sessionId"`
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	_ = json.Unmarshal(raw, &opened)
	t.mu.Lock()
	t.session = cmp.Or(opened.SessionID, req.SessionID)
	t.mu.Unlock()
	// a resumed session keeps the model it had; the route is the same, the model may not be
	want := `["` + dshRoute + `","` + model + `"]`
	if v, ok := configValue(opened.ConfigOptions, "model"); ok && v != want {
		_, _ = conn.call(ctx, "session/set_config_option", map[string]any{"sessionId": t.session, "configId": "model", "value": want})
	}
	if e := dshEffort(req.Effort); e != "" {
		if _, ok := configValue(opened.ConfigOptions, "reasoning_effort"); ok {
			_, _ = conn.call(ctx, "session/set_config_option", map[string]any{"sessionId": t.session, "configId": "reasoning_effort", "value": e})
		}
	}
	raw, err = conn.call(ctx, "session/prompt", map[string]any{"sessionId": t.session,
		"prompt": []map[string]any{{"type": "text", "text": strings.ToValidUTF8(dshPrompt(req), "�")}}})
	if err != nil {
		return err
	}
	var done struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(raw, &done)
	t.mu.Lock()
	t.stopReason = done.StopReason
	t.mu.Unlock()
	return nil
}

type acpConfigOption struct {
	ID           string `json:"id"`
	CurrentValue string `json:"currentValue"`
}

func configValue(opts []acpConfigOption, id string) (string, bool) {
	for _, o := range opts {
		if o.ID == id {
			return o.CurrentValue, true
		}
	}
	return "", false
}

// update is one session/update notification.
func (t *dshTurn) update(params json.RawMessage) {
	var p struct {
		Update struct {
			Kind       string          `json:"sessionUpdate"`
			MessageID  string          `json:"messageId"`
			Content    json.RawMessage `json:"content"`
			ToolCallID string          `json:"toolCallId"`
			Title      string          `json:"title"`
			Status     string          `json:"status"`
			RawInput   json.RawMessage `json:"rawInput"`
			Used       int             `json:"used"`
			Size       int             `json:"size"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	u := p.Update
	t.mu.Lock()
	defer t.mu.Unlock()
	switch u.Kind {
	case "agent_message_chunk":
		var c struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(u.Content, &c) != nil || c.Text == "" {
			return
		}
		text := c.Text
		// a new message after a tool call: keep the two apart
		if t.answer.Len() > 0 && u.MessageID != "" && u.MessageID != t.lastMsg {
			text = "\n\n" + text
		}
		t.lastMsg = u.MessageID
		t.answer.WriteString(text)
		t.emit(Event{Type: "text", Text: text})
	case "tool_call":
		if t.toolNames == nil {
			t.toolNames = map[string]string{}
		}
		t.toolNames[u.ToolCallID] = u.Title
		tc := storage.ToolCall{Name: u.Title, Summary: toolSummary(u.Title, u.RawInput)}
		t.tools = append(t.tools, tc)
		t.emit(Event{Type: "tool", Tool: &tc})
	case "tool_call_update":
		if u.Status == "failed" {
			t.toolErr = cmp.Or(acpContentText(u.Content), t.toolNames[u.ToolCallID]+" lỗi")
		}
	case "usage_update":
		if u.Used > 0 {
			t.context = ContextUse{Tokens: u.Used, Window: u.Size}
		}
	}
}

// acpContentText is the text of a tool call's content list.
func acpContentText(raw json.RawMessage) string {
	var items []struct {
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return ""
	}
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Content.Text)
	}
	return b.String()
}

// permission answers session/request_permission. Office decides through
// the sandbox preset and its own MCP policy, so dsh asks only for what lies
// beyond the preset: allowed with full access, else refused (no one is
// there to approve), except an MCP tool, which office itself guards.
func (t *dshTurn) permission(params json.RawMessage) any {
	var p struct {
		ToolCall struct {
			ToolCallID string `json:"toolCallId"`
			Title      string `json:"title"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(params, &p)
	t.mu.Lock()
	name := cmp.Or(t.toolNames[p.ToolCall.ToolCallID], p.ToolCall.Title)
	t.mu.Unlock()
	allow := t.fullAccess || isMCPTool(name)
	pick := ""
	for _, o := range p.Options {
		if (allow && strings.HasPrefix(o.Kind, "allow")) || (!allow && strings.HasPrefix(o.Kind, "reject")) {
			pick = o.OptionID
			break
		}
	}
	if pick == "" {
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	}
	return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": pick}}
}

// isMCPTool: dsh names an MCP tool after its server (office, other_office, a gateway server).
func isMCPTool(name string) bool {
	n := strings.ToLower(name)
	return strings.HasPrefix(n, "mcp__") || strings.HasPrefix(n, mcpserver.ServerName+"__") || strings.HasPrefix(n, mcpserver.ServerName+".") ||
		strings.HasPrefix(n, officetools.OtherServer)
}

// acpConn is a JSON-RPC 2.0 connection over newline-delimited JSON.
type acpConn struct {
	w         io.Writer
	wmu       sync.Mutex
	mu        sync.Mutex
	next      int
	pending   map[int]chan acpMsg
	done      chan struct{}
	onUpdate  func(json.RawMessage)
	onRequest func(json.RawMessage) any
}

type acpMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

func newACPConn(w io.Writer, onUpdate func(json.RawMessage), onPermission func(json.RawMessage) any) *acpConn {
	return &acpConn{w: w, pending: map[int]chan acpMsg{}, done: make(chan struct{}), onUpdate: onUpdate, onRequest: onPermission}
}

func (c *acpConn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}

func (c *acpConn) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// call sends a request and waits for its answer.
func (c *acpConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan acpMsg, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			msg := m.Error.Message
			if len(m.Error.Data) > 0 && string(m.Error.Data) != "null" {
				msg += " " + truncate(string(m.Error.Data), 300)
			}
			return nil, fmt.Errorf("%s: %s", method, msg)
		}
		return m.Result, nil
	case <-c.done:
		return nil, fmt.Errorf("%s: dsh đã thoát", method)
	case <-ctx.Done():
		if method == "session/prompt" {
			_ = c.send(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": params})
		}
		return nil, ctx.Err()
	}
}

// read dispatches what dsh writes until its stdout closes.
func (c *acpConn) read(r io.Reader) {
	defer close(c.done)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m acpMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Method == "session/update":
			c.onUpdate(m.Params)
		case m.Method != "" && len(m.ID) > 0: // a request of dsh's
			var result any = map[string]any{}
			if m.Method == "session/request_permission" {
				result = c.onRequest(m.Params)
			} else {
				_ = c.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
				continue
			}
			_ = c.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
		case m.Method == "" && len(m.ID) > 0:
			var id int
			if json.Unmarshal(m.ID, &id) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}
