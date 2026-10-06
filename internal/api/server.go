// Package api is the HTTP surface of the office server: dashboard REST API,
// health check, and (later) SSE and webhook ingress.
package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/cleanup"
	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/events"
	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/monitor"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/selfupdate"
	"github.com/tung1998/agent-office/internal/setup"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/sysinfo"
	"github.com/tung1998/agent-office/internal/transfer"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/usage"
)

// SessionCookie is the name of the login cookie.
const SessionCookie = "office_session"

const maxBody = 64 << 10

// Config wires the API.
type Config struct {
	Store          storage.Store
	Auth           *auth.Service
	AllowedOrigins []string       // extra origins allowed on state-changing requests (the dashboard)
	SecureCookies  bool           // set Secure on the cookie; required when served over HTTPS
	TrustedProxies []netip.Prefix // peers whose X-Forwarded-* headers are honoured (the dashboard proxy)
	Logger         *slog.Logger
	Version        string

	Providers  *provider.Service // nil disables the provider/model/repo routes (auth-only tests)
	Org        *orgmodel.Service
	Trigger    *trigger.Runner // automations: schedules and webhooks (nil = none)
	Setup      *setup.Assistant
	Transfer   *transfer.Service
	Usage      *usage.Service
	CLITools   *clitools.Manager // nil: installing/signing in CLIs from the dashboard is off
	Chat       *chat.Engine
	Burn       *burn.Service       // a project's agent running on its own (nil = off)
	Cleanup    *cleanup.Service    // data management (ADR-095; nil = off)
	Automation *automation.Service // nil: skills/agents/MCP management is off
	Ops        *ops.Manager        // nil: running project processes is off
	Monitors   *monitor.Service    // nil: health checks are off
	// MCP serves the office tools to agent runs (bearer token per run, no session).
	MCP http.Handler
	// Gateway forwards runs to the MCP servers office manages, /mcp/s/<name> (ADR-091; nil = off).
	Gateway *mcpgateway.Gateway
	// Actions are operations agents proposed; admins approve or reject them.
	Actions *actions.Service
	// Memory keeps agents' long-term notes (ADR-068).
	Memory *memory.Service
	// Events tell open dashboard pages what changed (ADR-072).
	Events *events.Bus
	// Updater rebuilds office from its source; nil when not supervised or
	// running an installed build without source.
	Updater *selfupdate.Updater
	// Supervised reports whether `office run` supervises this server.
	Supervised bool
	// Backup writes a copy of the data to a new folder and returns its path.
	Backup   func(ctx context.Context) (string, error)
	System   SystemInfo
	Office   *officetools.Toolbox // agents' tools: describe/list/get/propose_change use the config registry
	Channels ChannelReloader      // restarts a Telegram/Discord bot after its settings change (nil = off)
}

// SystemInfo tells the dashboard how this office is installed.
type SystemInfo struct {
	Mode        string `json:"mode"` // local | global
	HomeDir     string `json:"home_dir"`
	ProjectRoot string `json:"project_root,omitempty"`
}

type server struct {
	cfg     Config
	sys     *sysinfo.Sampler // the machine and office's processes (overview)
	live    liveStats        // its figures pushed to admins' open pages (ADR-078)
	push    livePush         // chats and "Cần xử lý" pushed with their data (ADR-078)
	origins map[string]bool
	log     *slog.Logger
	build   buildInfo // what is running, read once at startup

	automationMu    sync.Mutex             // guards automationLocks
	automationLocks map[string]*sync.Mutex // one project's automation create/update at a time (ADR-049 command dedupe)
}

// lockAutomations serializes create/update of a project's automations, so
// the check-then-write of a bot's custom /command (unique per channel) is
// never raced by two requests reading "free" before either writes.
func (s *server) lockAutomations(projectID string) func() {
	s.automationMu.Lock()
	if s.automationLocks == nil {
		s.automationLocks = map[string]*sync.Mutex{}
	}
	lk := s.automationLocks[projectID]
	if lk == nil {
		lk = &sync.Mutex{}
		s.automationLocks[projectID] = lk
	}
	s.automationMu.Unlock()
	lk.Lock()
	return lk.Unlock
}

