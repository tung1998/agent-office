package api

import (
	"cmp"
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/events"
	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/storage"
)

// The office's MCP servers behind its gateway (ADR-091).
func (s *server) mcpServerRoutes(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("/mcp/s/{name}", s.cfg.Gateway) // authenticated by the run's token, like /mcp
	mux.Handle("GET /api/mcp/servers", admin(s.listMCPServers))
	mux.Handle("POST /api/mcp/servers", admin(s.createMCPServer))
	mux.Handle("PATCH /api/mcp/servers/{id}", admin(s.updateMCPServer))
	mux.Handle("DELETE /api/mcp/servers/{id}", admin(s.deleteMCPServer))
	mux.Handle("POST /api/mcp/servers/{id}/check", admin(s.checkMCPServer))
	mux.Handle("POST /api/mcp/servers/{id}/oauth/start", admin(s.startMCPLogin))
	mux.Handle("POST /api/mcp/servers/{id}/oauth/logout", admin(s.logoutMCP))
	mux.Handle("GET "+mcpgateway.CallbackPath, admin(s.mcpLoginCallback))
	mux.Handle("POST /api/mcp/oauth/finish", admin(s.finishMCPLoginPaste))
	// ADR-093: who gets each server, its call log, moving servers into office
	mux.Handle("GET /api/mcp/agents", admin(s.mcpAgents))
	mux.Handle("GET /api/mcp/servers/{id}/calls", admin(s.mcpCalls))
	mux.Handle("GET /api/mcp/calls", admin(s.mcpCalls))
	mux.Handle("GET /api/mcp/import", admin(s.mcpImportList))
	mux.Handle("POST /api/mcp/import", admin(s.mcpImport))
	mux.Handle("POST /api/mcp/servers/{id}/put-back", admin(s.mcpPutBack))
	s.cfg.Gateway.OnStatus = func(m storage.MCPServer) { s.sendMCPStatus(s.mcpServerDTO(m)) }
}

// mcpOAuthDTO is what the dashboard sees of a login: never a token.
type mcpOAuthDTO struct {
	Required     bool       `json:"required"`  // the server has an authorization server
	LoggedIn     bool       `json:"logged_in"` // office holds a token
	Expired      bool       `json:"expired"`   // a refresh failed
	ExpiresAt    *time.Time `json:"expires_at"`
	ClientManual bool       `json:"client_manual"`
	ClientID     string     `json:"client_id"` // the typed one only
	HasSecret    bool       `json:"has_secret"`
	Dynamic      bool       `json:"dynamic"` // office can register itself
}

