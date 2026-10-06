// Package mcpgateway is the office's MCP gateway (ADR-091): each MCP server
// office manages has its own address /mcp/s/<name>, so its tools keep their
// names (mcp__<name>__*). Office checks the run's token, adds the server's
// secrets (or its OAuth token, ADR-092) and forwards the JSON-RPC as it is;
// answers come back as JSON or an SSE stream. A stdio server is a process
// office runs and shares between runs (ADR-092).
package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// CallTimeout bounds one forwarded request (a tool call may take a while).
const CallTimeout = 10 * time.Minute

// Gateway forwards agent runs' MCP requests to the servers office manages.
type Gateway struct {
	Store storage.Store
	Box   Sealer
	// Auth accepts the bearer token of a run (or a person's own CLI, ADR-047).
	Auth func(r *http.Request) bool
	// Identify, when set, replaces Auth: it also says who calls, for the
	// agent assignment and the tool policy (ADR-093). With only Auth every
	// caller counts as a person who may only read (fail closed, ADR-094).
	Identify func(r *http.Request) (Caller, bool)
	// Propose turns a call of a tool that writes into a proposal (nil: such
	// calls are refused for agents that may not write).
	Propose Proposer
	Client  *http.Client // nil: a default one
	Log     *slog.Logger
	// OnStatus hears of a status office found on its own (a session that
	// expired), for open pages.
	OnStatus func(storage.MCPServer)
	// Env is what stdio servers run with (nil: office's own environment).
	Env []string
	// StdioIdle stops a stdio server nobody called for this long (0: 10 minutes).
	StdioIdle time.Duration

	logins logins
	pool   pool
	logged atomic.Int64 // calls logged, to prune the log now and then
}

func (g *Gateway) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return http.DefaultClient
}

func (g *Gateway) log() *slog.Logger {
	if g.Log != nil {
		return g.Log
	}
	return slog.Default()
}

// Names lists every enabled server of the machine.
func (g *Gateway) Names(ctx context.Context) []string {
	return g.NamesFor(ctx, Caller{Kind: "person"})
}

