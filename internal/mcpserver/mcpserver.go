// Package mcpserver serves the office tools to Claude Code over MCP
// (streamable HTTP, JSON responses, stateless; both the 2026-07-28 protocol
// and the legacy initialize one). Each agent run gets its own
// bearer token bound to one project and revoked when the run ends, so an
// agent can only read the project it works on.
package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/officetools"
)

// ServerName is the MCP server name agents see (tools appear as mcp__office__*).
const ServerName = "office"

// OtherPath is where the MCP other_office is served, next to /mcp (ADR-169):
// the other offices' tools, with the same run tokens.
const OtherPath = "/" + officetools.OtherServer

type grant struct {
	scope   officetools.Scope
	issued  time.Time
	expires time.Time
}

// MaxRun bounds a run's token (ADR-254): it lives until the run revokes it
// (a turn has no time limit of its own), at most this long, so a token whose
// revoke never ran does not stay valid for good.
const MaxRun = 24 * time.Hour

// keepEnded: how long office remembers a token that ended, to tell its run
// why it is refused (ADR-254).
const keepEnded = 6 * time.Hour

type ended struct {
	revoked bool
	issued  time.Time
	at      time.Time
}

// Server is the MCP endpoint.
type Server struct {
	tools   *officetools.Toolbox
	version string
	// Now is the clock (nil: time.Now); tests move it.
	Now func() time.Time

	mu     sync.Mutex
	grants map[string]grant
	ended  map[string]ended
}

// New builds a Server.
func New(tools *officetools.Toolbox, version string) *Server {
	return &Server{tools: tools, version: version, grants: map[string]grant{}, ended: map[string]ended{}}
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Grant issues a token for one run (scope: project, conversation/task, run)
// valid until revoke, at most ttl (<= 0 or over MaxRun: MaxRun); call revoke
// when the run ends.
func (s *Server) Grant(scope officetools.Scope, ttl time.Duration) (token string, revoke func()) {
	if ttl <= 0 || ttl > MaxRun {
		ttl = MaxRun
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token = hex.EncodeToString(b)
	s.mu.Lock()
	now := s.now()
	s.sweepLocked(now)
	s.grants[token] = grant{scope: scope, issued: now, expires: now.Add(ttl)}
	s.mu.Unlock()
	return token, func() {
		s.mu.Lock()
		if g, ok := s.grants[token]; ok {
			delete(s.grants, token)
			s.ended[token] = ended{revoked: true, issued: g.issued, at: s.now()}
		}
		s.mu.Unlock()
	}
}

// sweepLocked moves expired grants to ended and forgets ended tokens past
// keepEnded.
func (s *Server) sweepLocked(now time.Time) {
	for k, g := range s.grants {
		if !now.Before(g.expires) {
			delete(s.grants, k)
			s.ended[k] = ended{issued: g.issued, at: g.expires}
		}
	}
	for k, e := range s.ended {
		if now.Sub(e.at) > keepEnded {
			delete(s.ended, k)
		}
	}
}

// Sweep drops expired grants every tick until ctx ends: a run that never
// comes back would otherwise leave its token in memory (ADR-254).
func (s *Server) Sweep(ctx context.Context, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			s.sweepLocked(s.now())
			s.mu.Unlock()
		}
	}
}

// Authorized reports whether r carries a run's token: the MCP gateway (/mcp/s/<name>) takes the same ones (ADR-091).
func (s *Server) Authorized(r *http.Request) bool {
	_, ok := s.scope(r)
	return ok
}

// ScopeOf is the scope of r's token: the gateway's tool policy and agent
// assignment go by it (ADR-093).
func (s *Server) ScopeOf(r *http.Request) (officetools.Scope, bool) { return s.scope(r) }

// Live reports whether token is a run's that has not ended: a stdio server a
// live run called keeps running (ADR-254).
func (s *Server) Live(token string) bool {
	if token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.grants[token]
	return ok && s.now().Before(g.expires)
}

// RunToken is the bearer token r carries ("" if none).
func RunToken(r *http.Request) string {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return strings.TrimSpace(tok)
}

