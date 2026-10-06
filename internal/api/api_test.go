package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/events"
	"github.com/tung1998/agent-office/internal/memory"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/api"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	officesetup "github.com/tung1998/agent-office/internal/setup"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/transfer"
	"github.com/tung1998/agent-office/internal/trigger"
	"github.com/tung1998/agent-office/internal/usage"
)

type env struct {
	srv   *httptest.Server
	auth  *auth.Service
	st    storage.Store
	acts  *actions.Service
	provs *provider.Service
	chat  *chat.Engine
}

// idleExec is an automation executor that does nothing (API tests only queue).
type idleExec struct{}

func (idleExec) RunChat(context.Context, string, string, string, string, string) (string, string, error) {
	return "", "", nil
}
func (idleExec) RunTask(context.Context, string, string, string, string) (string, error) {
	return "", nil
}
func (idleExec) RunQueuedTask(context.Context, string, string) (string, error) { return "", nil }

func setup(t *testing.T) *env { return setupWith(t, nil) }

// cliPath, when set before setupWith, enables CLI tools with that PATH.
var cliPath string

func setupWith(t *testing.T, proxies []netip.Prefix) *env {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(st, auth.Options{Hasher: auth.FastHasherForTests()})
	box, err := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	org := orgmodel.NewService(st)
	if _, err := org.SeedBuiltins(context.Background()); err != nil {
		t.Fatal(err)
	}
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	chatEng := chat.NewEngine(st, provs, u)
	acts := actions.New(st, nil)
	bus := events.New(20 * time.Millisecond)
	st.OnWrite(bus.Wrote)
	st.OnChat(bus.Chat)
	mem := memory.New(st, func(_ context.Context, _, _ string, items []storage.Memory) ([]string, error) {
		return []string{fmt.Sprintf("gộp %d ghi nhớ", len(items))}, nil
	})
	acts.SetMemory(mem)
	office := officetools.New(st, nil, acts)
	office.SetOffice(func(ctx context.Context) string { return assistant.ID(ctx, st) })
	mcp := mcpserver.New(office, "test")
	h := api.New(api.Config{Store: st, Auth: svc, AllowedOrigins: []string{"http://localhost:3000"}, TrustedProxies: proxies,
		Providers: provs, Org: org, Setup: officesetup.New(st, provs, org), Transfer: transfer.New(st, provs, org), Usage: u, CLITools: cliManager(), Chat: chatEng,
		Trigger: trigger.New(st, idleExec{}), Actions: acts, Office: office, MCP: mcp, Memory: mem, Events: bus, Automation: testAutomation(t, st),
		Gateway: &mcpgateway.Gateway{Store: st, Box: box, Auth: mcp.Authorized, Identify: testIdentify(mcp)}})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ctx := context.Background()
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "admin@x.io", Name: "Admin", Role: storage.RoleAdmin, Password: "admin-password"}, "system"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, auth.NewUser{Email: "member@x.io", Role: storage.RoleMember, Password: "member-password"}, "system"); err != nil {
		t.Fatal(err)
	}
	return &env{srv: srv, auth: svc, st: st, acts: acts, provs: provs, chat: chatEng}
}

// testIdentify says who calls the gateway from the token's scope, the way
// office's own does (cmd/office gatewayCaller), without agent lookups.
func testIdentify(mcp *mcpserver.Server) func(r *http.Request) (mcpgateway.Caller, bool) {
	return func(r *http.Request) (mcpgateway.Caller, bool) {
		sc, ok := mcp.ScopeOf(r)
		if !ok {
			return mcpgateway.Caller{}, false
		}
		c := mcpgateway.Caller{Kind: "claude", Agent: sc.Agent, ProjectID: sc.ProjectID, Scope: sc,
			CanWrite: sc.Access.Can(perm.CapMCPWrite), CanPropose: sc.Access.Can(perm.CapPropose)}
		if strings.HasPrefix(sc.RunRef, "cli-") {
			c.Kind = "person"
		}
		return c, true
	}
}