type mcpServerDTO struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Kind            string            `json:"kind"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers"` // values masked
	Command         string            `json:"command"`
	Args            []string          `json:"args"`
	Env             map[string]string `json:"env"` // values masked
	OAuth           *mcpOAuthDTO      `json:"oauth"`
	Scope           string            `json:"scope"`
	Origin          string            `json:"origin"`
	Enabled         bool              `json:"enabled"`
	Path            string            `json:"path"` // its address on office
	LastCheckAt     *time.Time        `json:"last_check_at"`
	LastCheckStatus string            `json:"last_check_status"`
	LastCheckError  string            `json:"last_check_error"`
	Tools           []mcpToolDTO      `json:"tools"`
	Agents          []string          `json:"agents"`        // [] = every agent
	TrustedTools    []string          `json:"trusted_tools"` // tools that write but run without approval
	MovedFrom       *mcpOriginDTO     `json:"moved_from"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// mcpToolDTO is a tool without its input schema (the list stays small).
type mcpToolDTO struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ReadOnly    *bool  `json:"read_only,omitempty"`
}

// mcpOriginDTO is where a moved server came from.
type mcpOriginDTO struct {
	Type    string `json:"type"`
	Label   string `json:"label"`
	Name    string `json:"name"`
	MovedAt string `json:"moved_at"`
	Backup  bool   `json:"backup"`
}

// mcpServerDTO never carries a secret: header and env values come masked,
// tokens not at all.
func (s *server) mcpServerDTO(m storage.MCPServer) mcpServerDTO {
	box := s.cfg.Providers.Box()
	headers, err := mcpgateway.OpenMap(box, m.HeadersEnc)
	if err != nil {
		headers = map[string]string{}
	}
	env, err := mcpgateway.OpenMap(box, m.EnvEnc)
	if err != nil {
		env = map[string]string{}
	}
	var oa *mcpOAuthDTO
	if o, err := mcpgateway.OpenOAuth(box, m.OAuthEnc); err == nil && (o.Discovered() || o.ClientManual) {
		oa = &mcpOAuthDTO{Required: o.Discovered(), LoggedIn: o.LoggedIn(), Expired: o.Expired, ClientManual: o.ClientManual,
			HasSecret: o.ClientSecret != "", Dynamic: o.RegistrationEndpoint != ""}
		if !o.ExpiresAt.IsZero() {
			oa.ExpiresAt = &o.ExpiresAt
		}
		if o.ClientManual {
			oa.ClientID = o.ClientID
		}
	}
	tools := make([]mcpToolDTO, 0, len(m.LastTools))
	for _, t := range m.LastTools {
		tools = append(tools, mcpToolDTO{Name: t.Name, Description: t.Description, ReadOnly: t.ReadOnly})
	}
	var from *mcpOriginDTO
	if o, ok := originOf(m); ok {
		from = &mcpOriginDTO{Type: o.Type, Label: o.Label, Name: o.Name, MovedAt: o.MovedAt, Backup: o.Backup != ""}
	}
	return mcpServerDTO{ID: m.ID, Name: m.Name, Kind: m.Kind, URL: m.URL, Headers: mcpgateway.MaskMap(headers),
		Command: m.Command, Args: m.Args, Env: mcpgateway.MaskMap(env), OAuth: oa, Scope: m.Scope, Origin: m.Origin,
		Enabled: m.Enabled, Path: "/mcp/s/" + m.Name, LastCheckAt: m.LastCheckAt, LastCheckStatus: m.LastCheckStatus, LastCheckError: m.LastCheckError,
		Tools: tools, Agents: m.Agents, TrustedTools: m.TrustedTools, MovedFrom: from, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
}

// auditMCP is what the change log keeps of a server (masked, no check results).
func (s *server) auditMCP(m storage.MCPServer) map[string]any {
	d := s.mcpServerDTO(m)
	out := map[string]any{"name": d.Name, "kind": d.Kind, "scope": d.Scope, "origin": d.Origin, "enabled": d.Enabled,
		"agents": d.Agents, "trusted_tools": d.TrustedTools}
	if d.Kind == "stdio" {
		out["command"], out["args"], out["env"] = d.Command, d.Args, d.Env
	} else {
		out["url"], out["headers"] = d.URL, d.Headers
	}
	if d.OAuth != nil && d.OAuth.ClientManual {
		out["oauth_client_id"] = d.OAuth.ClientID
	}
	return out
}

type mcpServerInput struct {
	Name    *string            `json:"name"`
	Kind    string             `json:"kind"`
	URL     *string            `json:"url"`
	Headers *map[string]string `json:"headers"` // write only; an empty value keeps the stored one
	Command *string            `json:"command"`
	Args    *[]string          `json:"args"`
	Env     *map[string]string `json:"env"` // like headers
	// a client typed on the form, for an authorization server office cannot
	// register with; "" clears it. An empty secret keeps the stored one.
	OAuthClientID     *string `json:"oauth_client_id"`
	OAuthClientSecret *string `json:"oauth_client_secret"`
	Scope             *string `json:"scope"`
	Enabled           *bool   `json:"enabled"`
	// ADR-093: agent ids that get it ([] = every agent), tools trusted to write
	Agents       *[]string `json:"agents"`
	TrustedTools *[]string `json:"trusted_tools"`
}

// cleanList trims, drops empties and duplicates.
func cleanList(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// sealMerged applies an edit of a sealed map (headers, env).
func sealMerged(box mcpgateway.Sealer, enc string, edit map[string]string) (string, error) {
	old, err := mcpgateway.OpenMap(box, enc)
	if err != nil {
		old = map[string]string{}
	}
	return mcpgateway.SealMap(box, mcpgateway.MergeHeaders(old, edit))
}

func (s *server) applyMCPServer(in mcpServerInput, m *storage.MCPServer) error {
	if in.Name != nil {
		m.Name = strings.TrimSpace(*in.Name)
	}
	if !mcpgateway.ValidName(m.Name) {
		return errors.New(`tên chỉ gồm chữ thường, số và "-" (vd context7), không quá 40 ký tự, không dùng "office"`)
	}
	if in.Scope != nil {
		m.Scope = strings.TrimSpace(*in.Scope)
	}
	if m.Scope != "machine" { // GĐ1: machine-wide only
		return errors.New("phạm vi hiện chỉ có machine (toàn máy)")
	}
	box := s.cfg.Providers.Box()
	if m.Kind == "stdio" {
		if in.Command != nil {
			m.Command = strings.TrimSpace(*in.Command)
		}
		if m.Command == "" {
			return errors.New("cần lệnh chạy MCP (vd npx)")
		}
		if in.Args != nil {
			m.Args = []string{}
			for _, a := range *in.Args {
				if a = strings.TrimSpace(a); a != "" {
					m.Args = append(m.Args, a)
				}
			}
		}
		if in.Env != nil {
			enc, err := sealMerged(box, m.EnvEnc, *in.Env)
			if err != nil {
				return err
			}
			m.EnvEnc = enc
		}
	} else {
		if in.URL != nil {
			m.URL = strings.TrimSpace(*in.URL)
		}
		if u, err := url.Parse(m.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("URL không hợp lệ: cần dạng https://…")
		}
		if in.Headers != nil {
			enc, err := sealMerged(box, m.HeadersEnc, *in.Headers)
			if err != nil {
				return err
			}
			m.HeadersEnc = enc
		}
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if in.Agents != nil {
		m.Agents = cleanList(*in.Agents)
	}
	if in.TrustedTools != nil {
		m.TrustedTools = cleanList(*in.TrustedTools)
	}
	return nil
}

// applyMCPOAuth applies the form's OAuth client and, when the URL changed,
// forgets what was found about the old one (a typed client stays).
func (s *server) applyMCPOAuth(in mcpServerInput, old, m *storage.MCPServer) error {
	box := s.cfg.Providers.Box()
	o, err := mcpgateway.OpenOAuth(box, m.OAuthEnc)
	if err != nil {
		o = mcpgateway.OAuth{}
	}
	if old != nil && old.URL != m.URL {
		o = mcpgateway.OAuth{ClientID: o.ClientID, ClientSecret: o.ClientSecret, ClientManual: o.ClientManual}
		if !o.ClientManual {
			o = mcpgateway.OAuth{}
		}
	}
	if in.OAuthClientID != nil {
		id := strings.TrimSpace(*in.OAuthClientID)
		switch {
		case id == "":
			if o.ClientManual {
				o.ClientID, o.ClientSecret, o.ClientManual, o.AuthMethod = "", "", false, ""
			}
		case id != o.ClientID || !o.ClientManual:
			o.ClientID, o.ClientSecret, o.ClientManual, o.RedirectURIs = id, "", true, nil
			o.AccessToken, o.RefreshToken, o.ExpiresAt = "", "", time.Time{}
		}
	}
	if in.OAuthClientSecret != nil && o.ClientManual {
		if sec := strings.TrimSpace(*in.OAuthClientSecret); sec != "" {
			o.ClientSecret = sec
		}
	}
	enc, err := mcpgateway.SealOAuth(box, o)
	if err != nil {
		return err
	}
	m.OAuthEnc = enc
	return nil
}

func (s *server) listMCPServers(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.MCPServers().List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]mcpServerDTO, 0, len(list))
	for _, m := range list {
		out = append(out, s.mcpServerDTO(m))
	}
	// calls of the last 24 hours per server (ADR-093)
	stats := map[string]storage.MCPCallStat{}
	if st, err := s.cfg.Store.MCPCalls().Stats(r.Context(), time.Now().UTC().Add(-24*time.Hour)); err == nil {
		for _, x := range st {
			stats[x.ServerID] = x
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": out, "stats": stats})
}

func (s *server) createMCPServer(w http.ResponseWriter, r *http.Request) {
	var in mcpServerInput
	if !decode(w, r, &in) {
		return
	}
	kind := cmp.Or(in.Kind, "http")
	if kind != "http" && kind != "stdio" {
		writeError(w, http.StatusBadRequest, "loại MCP chỉ có http hoặc stdio")
		return
	}
	m := storage.MCPServer{Kind: kind, Scope: "machine", Origin: "manual", Enabled: true}
	if err := s.applyMCPServer(in, &m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if kind == "http" {
		if err := s.applyMCPOAuth(in, nil, &m); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	m, err := s.cfg.Store.MCPServers().Create(r.Context(), m)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "mcp_server.create", ResourceID: m.ID, After: s.auditMCP(m)})
	s.checkMCPLater(m)
	writeJSON(w, http.StatusCreated, map[string]any{"server": s.mcpServerDTO(m)})
}

func (s *server) updateMCPServer(w http.ResponseWriter, r *http.Request) {
	var in mcpServerInput
	if !decode(w, r, &in) {
		return
	}
	m, err := s.cfg.Store.MCPServers().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if in.Kind != "" && in.Kind != m.Kind {
		writeError(w, http.StatusBadRequest, "không đổi được loại MCP")
		return
	}
	old := m
	if err := s.applyMCPServer(in, &m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if m.Kind == "http" {
		if err := s.applyMCPOAuth(in, &old, &m); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	if err := s.cfg.Store.MCPServers().Update(r.Context(), m); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if m.OAuthEnc != old.OAuthEnc {
		if err := s.cfg.Store.MCPServers().SetOAuth(r.Context(), m.ID, m.OAuthEnc); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	s.audit(r, audit.Change{Action: "mcp_server.update", ResourceID: m.ID, Before: s.auditMCP(old), After: s.auditMCP(m),
		Detail: map[string]any{"headers_changed": old.HeadersEnc != m.HeadersEnc, "env_changed": old.EnvEnc != m.EnvEnc, "oauth_changed": old.OAuthEnc != m.OAuthEnc}})
	changed := old.URL != m.URL || old.HeadersEnc != m.HeadersEnc || old.Command != m.Command || old.EnvEnc != m.EnvEnc ||
		strings.Join(old.Args, "\x00") != strings.Join(m.Args, "\x00") || old.OAuthEnc != m.OAuthEnc
	if !m.Enabled {
		s.cfg.Gateway.Forget(m.ID) // a stdio process stops with it
	}
	if m.Enabled && (changed || !old.Enabled) {
		s.checkMCPLater(m)
	}
	m, _ = s.cfg.Store.MCPServers().Get(r.Context(), m.ID)
	writeJSON(w, http.StatusOK, map[string]any{"server": s.mcpServerDTO(m)})
}

func (s *server) deleteMCPServer(w http.ResponseWriter, r *http.Request) {
	m, err := s.cfg.Store.MCPServers().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Store.MCPServers().Delete(r.Context(), m.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	s.cfg.Gateway.Forget(m.ID)
	s.audit(r, audit.Change{Action: "mcp_server.delete", ResourceID: m.ID, Before: s.auditMCP(m)})
	w.WriteHeader(http.StatusNoContent)
}

// checkMCPServer connects to the server like a client (a few seconds) and
// returns what it found; open pages get it as mcp.gateway.status.
func (s *server) checkMCPServer(w http.ResponseWriter, r *http.Request) {
	m, err := s.cfg.Store.MCPServers().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if m, err = s.cfg.Gateway.CheckServer(r.Context(), m); err != nil {
		s.internal(w, r, err)
		return
	}
	d := s.mcpServerDTO(m)
	s.sendMCPStatus(d)
	writeJSON(w, http.StatusOK, map[string]any{"server": d})
}

// startMCPLogin begins an OAuth login: the dashboard opens the returned URL
// in a new tab. origin is the address the person opened office with (the
// callback comes back there), so it must be one office answers on.
func (s *server) startMCPLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Origin string `json:"origin"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := url.Parse(in.Origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || !s.originAllowed(r, in.Origin) {
		writeError(w, http.StatusBadRequest, "địa chỉ mở office không hợp lệ")
		return
	}
	m, err := s.cfg.Store.MCPServers().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	origin := u.Scheme + "://" + u.Host
	authURL, err := s.cfg.Gateway.StartLogin(r.Context(), m, userFrom(r).ID, origin)
	if errors.Is(err, mcpgateway.ErrNeedsClient) { // the dashboard opens the form
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "code": "needs_client"})
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.auditAction(r, "mcp_server.login_start", m.ID, map[string]any{"name": m.Name})
	// paste: the callback is localhost, which this browser cannot reach; the
	// person pastes the address it lands on (finishMCPLoginPaste)
	writeJSON(w, http.StatusOK, map[string]any{"url": authURL, "paste": mcpgateway.PasteBack(origin)})
}