func (s *Server) scope(r *http.Request) (officetools.Scope, bool) {
	tok := RunToken(r)
	if tok == "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return officetools.Scope{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.grants[tok]
	if ok && s.now().Before(g.expires) {
		return g.scope, true
	}
	if ok { // expired: remembered to say why
		delete(s.grants, tok)
		s.ended[tok] = ended{issued: g.issued, at: g.expires}
	}
	return officetools.Scope{}, false
}

// Why says why r's token is refused, for the run (and office's log) to
// read instead of a bare 401 (ADR-254); "" when it is not refused.
func (s *Server) Why(r *http.Request) string {
	if _, ok := s.scope(r); ok {
		return ""
	}
	tok := RunToken(r)
	if tok == "" {
		return "thiếu token của lượt chạy (header Authorization: Bearer …)"
	}
	s.mu.Lock()
	e, ok := s.ended[tok]
	s.mu.Unlock()
	switch {
	case !ok:
		return "token của lượt chạy không hợp lệ: office không cấp token này (office đã khởi động lại?)"
	case e.revoked:
		return "token của lượt chạy đã bị thu hồi lúc " + e.at.Local().Format("15:04") + ": lượt đã kết thúc, lượt mới có token mới"
	default:
		return "token của lượt chạy đã hết hạn lúc " + e.at.Local().Format("15:04") + ", sau " + e.at.Sub(e.issued).Round(time.Minute).String() + " kể từ khi cấp"
	}
}

// deny answers a refused request with why.
func (s *Server) deny(w http.ResponseWriter, r *http.Request) {
	why := s.Why(r)
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized: "+why, http.StatusUnauthorized)
}

// Protocol versions served (ADR-091): the modern one is stateless (per-request
// _meta, server/discover); the legacy ones open with initialize.
const modernVersion = "2026-07-28"

var legacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// supported lists every version served, newest first.
func supported() []string { return append([]string{modernVersion}, legacyVersions...) }

// _meta keys of the modern protocol
const (
	metaVersion    = "io.modelcontextprotocol/protocolVersion"
	metaServerInfo = "io.modelcontextprotocol/serverInfo"
)

// MCP error codes of the modern protocol
const (
	codeHeaderMismatch     = -32020
	codeUnsupportedVersion = -32022
)

// otherInstructions introduce the MCP other_office (ADR-169).
const otherInstructions = "Các office khác đã ghép nối: xem bằng offices, giao việc bằng send, đọc chat bằng read_chat, tìm bằng search; trợ lý office chép project sang đây bằng import. Chỉ gửi những gì người dùng đồng ý giao."

