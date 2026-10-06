package mcpgateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// LoginTTL is how long a started login may take.
const LoginTTL = 10 * time.Minute

// refreshBefore: a token this close to expiry is refreshed first.
const refreshBefore = 60 * time.Second

// CallbackPath is where the authorization server sends the person back.
const CallbackPath = "/api/mcp/oauth/callback"

// ErrNeedsLogin: the server wants an OAuth login office does not have.
var ErrNeedsLogin = errors.New("cần đăng nhập: bấm Kết nối")

// ErrSessionExpired: a refresh failed, the person logs in again.
var ErrSessionExpired = errors.New("phiên hết hạn: bấm Kết nối lại")

// ErrBadState: the callback does not match a login this person started.
var ErrBadState = errors.New("phiên đăng nhập không hợp lệ hoặc đã hết hạn: bấm Kết nối lại")

type pendingLogin struct {
	user, server       string
	verifier, redirect string
	exp                time.Time
}

type logins struct {
	mu      sync.Mutex
	pending map[string]pendingLogin // by state
	locks   map[string]*sync.Mutex  // one refresh at a time per server
}

func (l *logins) lock(id string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.locks == nil {
		l.locks = map[string]*sync.Mutex{}
	}
	m := l.locks[id]
	if m == nil {
		m = &sync.Mutex{}
		l.locks[id] = m
	}
	return m
}

func (l *logins) put(state string, p pendingLogin) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending == nil {
		l.pending = map[string]pendingLogin{}
	}
	now := time.Now()
	for k, v := range l.pending {
		if now.After(v.exp) {
			delete(l.pending, k)
		}
	}
	l.pending[state] = p
}

// take removes the login of state (once only) and returns it when it is the
// person's own and still fresh.
func (l *logins) take(state, user string) (pendingLogin, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.pending[state]
	delete(l.pending, state)
	return p, ok && state != "" && p.user == user && time.Now().Before(p.exp)
}

func (g *Gateway) saveOAuth(ctx context.Context, m *storage.MCPServer, o OAuth) error {
	enc, err := SealOAuth(g.Box, o)
	if err != nil {
		return err
	}
	if err := g.Store.MCPServers().SetOAuth(context.WithoutCancel(ctx), m.ID, enc); err != nil {
		return err
	}
	m.OAuthEnc = enc
	return nil
}

// probe asks the server to start a session without office's OAuth token:
// a 401 says where to log in.
func (g *Gateway) probe(ctx context.Context, m storage.MCPServer) (OAuth, error) {
	headers, err := OpenMap(g.Box, m.HeadersEnc)
	if err != nil {
		return OAuth{}, errors.New("không mở được bí mật đã lưu: nhập lại header")
	}
	delete(headers, "Authorization")
	_, err = Check(ctx, g.client(), m.URL, headers)
	var se *StatusError
	if errors.As(err, &se) && se.Code == http.StatusUnauthorized {
		return Discover(ctx, g.client(), m.URL, se.WWWAuthenticate)
	}
	if err == nil {
		return OAuth{}, errors.New("MCP này không đòi đăng nhập OAuth")
	}
	return OAuth{}, err
}