// finishMCPLogin ends a login from the callback's query, telling open pages.
func (s *server) finishMCPLogin(r *http.Request, q url.Values) (storage.MCPServer, error) {
	m, err := s.cfg.Gateway.FinishLogin(r.Context(), userFrom(r).ID, q.Get("state"), q.Get("code"), q.Get("error"))
	if err == nil {
		s.auditAction(r, "mcp_server.login", m.ID, map[string]any{"name": m.Name})
		s.sendMCPStatus(s.mcpServerDTO(m))
		s.checkMCPLater(m)
	} else if m.ID != "" {
		if fresh, gerr := s.cfg.Store.MCPServers().Get(r.Context(), m.ID); gerr == nil {
			s.sendMCPStatus(s.mcpServerDTO(fresh))
		}
	}
	return m, err
}

// finishMCPLoginPaste ends a login whose callback went to localhost: the
// person pastes the address the browser landed on (it carries state and code).
func (s *server) finishMCPLoginPaste(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil || u.Query().Get("state") == "" {
		writeError(w, http.StatusBadRequest, "link không đúng: dán nguyên địa chỉ trên thanh trình duyệt sau khi đăng nhập (có state=…)")
		return
	}
	m, err := s.finishMCPLogin(r, u.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": s.mcpServerDTO(m)})
}

// mcpLoginCallback is where the authorization server sends the person back;
// it answers with a small page that closes itself (or goes back to the MCP
// tab).
func (s *server) mcpLoginCallback(w http.ResponseWriter, r *http.Request) {
	m, err := s.finishMCPLogin(r, r.URL.Query())
	ok := err == nil
	msg := "Đã kết nối " + m.Name + ". Có thể đóng tab này."
	if !ok {
		msg = "Không kết nối được"
		if m.Name != "" {
			msg += " " + m.Name
		}
		msg += ": " + err.Error()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer") // the URL carries the code
	status := http.StatusOK
	if !ok {
		status = http.StatusBadRequest
	}
	w.WriteHeader(status)
	script := ""
	if ok {
		script = `try{if(window.opener){window.close()}}catch(e){}setTimeout(function(){location.replace('/library?tab=mcp')},1500)`
	}
	_, _ = w.Write([]byte(`<!doctype html><html lang="vi"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Agent Office</title>` +
		`<body style="font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem"><p>` + html.EscapeString(msg) +
		`</p><p><a href="/library?tab=mcp">Về trang MCP</a></p><script>` + script + `</script></body></html>`))
}

// logoutMCP forgets office's OAuth tokens for the server.
func (s *server) logoutMCP(w http.ResponseWriter, r *http.Request) {
	m, err := s.cfg.Store.MCPServers().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if m, err = s.cfg.Gateway.Logout(r.Context(), m); err != nil {
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "mcp_server.logout", m.ID, map[string]any{"name": m.Name})
	writeJSON(w, http.StatusOK, map[string]any{"server": s.mcpServerDTO(m)})
}

// checkMCPLater checks a server just saved, in the background.
func (s *server) checkMCPLater(m storage.MCPServer) {
	if !m.Enabled {
		return
	}
	go func() {
		m, err := s.cfg.Gateway.CheckServer(context.Background(), m)
		if err != nil {
			s.log.Warn("mcp gateway: check", "server", m.Name, "err", err)
			return
		}
		s.sendMCPStatus(s.mcpServerDTO(m))
	}()
}

func (s *server) sendMCPStatus(d mcpServerDTO) {
	if s.cfg.Events != nil {
		s.cfg.Events.Send(events.Event{Name: "mcp.gateway.status", Data: d}, admins)
	}
}