const instructions = "Thông tin vận hành của project (tiến trình build/dev/test, docker compose, giám sát). Thao tác chỉ được đề xuất qua propose_action để người dùng duyệt."

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ServeHTTP handles POST (JSON-RPC message or batch). GET (server stream) is
// not offered; DELETE (end session) is accepted and ignored.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sc, ok := s.scope(r)
	if !ok {
		s.deny(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	default:
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var (
		reqs  []request
		batch bool
	)
	if t := strings.TrimSpace(string(body)); strings.HasPrefix(t, "[") {
		batch = true
		err = json.Unmarshal(body, &reqs)
	} else {
		var one request
		err = json.Unmarshal(body, &one)
		reqs = []request{one}
	}
	if err != nil {
		writeJSON(w, http.StatusOK, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	if batch && strings.Contains(string(body), metaVersion) { // the modern protocol has no batches
		writeJSON(w, http.StatusBadRequest, response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32600, Message: "batch not supported in " + modernVersion}})
		return
	}
	var (
		out    []response
		status = http.StatusOK
	)
	for _, q := range reqs {
		if len(q.ID) == 0 { // notification: no reply
			continue
		}
		res, st := s.handle(r, sc, q, strings.HasSuffix(r.URL.Path, OtherPath))
		out = append(out, res)
		status = st
	}
	if len(out) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if batch { // legacy only: each answer carries its own error
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeJSON(w, status, out[0])
}

// handle answers one request and says the HTTP status for it: a modern
// request's protocol errors are 400 and an unknown method 404, so a client
// that speaks both eras tells office from a legacy server.
func (s *Server) handle(r *http.Request, sc officetools.Scope, q request, other bool) (response, int) {
	res := response{JSONRPC: "2.0", ID: q.ID}
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	_ = json.Unmarshal(q.Params, &p)
	var version string
	_ = json.Unmarshal(p.Meta[metaVersion], &version)
	modern := version != ""
	if modern {
		if version != modernVersion {
			res.Error = &rpcError{Code: codeUnsupportedVersion, Message: "Unsupported protocol version",
				Data: map[string]any{"supported": []string{modernVersion}, "requested": version}}
			return res, http.StatusBadRequest
		}
		if msg := mismatch(r, q); msg != "" {
			res.Error = &rpcError{Code: codeHeaderMismatch, Message: msg}
			return res, http.StatusBadRequest
		}
	}
	var result map[string]any
	info, intro := s.info(), instructions
	if other {
		info["name"], intro = "agent-office "+officetools.OtherServer, otherInstructions
	}
	switch q.Method {
	case "initialize":
		var ip struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(q.Params, &ip)
		v := ip.ProtocolVersion
		if !slices.Contains(legacyVersions, v) { // one office does not speak: its newest legacy one
			v = legacyVersions[0]
		}
		result = map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
			"serverInfo":      info,
			"instructions":    intro,
		}
	case "server/discover":
		result = map[string]any{
			"supportedVersions": supported(),
			"capabilities":      map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
			"instructions":      intro,
			"ttlMs":             3_600_000,
			"cacheScope":        "private",
		}
	case "ping":
		result = map[string]any{}
	case "resources/list":
		list := []map[string]any{}
		if !other {
			list = s.resourcesList(sc)
		}
		result = map[string]any{"resources": list}
		if modern {
			result["ttlMs"], result["cacheScope"] = 0, "private"
		}
	case "resources/read":
		var rp struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(q.Params, &rp)
		if other {
			res.Error = &rpcError{Code: -32602, Message: "unknown resource"}
			return res, http.StatusOK
		}
		contents, rerr := s.resourceRead(r.Context(), sc, rp.URI)
		if rerr != nil {
			res.Error = rerr
			return res, http.StatusOK
		}
		result = contents
	case "tools/list":
		list := []map[string]any{}
		tools := s.tools.ToolsFor(sc)
		if other {
			tools = s.tools.OtherTools(r.Context(), sc)
		}
		for _, t := range tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema,
				"annotations": map[string]any{"readOnlyHint": t.ReadOnly}})
		}
		result = map[string]any{"tools": list}
		if modern { // tools depend on the token's scope: no shared cache
			result["ttlMs"], result["cacheScope"] = 0, "private"
		}
	case "tools/call":
		var cp struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		err := json.Unmarshal(q.Params, &cp)
		has, call := s.tools.Has, s.tools.Call
		if other {
			has = func(sc officetools.Scope, name string) bool { return s.tools.HasOther(r.Context(), sc, name) }
			call = s.tools.CallOther
		}
		if err != nil || !has(sc, cp.Name) {
			res.Error = &rpcError{Code: -32602, Message: "unknown tool"}
			return res, http.StatusOK
		}
		if other {
			result = recoverToolCall(cp.Name, func() map[string]any {
				text, isErr := call(r.Context(), sc, cp.Name, cp.Arguments)
				return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
			})
		} else {
			result = recoverToolCall(cp.Name, func() map[string]any {
				return s.tools.CallMCP(r.Context(), sc, cp.Name, cp.Arguments)
			})
		}
	default:
		res.Error = &rpcError{Code: -32601, Message: "method not found: " + q.Method}
		if modern {
			return res, http.StatusNotFound
		}
		return res, http.StatusOK
	}
	if modern || q.Method == "server/discover" {
		result["resultType"] = "complete"
		meta, _ := result["_meta"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
		}
		meta[metaServerInfo] = info
		result["_meta"] = meta
	}
	res.Result = result
	return res, http.StatusOK
}

// recoverToolCall runs fn and turns a panicking tool handler into a normal
// MCP error result instead of letting it take down the HTTP connection
// (a bad tool must fail that one call, not the agent's whole session).
func recoverToolCall(name string, fn func() map[string]any) (result map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("mcpserver: tool panic", "tool", name, "panic", r)
			result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("internal error in tool %q: %v", name, r)}},
				"isError": true,
			}
		}
	}()
	return fn()
}