// PasteBack reports whether a login started from origin comes back by paste:
// office opened over plain http on another host (Tailscale, LAN) gets a
// localhost callback, which most authorization servers accept but the
// person's browser cannot reach, so they paste the address it lands on.
func PasteBack(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "http" && !loopback(u.Hostname())
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CallbackURL is where the authorization server sends the person back for a
// login started from origin: origin itself, or localhost on the same port
// when PasteBack.
func CallbackURL(origin string) string {
	origin = strings.TrimRight(origin, "/")
	if PasteBack(origin) {
		u, _ := url.Parse(origin)
		host := "localhost"
		if p := u.Port(); p != "" {
			host += ":" + p
		}
		return "http://" + host + CallbackPath
	}
	return origin + CallbackPath
}

// StartLogin begins an OAuth login for m on behalf of user: it finds the
// authorization server and registers office as a client when needed, then
// returns the address to open in the browser. origin is the address the
// person opened office with; the callback goes to CallbackURL(origin).
func (g *Gateway) StartLogin(ctx context.Context, m storage.MCPServer, user, origin string) (string, error) {
	if m.Kind != "http" {
		return "", errors.New("chỉ MCP HTTP mới đăng nhập OAuth")
	}
	lk := g.logins.lock(m.ID) // two logins at once register one client
	lk.Lock()
	defer lk.Unlock()
	if fresh, err := g.Store.MCPServers().Get(ctx, m.ID); err == nil {
		m = fresh // what an earlier login found
	}
	o, err := OpenOAuth(g.Box, m.OAuthEnc)
	if err != nil {
		o = OAuth{}
	}
	if !o.Discovered() {
		found, err := g.probe(ctx, m)
		if err != nil {
			return "", err
		}
		if o.ClientManual { // keep the typed client
			found.ClientID, found.ClientSecret, found.ClientManual = o.ClientID, o.ClientSecret, true
		}
		o = found
	}
	redirect := CallbackURL(origin)
	if o.ClientManual {
		o.AuthMethod = manualAuthMethod(o)
	} else if o.ClientID == "" || !slices.Contains(o.RedirectURIs, redirect) {
		if err := Register(ctx, g.client(), &o, redirect); err != nil {
			_ = g.saveOAuth(ctx, &m, o) // keep what was discovered
			return "", err
		}
	}
	if err := g.saveOAuth(ctx, &m, o); err != nil {
		return "", err
	}
	verifier, challenge := pkce()
	state := randomToken(24)
	g.logins.put(state, pendingLogin{user: user, server: m.ID, verifier: verifier, redirect: redirect, exp: time.Now().Add(LoginTTL)})
	g.log().Info("mcp gateway: login started", "server", m.Name)
	return authURL(o, redirect, state, challenge), nil
}

// FinishLogin handles the callback: state must be a login user started in
// the last LoginTTL (each state works once). authErr is the "error" the
// authorization server sent back instead of a code.
func (g *Gateway) FinishLogin(ctx context.Context, user, state, code, authErr string) (storage.MCPServer, error) {
	p, ok := g.logins.take(state, user)
	if !ok {
		return storage.MCPServer{}, ErrBadState
	}
	lk := g.logins.lock(p.server) // what is stored stays as read until the tokens are saved
	lk.Lock()
	defer lk.Unlock()
	m, err := g.Store.MCPServers().Get(ctx, p.server)
	if err != nil {
		return m, err
	}
	if authErr != "" {
		msg := "đăng nhập không thành công (" + firstLine(authErr, 80) + ")"
		if authErr == "invalid_request" || strings.Contains(authErr, "redirect") {
			msg += redirectHint(p.redirect)
		}
		return m, errors.New(msg)
	}
	if code == "" {
		return m, errors.New("máy chủ đăng nhập không trả mã đăng nhập")
	}
	o, err := OpenOAuth(g.Box, m.OAuthEnc)
	if err != nil || !o.Discovered() {
		return m, errors.New("thông tin đăng nhập của MCP đã thay đổi: bấm Kết nối lại")
	}
	if err := exchange(ctx, g.client(), &o, code, p.verifier, p.redirect); err != nil {
		var te *TokenError
		if errors.As(err, &te) {
			return m, fmt.Errorf("không đổi được mã đăng nhập lấy token: %w", err)
		}
		return m, err
	}
	if err := g.saveOAuth(ctx, &m, o); err != nil {
		return m, err
	}
	g.log().Info("mcp gateway: logged in", "server", m.Name)
	return m, nil
}

// Logout forgets m's tokens (what was discovered and the client stay).
func (g *Gateway) Logout(ctx context.Context, m storage.MCPServer) (storage.MCPServer, error) {
	lk := g.logins.lock(m.ID)
	lk.Lock()
	defer lk.Unlock()
	if fresh, err := g.Store.MCPServers().Get(ctx, m.ID); err == nil {
		m = fresh
	}
	o, err := OpenOAuth(g.Box, m.OAuthEnc)
	if err != nil {
		o = OAuth{}
	}
	o.clearTokens()
	o.Expired = false
	if err := g.saveOAuth(ctx, &m, o); err != nil {
		return m, err
	}
	return g.setStatus(ctx, m, "needs_login", ErrNeedsLogin.Error())
}

// setStatus records a status office found on its own and tells open pages.
func (g *Gateway) setStatus(ctx context.Context, m storage.MCPServer, status, msg string) (storage.MCPServer, error) {
	now := time.Now().UTC()
	if err := g.Store.MCPServers().SetCheck(context.WithoutCancel(ctx), m.ID, status, msg, m.LastTools, now); err != nil {
		return m, err
	}
	m.LastCheckAt, m.LastCheckStatus, m.LastCheckError = &now, status, msg
	if g.OnStatus != nil {
		g.OnStatus(m)
	}
	return m, nil
}

// bearer is the OAuth access token to send to m ("" when m has no OAuth),
// refreshed when it is about to expire, or anyway when force (the server
// just refused it). A refused refresh ends the session: m goes to
// needs_login and ErrSessionExpired comes back.
func (g *Gateway) bearer(ctx context.Context, m *storage.MCPServer, force bool) (string, error) {
	if m.OAuthEnc == "" {
		return "", nil
	}
	lk := g.logins.lock(m.ID)
	lk.Lock()
	defer lk.Unlock()
	if fresh, err := g.Store.MCPServers().Get(ctx, m.ID); err == nil {
		*m = fresh // another call may have refreshed already
	}
	o, err := OpenOAuth(g.Box, m.OAuthEnc)
	if err != nil {
		return "", errors.New("không mở được token đã lưu: bấm Kết nối lại")
	}
	if !o.LoggedIn() {
		if !o.Discovered() {
			return "", nil
		}
		if o.Expired {
			return "", ErrSessionExpired
		}
		return "", ErrNeedsLogin
	}
	soon := !o.ExpiresAt.IsZero() && time.Until(o.ExpiresAt) < refreshBefore
	if !force && !soon && o.AccessToken != "" {
		return o.AccessToken, nil
	}
	if o.RefreshToken == "" {
		if !force && o.AccessToken != "" && (o.ExpiresAt.IsZero() || time.Now().Before(o.ExpiresAt)) {
			return o.AccessToken, nil // nothing to refresh with: use it while it lasts
		}
		return "", g.expire(ctx, m, o)
	}
	err = refresh(ctx, g.client(), &o)
	var te *TokenError
	if errors.As(err, &te) {
		return "", g.expire(ctx, m, o)
	}
	if err != nil {
		return "", fmt.Errorf("không làm mới được token: %w", err)
	}
	if err := g.saveOAuth(ctx, m, o); err != nil {
		return "", err
	}
	g.log().Info("mcp gateway: token refreshed", "server", m.Name)
	return o.AccessToken, nil
}

// expire ends m's session after a refused refresh.
func (g *Gateway) expire(ctx context.Context, m *storage.MCPServer, o OAuth) error {
	o.clearTokens()
	o.Expired = true
	if err := g.saveOAuth(ctx, m, o); err != nil {
		return err
	}
	g.log().Warn("mcp gateway: session expired", "server", m.Name)
	if fresh, err := g.setStatus(ctx, *m, "needs_login", ErrSessionExpired.Error()); err == nil {
		*m = fresh
	}
	return ErrSessionExpired
}
