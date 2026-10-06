package mcpgateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// OAuth is what office keeps about a server's OAuth login (ADR-092), sealed
// as one JSON object into mcp_servers.oauth_enc. Never sent out of office.
type OAuth struct {
	Resource             string   `json:"resource,omitempty"` // the MCP server, as the token's audience (RFC 8707)
	Issuer               string   `json:"issuer,omitempty"`
	AuthEndpoint         string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint        string   `json:"token_endpoint,omitempty"`
	RegistrationEndpoint string   `json:"registration_endpoint,omitempty"`
	AuthMethods          []string `json:"auth_methods,omitempty"` // token_endpoint_auth_methods_supported
	Scope                string   `json:"scope,omitempty"`
	Guessed              bool     `json:"guessed,omitempty"` // no metadata: the default endpoints

	ClientID     string   `json:"client_id,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty"`
	AuthMethod   string   `json:"auth_method,omitempty"`   // none | client_secret_post | client_secret_basic
	ClientManual bool     `json:"client_manual,omitempty"` // typed on the form, not registered by office
	RedirectURIs []string `json:"redirect_uris,omitempty"` // what the registered client accepts

	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	Expired      bool      `json:"expired,omitempty"` // a refresh failed: the person logs in again
}

// Discovered reports whether office knows where to log in.
func (o OAuth) Discovered() bool { return o.AuthEndpoint != "" && o.TokenEndpoint != "" }

// LoggedIn reports whether office holds a token.
func (o OAuth) LoggedIn() bool { return o.AccessToken != "" || o.RefreshToken != "" }

// clearTokens forgets the login, keeping what was discovered and the client.
func (o *OAuth) clearTokens() {
	o.AccessToken, o.RefreshToken, o.ExpiresAt = "", "", time.Time{}
}

// keep carries over from old (what was stored) the client and the login a
// fresh discovery does not know: a typed client always, a registered one and
// its tokens while the token endpoint is the same.
func (o *OAuth) keep(old OAuth) {
	switch {
	case old.ClientManual:
		o.ClientID, o.ClientSecret, o.ClientManual = old.ClientID, old.ClientSecret, true
	case old.ClientID != "" && old.TokenEndpoint == o.TokenEndpoint:
		o.ClientID, o.ClientSecret, o.AuthMethod, o.RedirectURIs = old.ClientID, old.ClientSecret, old.AuthMethod, old.RedirectURIs
	default:
		return
	}
	if old.LoggedIn() && old.TokenEndpoint == o.TokenEndpoint {
		o.AccessToken, o.RefreshToken, o.ExpiresAt, o.Expired = old.AccessToken, old.RefreshToken, old.ExpiresAt, old.Expired
	}
}

// SealOAuth encrypts o ("" when there is nothing to keep).
func SealOAuth(b Sealer, o OAuth) (string, error) {
	if o.Discovered() || o.ClientID != "" || o.LoggedIn() {
		raw, err := json.Marshal(o)
		if err != nil {
			return "", err
		}
		return b.Seal(string(raw))
	}
	return "", nil
}

// OpenOAuth decrypts what SealOAuth made.
func OpenOAuth(b Sealer, enc string) (OAuth, error) {
	var o OAuth
	if enc == "" {
		return o, nil
	}
	raw, err := b.Open(enc)
	if err != nil {
		return o, err
	}
	return o, json.Unmarshal([]byte(raw), &o)
}

const oauthTimeout = 15 * time.Second

var authParamRe = regexp.MustCompile(`([a-zA-Z_]+)\s*=\s*(?:"([^"]*)"|([^\s,]+))`)

// authParam reads one parameter of a WWW-Authenticate header.
func authParam(h, name string) string {
	for _, m := range authParamRe.FindAllStringSubmatch(h, -1) {
		if strings.EqualFold(m[1], name) {
			return m[2] + m[3]
		}
	}
	return ""
}

// resourceOf is the MCP server's canonical URI: no query (it may carry a
// key) and no fragment.
func resourceOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

// wellKnown builds the RFC 8414/9728 address: the well-known segment goes
// between the host and the path.
func wellKnown(base, name string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	p := strings.TrimRight(u.Path, "/")
	u.Path, u.RawQuery, u.Fragment = "/.well-known/"+name+p, "", ""
	return u.String()
}

