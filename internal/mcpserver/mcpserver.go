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
	"io"
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
	expires time.Time
}

// Server is the MCP endpoint.
type Server struct {
	tools   *officetools.Toolbox
	version string

	mu     sync.Mutex
	grants map[string]grant
	// personal tokens of people's own CLIs (ADR-047)
	tokenAuth func(ctx context.Context, token string) (officetools.Scope, bool)
}

// SetTokenAuth accepts personal tokens besides the per-run grants.
func (s *Server) SetTokenAuth(fn func(ctx context.Context, token string) (officetools.Scope, bool)) {
	s.tokenAuth = fn
}

// New builds a Server.
func New(tools *officetools.Toolbox, version string) *Server {
	return &Server{tools: tools, version: version, grants: map[string]grant{}}
}

// Grant issues a token for one run (scope: project, conversation/task, run)
// valid for ttl; call revoke when done.
func (s *Server) Grant(scope officetools.Scope, ttl time.Duration) (token string, revoke func()) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token = hex.EncodeToString(b)
	s.mu.Lock()
	now := time.Now()
	for k, g := range s.grants { // drop expired grants
		if now.After(g.expires) {
			delete(s.grants, k)
		}
	}
	s.grants[token] = grant{scope: scope, expires: now.Add(ttl)}
	s.mu.Unlock()
	return token, func() {
		s.mu.Lock()
		delete(s.grants, token)
		s.mu.Unlock()
	}
}

// Authorized reports whether r carries a run's token or a personal token:
// the MCP gateway (/mcp/s/<name>) takes the same ones (ADR-091).
func (s *Server) Authorized(r *http.Request) bool {
	_, ok := s.scope(r)
	return ok
}

// ScopeOf is the scope of r's token: the gateway's tool policy and agent
// assignment go by it (ADR-093).
func (s *Server) ScopeOf(r *http.Request) (officetools.Scope, bool) { return s.scope(r) }

func (s *Server) scope(r *http.Request) (officetools.Scope, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return officetools.Scope{}, false
	}
	s.mu.Lock()
	g, ok := s.grants[tok]
	s.mu.Unlock()
	if ok && time.Now().Before(g.expires) {
		return g.scope, true
	}
	if s.tokenAuth != nil {
		return s.tokenAuth(r.Context(), tok)
	}
	return officetools.Scope{}, false
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
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      info,
			"instructions":    intro,
		}
	case "server/discover":
		result = map[string]any{
			"supportedVersions": supported(),
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions":      intro,
			"ttlMs":             3_600_000,
			"cacheScope":        "private",
		}
	case "ping":
		result = map[string]any{}
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
			text, isErr := call(r.Context(), sc, cp.Name, cp.Arguments)
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
		} else {
			result = s.tools.CallMCP(r.Context(), sc, cp.Name, cp.Arguments)
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

func (s *Server) info() map[string]any {
	return map[string]any{"name": "agent-office", "version": s.version}
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