func (e *env) client(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func do(t *testing.T, c *http.Client, method, url string, body any, hdr map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func login(t *testing.T, e *env, c *http.Client, email, pw string) {
	t.Helper()
	resp, body := do(t, c, "POST", e.srv.URL+"/api/auth/login", map[string]string{"email": email, "password": pw}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login %s = %d %v", email, resp.StatusCode, body)
	}
}

func TestHealthz(t *testing.T) {
	e := setup(t)
	for _, p := range []string{"/healthz", "/api/health"} {
		resp, _ := do(t, e.client(t), "GET", e.srv.URL+p, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("%s = %d", p, resp.StatusCode)
		}
	}
}

func TestLoginFlow(t *testing.T) {
	e := setup(t)
	c := e.client(t)

	resp, _ := do(t, c, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("me before login = %d", resp.StatusCode)
	}
	resp, body := do(t, c, "POST", e.srv.URL+"/api/auth/login", map[string]string{"email": "admin@x.io", "password": "nope-nope-nope"}, nil)
	if resp.StatusCode != 401 || body["error"] == nil {
		t.Fatalf("bad login = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, c, "POST", e.srv.URL+"/api/auth/login", map[string]string{"email": "admin@x.io", "password": "admin-password"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %v", resp.StatusCode, body)
	}
	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == api.SessionCookie {
			cookie = ck
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("session cookie = %+v", cookie)
	}
	user := body["user"].(map[string]any)
	if user["email"] != "admin@x.io" || user["role"] != "admin" || user["password_hash"] != nil {
		t.Fatalf("user payload = %v", user)
	}
	resp, body = do(t, c, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	if resp.StatusCode != 200 || body["user"].(map[string]any)["email"] != "admin@x.io" {
		t.Fatalf("me = %d %v", resp.StatusCode, body)
	}
	if b, ok := body["build"].(map[string]any); !ok || b["version"] == nil {
		t.Fatalf("me build = %v", body["build"])
	}
	resp, _ = do(t, c, "POST", e.srv.URL+"/api/auth/logout", map[string]string{}, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	resp, _ = do(t, c, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("me after logout = %d", resp.StatusCode)
	}
}

func TestCSRFGuards(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	// form-encoded POST (what a cross-site <form> can send) is rejected
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/auth/login", bytes.NewBufferString("email=admin@x.io&password=admin-password"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post = %d", resp.StatusCode)
	}
	// foreign Origin is rejected
	resp, _ = do(t, c, "POST", e.srv.URL+"/api/auth/login", map[string]string{"email": "admin@x.io", "password": "admin-password"}, map[string]string{"Origin": "https://evil.example"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin = %d", resp.StatusCode)
	}
	// allowed Origin passes
	resp, _ = do(t, c, "POST", e.srv.URL+"/api/auth/login", map[string]string{"email": "admin@x.io", "password": "admin-password"}, map[string]string{"Origin": "http://localhost:3000"})
	if resp.StatusCode != 200 {
		t.Fatalf("allowed origin = %d", resp.StatusCode)
	}
}

func TestAdminUserManagement(t *testing.T) {
	e := setup(t)
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	resp, _ := do(t, member, "GET", e.srv.URL+"/api/users", nil, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("member list users = %d", resp.StatusCode)
	}

	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	resp, body := do(t, admin, "GET", e.srv.URL+"/api/users", nil, nil)
	if resp.StatusCode != 200 || len(body["users"].([]any)) != 2 {
		t.Fatalf("admin list users = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/users", map[string]string{"email": "new@x.io", "name": "New", "role": "member", "password": "new-user-password"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create user = %d %v", resp.StatusCode, body)
	}
	newID := body["user"].(map[string]any)["id"].(string)
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/users", map[string]string{"email": "new@x.io", "role": "member", "password": "new-user-password"}, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("duplicate user = %d %v", resp.StatusCode, body)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/users", map[string]string{"email": "weak@x.io", "role": "member", "password": "short"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("weak password = %d %v", resp.StatusCode, body)
	}

	nc := e.client(t)
	login(t, e, nc, "new@x.io", "new-user-password")
	resp, _ = do(t, admin, "PATCH", e.srv.URL+"/api/users/"+newID, map[string]any{"disabled": true}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("disable = %d", resp.StatusCode)
	}
	resp, _ = do(t, nc, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("disabled user session = %d", resp.StatusCode)
	}
	// admin cannot disable themself (would lock out the office)
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	selfID := body["user"].(map[string]any)["id"].(string)
	resp, _ = do(t, admin, "PATCH", e.srv.URL+"/api/users/"+selfID, map[string]any{"disabled": true}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("self disable = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "GET", e.srv.URL+"/api/audit?limit=50", nil, nil)
	if resp.StatusCode != 200 || len(body["entries"].([]any)) == 0 {
		t.Fatalf("audit = %d %v", resp.StatusCode, body)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	e := setup(t)
	c := e.client(t)
	login(t, e, c, "member@x.io", "member-password")
	resp, _ := do(t, c, "POST", e.srv.URL+"/api/auth/password", map[string]string{"old_password": "wrong-wrong-1", "new_password": "brand-new-pass"}, nil)
	if resp.StatusCode != 400 {
		t.Fatalf("wrong old = %d", resp.StatusCode)
	}
	resp, _ = do(t, c, "POST", e.srv.URL+"/api/auth/password", map[string]string{"old_password": "member-password", "new_password": "brand-new-pass"}, nil)
	if resp.StatusCode != 204 {
		t.Fatalf("change = %d", resp.StatusCode)
	}
	resp, _ = do(t, c, "GET", e.srv.URL+"/api/auth/me", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("session after change = %d", resp.StatusCode)
	}
}

func TestForwardedHostFromTrustedProxy(t *testing.T) {
	body := map[string]string{"email": "admin@x.io", "password": "admin-password"}
	hdr := map[string]string{"Origin": "https://office.example.com", "X-Forwarded-Host": "office.example.com"}

	untrusted := setup(t)
	if resp, _ := do(t, untrusted.client(t), "POST", untrusted.srv.URL+"/api/auth/login", body, hdr); resp.StatusCode != 403 {
		t.Fatalf("untrusted peer forwarded host = %d, want 403", resp.StatusCode)
	}
	trusted := setupWith(t, []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")})
	if resp, _ := do(t, trusted.client(t), "POST", trusted.srv.URL+"/api/auth/login", body, hdr); resp.StatusCode != 200 {
		t.Fatalf("trusted peer forwarded host = %d, want 200", resp.StatusCode)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func cliManager() *clitools.Manager {
	if cliPath == "" {
		return nil
	}
	return clitools.NewManagerWithPath(cliPath)
}

// testAutomation manages skills in a temp home, over the store's projects.
func testAutomation(t *testing.T, st storage.Store) *automation.Service {
	home, office := t.TempDir(), t.TempDir()
	trash := filepath.Join(office, "trash")
	return &automation.Service{Home: home, Library: automation.Library{Dir: filepath.Join(office, "library"), Trash: trash},
		Installer: automation.Installer{Home: home, Trash: trash},
		Projects: func(ctx context.Context) map[string]string {
			out := map[string]string{}
			if list, err := st.Repos().List(ctx); err == nil {
				for _, p := range list {
					out[p.Path] = p.ID
				}
			}
			return out
		}}
}