// New returns the root handler.
func New(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Automation != nil && cfg.Automation.Stash == nil && cfg.Providers != nil && cfg.Store != nil {
		cfg.Automation.Stash = mcpStash{st: cfg.Store, box: cfg.Providers.Box()} // MCP servers turned off
	}
	s := &server{cfg: cfg, origins: map[string]bool{}, log: cfg.Logger, sys: sysinfo.New(cmp.Or(cfg.System.HomeDir, "/"))}
	src := ""
	if cfg.Updater != nil {
		src = cfg.Updater.Source().Root
	}
	s.build = loadBuild(cfg.Version, src)
	s.startStats()
	s.startLiveData()
	if cfg.Automation != nil && cfg.Automation.Health != nil && cfg.Events != nil {
		cfg.Automation.Health.OnDone = func(c automation.MCPCheck) {
			cfg.Events.Send(events.Event{Name: "mcp.status", Data: c}, admins)
		}
	}
	// the config registry behind propose_change and the generic tools (ADR-045)
	if cfg.Actions != nil {
		cfg.Actions.SetConfig(s)
	}
	if cfg.Office != nil {
		cfg.Office.SetConfig(s)
	}
	if m, ok := cfg.MCP.(interface {
		SetTokenAuth(func(context.Context, string) (officetools.Scope, bool))
	}); ok {
		m.SetTokenAuth(s.tokenScope) // people's own CLIs (ADR-047)
	}
	for _, o := range cfg.AllowedOrigins {
		s.origins[strings.TrimRight(strings.ToLower(o), "/")] = true
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/health", s.healthz) // same, reachable through the dashboard proxy

	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.Handle("GET /api/auth/me", s.requireAuth(http.HandlerFunc(s.me)))
	mux.Handle("POST /api/auth/password", s.requireAuth(http.HandlerFunc(s.changePassword)))
	mux.Handle("POST /api/auth/setup", s.requireAuth(http.HandlerFunc(s.setupAccount)))

	mux.Handle("GET /api/users", s.requireRole(storage.RoleAdmin, http.HandlerFunc(s.listUsers)))
	mux.Handle("POST /api/users", s.requireRole(storage.RoleAdmin, http.HandlerFunc(s.createUser)))
	mux.Handle("PATCH /api/users/{id}", s.requireRole(storage.RoleAdmin, http.HandlerFunc(s.updateUser)))
	mux.Handle("POST /api/users/{id}/reset-password", s.requireRole(storage.RoleAdmin, http.HandlerFunc(s.resetPassword)))
	mux.Handle("GET /api/audit", s.requireRole(storage.RoleAdmin, http.HandlerFunc(s.listAudit)))
	mux.Handle("GET /api/audit/stats", s.requireRole(storage.RoleAdmin, http.HandlerFunc(s.auditStats)))

	if cfg.Providers != nil && cfg.Org != nil {
		s.orgRoutes(mux)
	}
	if cfg.Trigger != nil {
		// automation webhooks: authenticated by each automation's token (ADR-040)
		mux.Handle("POST /hooks/{id}", cfg.Trigger.Webhook())
		mux.Handle("GET /hooks/{id}/jobs/{job}", cfg.Trigger.Webhook())
	}
	if cfg.Org != nil {
		s.triggerRoutes(mux)
	}

	return gzipJSON(s.securityHeaders(s.csrf(mux)))
}

// ---- middleware ----

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
)

func userFrom(r *http.Request) storage.User { return r.Context().Value(ctxUser).(storage.User) }
func sessionFrom(r *http.Request) storage.Session {
	return r.Context().Value(ctxSession).(storage.Session)
}

func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// csrf protects cookie-authenticated state changes. A cross-site page cannot
// send application/json without a CORS preflight (which we never grant), and
// a present Origin must be ours. SameSite=Lax on the cookie is the third layer.
func (s *server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/hooks/") {
			next.ServeHTTP(w, r) // no cookie: a token per automation, any content type
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(r, origin) {
			writeError(w, http.StatusForbidden, "origin not allowed")
			return
		}
		if r.ContentLength != 0 || r.Header.Get("Content-Type") != "" {
			mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mt != "application/json" {
				writeError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
				return
			}
		}
		limit := int64(maxBody)
		if r.URL.Path == "/api/transfer/import" || strings.HasPrefix(r.URL.Path, "/api/automation/") || strings.HasPrefix(r.URL.Path, "/mcp/s/") {
			limit = 8 << 20
		}
		if strings.HasSuffix(r.URL.Path, "/attachments") {
			limit = 15 << 20 // 10 MB file as base64
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

func (s *server) originAllowed(r *http.Request, origin string) bool {
	o := strings.TrimRight(strings.ToLower(origin), "/")
	if s.origins[o] {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	host := r.Host
	if s.trusted(r.RemoteAddr) {
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
			host = fh
		}
	}
	return strings.EqualFold(u.Host, host)
}

func (s *server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ck, err := r.Cookie(SessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		u, sess, err := s.cfg.Auth.Authenticate(r.Context(), ck.Value)
		if errors.Is(err, auth.ErrUnauthenticated) {
			s.clearCookie(w, r)
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		// the default admin does nothing but set its real email and password
		if u.MustChange && r.URL.Path != "/api/auth/me" && r.URL.Path != "/api/auth/setup" {
			writeError(w, http.StatusForbidden, "Đặt email và mật khẩu cho tài khoản mặc định trước")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, u)
		ctx = orgmodel.WithActor(ctx, "human:"+u.Email)
		ctx = context.WithValue(ctx, ctxSession, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *server) requireRole(role storage.Role, next http.Handler) http.Handler {
	return s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userFrom(r).Role != role {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// ---- helpers ----

func (s *server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.trusted(r.RemoteAddr) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	return host
}

func (s *server) secure(r *http.Request) bool {
	if s.cfg.SecureCookies || r.TLS != nil {
		return true
	}
	return s.trusted(r.RemoteAddr) && r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *server) setCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteLaxMode,
	})
}

func (s *server) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.secure(r), SameSite: http.SameSiteLaxMode,
	})
}

// trusted reports whether the direct peer is a configured proxy.
func (s *server) trusted(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range s.cfg.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("api: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