// NamesFor lists the servers c's run gets: enabled, of the machine, and
// given to its agent (ADR-093).
func (g *Gateway) NamesFor(ctx context.Context, c Caller) []string {
	list, err := g.Store.MCPServers().List(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range list {
		if m.Enabled && (m.Kind == "http" || m.Kind == "stdio") && m.Scope == "machine" && Assigned(m, c) {
			out = append(out, m.Name)
		}
	}
	return out
}

// caller authenticates r and says who it is.
func (g *Gateway) caller(r *http.Request) (Caller, bool) {
	if g.Identify != nil {
		return g.Identify(r)
	}
	if g.Auth != nil && g.Auth(r) { // no identity: fail closed, only tools that read (ADR-094)
		return Caller{Kind: "person"}, true
	}
	return Caller{}, false
}

// CheckServer checks m and saves the result. An HTTP server that wants an
// OAuth login office does not have ends as needs_login.
func (g *Gateway) CheckServer(ctx context.Context, m storage.MCPServer) (storage.MCPServer, error) {
	var (
		tools []storage.MCPTool
		err   error
	)
	if m.Kind == "stdio" {
		tools, err = g.checkStdio(ctx, m)
	} else {
		tools, err = g.checkHTTP(ctx, &m)
	}
	status, msg := "ok", ""
	if err != nil {
		status, msg, tools = "error", err.Error(), m.LastTools
		if errors.Is(err, ErrNeedsLogin) || errors.Is(err, ErrSessionExpired) {
			status = "needs_login"
		}
	}
	now := time.Now().UTC()
	if serr := g.Store.MCPServers().SetCheck(context.WithoutCancel(ctx), m.ID, status, msg, tools, now); serr != nil {
		return m, serr
	}
	m.LastCheckAt, m.LastCheckStatus, m.LastCheckError, m.LastTools = &now, status, msg, tools
	g.log().Info("mcp gateway: check", "server", m.Name, "status", status, "tools", len(tools))
	return m, nil
}

func (g *Gateway) checkHTTP(ctx context.Context, m *storage.MCPServer) ([]storage.MCPTool, error) {
	headers, err := OpenMap(g.Box, m.HeadersEnc)
	if err != nil {
		return nil, errors.New("không mở được bí mật đã lưu: nhập lại header")
	}
	tok, err := g.bearer(ctx, m, false)
	if err != nil {
		return nil, err
	}
	with := func(tok string) map[string]string {
		if tok != "" {
			headers["Authorization"] = "Bearer " + tok
		}
		return headers
	}
	tools, err := Check(ctx, g.client(), m.URL, with(tok))
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusUnauthorized {
		return tools, err
	}
	if tok != "" { // refused: refresh, then once more
		if tok, err = g.bearer(ctx, m, true); err != nil {
			return nil, err
		}
		tools, err = Check(ctx, g.client(), m.URL, with(tok))
		if errors.As(err, &se) && se.Code == http.StatusUnauthorized {
			return nil, ErrSessionExpired
		}
		return tools, err
	}
	// no token yet: a server with OAuth wants a login, not a typed token
	o, derr := Discover(ctx, g.client(), m.URL, se.WWWAuthenticate)
	if derr != nil {
		if len(headers) > 0 {
			return nil, err // a typed token, refused
		}
		return nil, fmt.Errorf("MCP cần đăng nhập nhưng %v; nếu nó dùng API key thì thêm ở header", derr)
	}
	// what is stored now: a login may have started (or ended) while this
	// check waited on the network, its client and tokens must stay (and no
	// login saves between this read and the save)
	lk := g.logins.lock(m.ID)
	lk.Lock()
	defer lk.Unlock()
	if fresh, ferr := g.Store.MCPServers().Get(ctx, m.ID); ferr == nil {
		m.OAuthEnc = fresh.OAuthEnc
	}
	if old, oerr := OpenOAuth(g.Box, m.OAuthEnc); oerr == nil {
		o.keep(old)
	}
	if serr := g.saveOAuth(ctx, m, o); serr != nil {
		return nil, serr
	}
	return nil, ErrNeedsLogin
}

// request headers passed on to the server (never the run's Authorization)
var passRequest = []string{"Accept", "Content-Type", "Mcp-Session-Id", "Mcp-Protocol-Version", "Last-Event-Id"}

// response headers passed back
var passResponse = []string{"Content-Type", "Mcp-Session-Id", "Mcp-Protocol-Version"}

// ServeHTTP handles /mcp/s/{name}.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, authed := g.caller(r)
	if !authed {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPost, http.MethodGet, http.MethodDelete:
	default:
		w.Header().Set("Allow", "POST, GET, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.PathValue("name")
	m, err := g.Store.MCPServers().GetByName(r.Context(), name)
	if err != nil || !m.Enabled || (m.Kind != "http" && m.Kind != "stdio") || !Assigned(m, c) {
		http.Error(w, "office không có MCP "+name, http.StatusNotFound)
		return
	}
	var body []byte
	if r.Method == http.MethodPost {
		if body, err = io.ReadAll(io.LimitReader(r.Body, 8<<20)); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}
	// the tool policy (ADR-093): a call that is not let through is answered here
	callID, callTool, callArgs, isCall := callOf(body)
	if isCall {
		if reply, ok := g.gateCall(r.Context(), m, c, callID, callTool, callArgs); !ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(reply)
			return
		}
	} else if batchCalls(body) && !c.CanWrite {
		http.Error(w, "office không nhận gọi tool theo lô (batch)", http.StatusBadRequest)
		return
	}
	start := time.Now()
	logged := func(status int, errMsg string) {
		if !isCall {
			return
		}
		st := "ok"
		if status >= 300 || errMsg != "" {
			st = "error"
			if errMsg == "" {
				errMsg = "HTTP " + strconv.Itoa(status)
			}
		}
		g.logCall(r.Context(), m, c, callTool, st, errMsg, time.Since(start).Milliseconds(), "")
	}
	if m.Kind == "stdio" {
		g.serveStdio(w, r, m, body)
		logged(http.StatusOK, "")
		return
	}
	headers, err := OpenMap(g.Box, m.HeadersEnc)
	if err != nil {
		g.log().Error("mcp gateway: open secrets", "server", name, "err", err)
		http.Error(w, "office không mở được bí mật của MCP "+name, http.StatusInternalServerError)
		return
	}
	rpc, tool := describe(body)

	ctx, cancel := r.Context(), context.CancelFunc(func() {})
	if r.Method != http.MethodGet { // GET is the server's own stream: open while the client listens
		ctx, cancel = context.WithTimeout(ctx, CallTimeout)
	}
	defer cancel()
	tok, err := g.bearer(ctx, &m, false)
	if err != nil {
		// office's login, not the run's token: no WWW-Authenticate (the CLI
		// would start a login against office)
		logged(http.StatusBadGateway, loginFail(name, err))
		http.Error(w, loginFail(name, err), http.StatusBadGateway)
		return
	}
	resp, err := g.send(ctx, r, m.URL, body, headers, tok)
	if err == nil && resp.StatusCode == http.StatusUnauthorized && tok != "" {
		resp.Body.Close() // refused: refresh, then once more
		if tok, err = g.bearer(ctx, &m, true); err != nil {
			logged(http.StatusBadGateway, loginFail(name, err))
			http.Error(w, loginFail(name, err), http.StatusBadGateway)
			return
		}
		resp, err = g.send(ctx, r, m.URL, body, headers, tok)
	}
	if err != nil {
		if r.Context().Err() == nil {
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err // the URL may carry a key
			}
			g.log().Warn("mcp gateway", "server", name, "rpc", rpc, "tool", tool, "err", err.Error(), "ms", time.Since(start).Milliseconds())
			logged(http.StatusBadGateway, err.Error())
			http.Error(w, "office không gọi được MCP "+name+": "+err.Error(), http.StatusBadGateway)
		}
		return
	}
	defer resp.Body.Close()
	g.log().Info("mcp gateway", "server", name, "method", r.Method, "rpc", rpc, "tool", tool, "status", resp.StatusCode, "ms", time.Since(start).Milliseconds())
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		if tok != "" && resp.StatusCode == http.StatusUnauthorized {
			o, _ := OpenOAuth(g.Box, m.OAuthEnc)
			_ = g.expire(ctx, &m, o) // even a fresh token is refused
			logged(resp.StatusCode, loginFail(name, ErrSessionExpired))
			http.Error(w, loginFail(name, ErrSessionExpired), http.StatusBadGateway)
			return
		}
		logged(resp.StatusCode, "")
		http.Error(w, "MCP "+name+" từ chối token đã lưu trong office (HTTP "+strconv.Itoa(resp.StatusCode)+"): sửa trên dashboard", http.StatusBadGateway)
		return
	}
	logged(resp.StatusCode, "")
	for _, h := range passResponse {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt == "text/event-stream" {
		stream(w, resp.Body)
		return
	}
	_, _ = io.Copy(w, resp.Body)
}