func getJSON(ctx context.Context, hc *http.Client, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Mcp-Protocol-Version", protocolVersion)
	resp, err := hc.Do(req)
	if err != nil {
		return connError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// Discover finds where to log in to an MCP server that answered 401 (MCP
// authorization 2025-06-18): the resource metadata its WWW-Authenticate
// names, else the one at /.well-known/oauth-protected-resource; then the
// authorization server's metadata (RFC 8414, else OpenID). A server without
// resource metadata is its own authorization server (MCP 2025-03-26).
func Discover(ctx context.Context, hc *http.Client, mcpURL, wwwAuth string) (OAuth, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, oauthTimeout)
	defer cancel()
	o := OAuth{Resource: resourceOf(mcpURL), Scope: authParam(wwwAuth, "scope")}
	var prm struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	candidates := []string{}
	if rm := authParam(wwwAuth, "resource_metadata"); rm != "" {
		candidates = append(candidates, rm)
	}
	candidates = append(candidates, wellKnown(mcpURL, "oauth-protected-resource"))
	if u, err := url.Parse(mcpURL); err == nil && strings.Trim(u.Path, "/") != "" {
		candidates = append(candidates, wellKnown(u.Scheme+"://"+u.Host, "oauth-protected-resource"))
	}
	issuer := ""
	for _, c := range candidates {
		if c != "" && getJSON(ctx, hc, c, &prm) == nil && len(prm.AuthorizationServers) > 0 {
			issuer = prm.AuthorizationServers[0]
			break
		}
	}
	if issuer == "" {
		u, err := url.Parse(mcpURL)
		if err != nil {
			return o, errors.New("URL không hợp lệ")
		}
		issuer = u.Scheme + "://" + u.Host
	}
	if prm.Resource != "" {
		o.Resource = prm.Resource
	}
	if o.Scope == "" {
		o.Scope = strings.Join(prm.ScopesSupported, " ")
	}
	var as struct {
		Issuer                 string   `json:"issuer"`
		AuthorizationEndpoint  string   `json:"authorization_endpoint"`
		TokenEndpoint          string   `json:"token_endpoint"`
		RegistrationEndpoint   string   `json:"registration_endpoint"`
		CodeChallengeMethods   []string `json:"code_challenge_methods_supported"`
		TokenEndpointAuthMetho []string `json:"token_endpoint_auth_methods_supported"`
	}
	asURLs := []string{wellKnown(issuer, "oauth-authorization-server"), wellKnown(issuer, "openid-configuration")}
	if u, err := url.Parse(issuer); err == nil && strings.Trim(u.Path, "/") != "" {
		asURLs = append(asURLs, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration")
	}
	found := false
	for _, a := range asURLs {
		if a != "" && getJSON(ctx, hc, a, &as) == nil && as.AuthorizationEndpoint != "" && as.TokenEndpoint != "" {
			found = true
			break
		}
	}
	if !found {
		// no metadata: the default endpoints of MCP 2025-03-26, at the
		// authorization server's origin, if its /authorize is there at all (a
		// server that only takes an API key has none)
		u, err := url.Parse(issuer)
		base := ""
		if err == nil && u.Host != "" {
			base = u.Scheme + "://" + u.Host
		}
		if base == "" || !exists(ctx, hc, base+"/authorize") {
			return o, errors.New("không tìm thấy máy chủ đăng nhập (OAuth) của MCP này")
		}
		as.AuthorizationEndpoint, as.TokenEndpoint, as.RegistrationEndpoint = base+"/authorize", base+"/token", base+"/register"
		o.Guessed = true
	}
	if len(as.CodeChallengeMethods) > 0 && !slices.Contains(as.CodeChallengeMethods, "S256") {
		return o, errors.New("máy chủ đăng nhập không hỗ trợ PKCE S256, office không đăng nhập được")
	}
	o.Issuer = cmpStr(as.Issuer, issuer)
	o.AuthEndpoint, o.TokenEndpoint, o.RegistrationEndpoint = as.AuthorizationEndpoint, as.TokenEndpoint, as.RegistrationEndpoint
	o.AuthMethods = as.TokenEndpointAuthMetho
	return o, nil
}

// exists reports whether rawURL looks like a login page: a bare GET gets a
// form, a redirect or a 400 (not a 401/404 like any other path).
func exists(ctx context.Context, hc *http.Client, rawURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	c := *hc
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400 || resp.StatusCode == http.StatusBadRequest
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ErrRedirectRejected: the authorization server refused office's callback
// address (often because it wants https or localhost).
var ErrRedirectRejected = errors.New("máy chủ đăng nhập từ chối địa chỉ callback của office")

// redirectHint tells what to do when the callback address is refused.
func redirectHint(redirect string) string {
	port := "8787"
	if u, err := url.Parse(redirect); err == nil {
		if u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" {
			return ""
		}
		port = cmpStr(u.Port(), port)
	}
	return fmt.Sprintf(": mở office qua http://localhost:%s rồi bấm Kết nối", port)
}

// ErrNeedsClient: the authorization server registers no client on its own,
// the person types one (client_id, maybe a secret) on the form.
var ErrNeedsClient = errors.New("máy chủ đăng nhập không cho đăng ký tự động: nhập client_id trên form")

// Register registers office as a client (RFC 7591) for redirect.
func Register(ctx context.Context, hc *http.Client, o *OAuth, redirect string) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	if o.RegistrationEndpoint == "" {
		return ErrNeedsClient
	}
	ctx, cancel := context.WithTimeout(ctx, oauthTimeout)
	defer cancel()
	in := map[string]any{
		"client_name":                "Agent Office",
		"redirect_uris":              []string{redirect},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	if o.Scope != "" {
		in["scope"] = o.Scope
	}
	raw, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.RegistrationEndpoint, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return connError(ctx, err)
	}
	defer resp.Body.Close()
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		AuthMethod   string `json:"token_endpoint_auth_method"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode >= 300 || out.ClientID == "" {
		if out.Error == "invalid_redirect_uri" || strings.Contains(strings.ToLower(out.Description), "redirect") {
			return fmt.Errorf("%w (%s)%s", ErrRedirectRejected, firstLine(cmpStr(out.Description, out.Error), 160), redirectHint(redirect))
		}
		return fmt.Errorf("đăng ký client tự động bị từ chối (HTTP %d %s)", resp.StatusCode, firstLine(cmpStr(out.Description, out.Error), 160))
	}
	o.ClientID, o.ClientSecret, o.ClientManual = out.ClientID, out.ClientSecret, false
	o.AuthMethod = out.AuthMethod
	if o.AuthMethod == "" {
		o.AuthMethod = "none"
		if o.ClientSecret != "" {
			o.AuthMethod = "client_secret_basic"
		}
	}
	o.RedirectURIs = []string{redirect}
	return nil
}

// manualAuthMethod picks how a typed client authenticates at the token
// endpoint.
func manualAuthMethod(o OAuth) string {
	switch {
	case o.ClientSecret == "":
		return "none"
	case len(o.AuthMethods) == 0 || slices.Contains(o.AuthMethods, "client_secret_basic"):
		return "client_secret_basic"
	default:
		return "client_secret_post"
	}
}

// pkce is one login's verifier and its S256 challenge (RFC 7636).
func pkce() (verifier, challenge string) {
	verifier = randomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// authURL is where the person logs in.
func authURL(o OAuth, redirect, state, challenge string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {o.ClientID},
		"redirect_uri":          {redirect},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {o.Resource},
	}
	if o.Scope != "" {
		q.Set("scope", o.Scope)
	}
	sep := "?"
	if strings.Contains(o.AuthEndpoint, "?") {
		sep = "&"
	}
	return o.AuthEndpoint + sep + q.Encode()
}

// TokenError is the authorization server refusing a grant (the code or the
// refresh token is no good): the person has to log in again.
type TokenError struct {
	Status int
	Code   string
}

func (e *TokenError) Error() string {
	return fmt.Sprintf("máy chủ đăng nhập từ chối (HTTP %d %s)", e.Status, e.Code)
}

// token calls the token endpoint and keeps what it returns in o.
func token(ctx context.Context, hc *http.Client, o *OAuth, form url.Values) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, oauthTimeout)
	defer cancel()
	form.Set("client_id", o.ClientID)
	if o.Resource != "" {
		form.Set("resource", o.Resource)
	}
	if o.AuthMethod == "client_secret_post" {
		form.Set("client_secret", o.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if o.AuthMethod == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(o.ClientID), url.QueryEscape(o.ClientSecret))
	}
	resp, err := hc.Do(req)
	if err != nil {
		return connError(ctx, err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return &TokenError{Status: resp.StatusCode, Code: out.Error}
	}
	if resp.StatusCode >= 300 || out.AccessToken == "" {
		return fmt.Errorf("máy chủ đăng nhập trả lỗi HTTP %d", resp.StatusCode)
	}
	o.AccessToken = out.AccessToken
	if out.RefreshToken != "" { // a refresh may keep the old one
		o.RefreshToken = out.RefreshToken
	}
	o.ExpiresAt = time.Time{}
	if out.ExpiresIn > 0 {
		o.ExpiresAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second).UTC()
	}
	o.Expired = false
	return nil
}

// exchange trades the login's code for tokens.
func exchange(ctx context.Context, hc *http.Client, o *OAuth, code, verifier, redirect string) error {
	return token(ctx, hc, o, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}})
}

// refresh gets a new access token with the refresh token.
func refresh(ctx context.Context, hc *http.Client, o *OAuth) error {
	return token(ctx, hc, o, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {o.RefreshToken}})
}
