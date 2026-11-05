package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-254, as office wires it: a run's caller is named by its token (the
// gateway's Alive asks the MCP server), and a token past its run gets a 401
// that says so instead of a bare one.
func TestGatewayRunTokenWiring(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	mcp := mcpserver.New(officetools.New(st, nil, nil), "test")
	now := time.Now()
	mcp.Now = func() time.Time { return now }
	gw := &mcpgateway.Gateway{Store: st, Identify: gatewayIdentify(st, mcp), Deny: mcp.Why, Alive: mcp.Live}
	srv := httptest.NewServer(gw)
	defer srv.Close()

	tok, revoke := mcp.Grant(officetools.Scope{ProjectID: "p", Level: "read"}, mcpserver.MaxRun)
	req := httptest.NewRequest("POST", "/mcp/s/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	now = now.Add(45 * time.Minute) // past the old 30-minute token
	c, ok := gw.Identify(req)
	if !ok || c.Run != tok || !gw.Alive(c.Run) {
		t.Fatalf("caller 45m in = %+v %v", c, ok)
	}
	revoke()
	if gw.Alive(c.Run) {
		t.Fatal("run still alive after revoke")
	}
	r, _ := http.NewRequest("POST", srv.URL, strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 401 || !strings.Contains(string(body), "thu hồi") {
		t.Fatalf("revoked = %d %q", resp.StatusCode, body)
	}
}