// loginFail says why office could not use its OAuth token for name (the
// person fixes it on the dashboard).
func loginFail(name string, err error) string {
	msg := "MCP " + name + ": " + err.Error()
	if errors.Is(err, ErrNeedsLogin) || errors.Is(err, ErrSessionExpired) {
		msg += " trên dashboard"
	}
	return msg
}

// send forwards one request to an HTTP server with its secrets and, when it
// has one, office's OAuth token.
func (g *Gateway) send(ctx context.Context, r *http.Request, target string, body []byte, headers map[string]string, tok string) (*http.Response, error) {
	up, err := http.NewRequestWithContext(ctx, r.Method, target, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("URL không hợp lệ")
	}
	for _, h := range passRequest {
		if v := r.Header.Get(h); v != "" {
			up.Header.Set(h, v)
		}
	}
	for k, v := range headers {
		up.Header.Set(k, v)
	}
	if tok != "" {
		up.Header.Set("Authorization", "Bearer "+tok)
	}
	return g.client().Do(up)
}

// stream copies an SSE body, flushing as events arrive.
func stream(w http.ResponseWriter, body io.Reader) {
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// describe names a JSON-RPC request for the log: its method and, for a tool
// call, the tool (never the arguments).
func describe(body []byte) (rpc, tool string) {
	var one struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	t := bytes.TrimSpace(body)
	if len(t) == 0 {
		return "", ""
	}
	if t[0] == '[' {
		var batch []json.RawMessage
		if json.Unmarshal(t, &batch) != nil || len(batch) == 0 {
			return "batch", ""
		}
		_ = json.Unmarshal(batch[0], &one)
		return "batch:" + one.Method, one.Params.Name
	}
	_ = json.Unmarshal(t, &one)
	if one.Method != "tools/call" {
		one.Params.Name = ""
	}
	return one.Method, one.Params.Name
}