func (s *Server) info() map[string]any {
	return map[string]any{"name": "agent-office", "version": s.version}
}

// resourcesList is the small, fixed set of resources a run's scope can read
// right now (ADR follow-up): the project's report, and, in a Burn chat, its
// open pieces. More (per-file, per-skill) can follow once this is in use.
func (s *Server) resourcesList(sc officetools.Scope) []map[string]any {
	var out []map[string]any
	if sc.ProjectID != "" && s.tools.Has(sc, "project_report") {
		out = append(out, map[string]any{
			"uri":         "office://project/" + sc.ProjectID + "/report",
			"name":        "project_report",
			"description": "Báo cáo vận hành project: việc theo bảng, agent, chi phí, lỗi",
			"mimeType":    "text/plain",
		})
	}
	if sc.ProjectID != "" && s.tools.Has(sc, "burn_list") {
		out = append(out, map[string]any{
			"uri":         "office://project/" + sc.ProjectID + "/burn",
			"name":        "burn_open",
			"description": "Các việc Burn đang mở trong hội thoại này",
			"mimeType":    "text/plain",
		})
	}
	return out
}

// resourceRead dispatches a resources/read to the matching tool and wraps its
// text as the resource's content. A tool panic or failure (isError) becomes
// an rpcError instead of either crashing the connection or being handed to
// the client as if it were the resource's text.
func (s *Server) resourceRead(ctx context.Context, sc officetools.Scope, uri string) (map[string]any, *rpcError) {
	prefix := "office://project/" + sc.ProjectID + "/"
	var name string
	var args json.RawMessage
	switch {
	case sc.ProjectID != "" && uri == prefix+"report" && s.tools.Has(sc, "project_report"):
		name, args = "project_report", json.RawMessage(`{}`)
	case sc.ProjectID != "" && uri == prefix+"burn" && s.tools.Has(sc, "burn_list"):
		name, args = "burn_list", json.RawMessage(`{"what":"open"}`)
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown resource: " + uri}
	}
	text, rerr := s.callResourceTool(ctx, sc, name, args)
	if rerr != nil {
		return nil, rerr
	}
	return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": "text/plain", "text": text}}}, nil
}

// callResourceTool runs the tool behind a resource, recovering a panic (as
// recoverToolCall does for tools/call) and turning an isError result into an
// rpcError rather than passing the error text off as resource content.
func (s *Server) callResourceTool(ctx context.Context, sc officetools.Scope, name string, args json.RawMessage) (text string, rerr *rpcError) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("mcpserver: resource tool panic", "tool", name, "panic", r)
			rerr = &rpcError{Code: -32603, Message: fmt.Sprintf("internal error in tool %q: %v", name, r)}
		}
	}()
	out, failed := s.tools.Call(ctx, sc, name, args)
	if failed {
		return "", &rpcError{Code: -32603, Message: out}
	}
	return out, nil
}

// mismatch checks the headers a modern request mirrors from its body
// (MCP-Protocol-Version, Mcp-Method, Mcp-Name); a header that is there must
// match. "" means fine.
func mismatch(r *http.Request, q request) string {
	if v := r.Header.Get("Mcp-Protocol-Version"); v != "" && v != modernVersion {
		return "Header mismatch: MCP-Protocol-Version " + v + " does not match body " + modernVersion
	}
	if m := r.Header.Get("Mcp-Method"); m != "" && m != q.Method {
		return "Header mismatch: Mcp-Method " + m + " does not match body " + q.Method
	}
	if n := r.Header.Get("Mcp-Name"); n != "" && q.Method == "tools/call" {
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(q.Params, &p)
		if dec, ok := decodeHeader(n); !ok || dec != p.Name {
			return "Header mismatch: Mcp-Name does not match body name"
		}
	}
	return ""
}

// decodeHeader undoes the =?base64?…?= form of a mirrored header value.
func decodeHeader(v string) (string, bool) {
	inner, ok := strings.CutPrefix(v, "=?base64?")
	if !ok {
		return v, true
	}
	inner, ok = strings.CutSuffix(inner, "?=")
	if !ok {
		return "", false
	}
	b, err := base64.StdEncoding.DecodeString(inner)
	return string(b), err == nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
