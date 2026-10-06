package chat

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// A run gets the gateway's servers next to office's own, each at
// /mcp/s/<name> with the run's token (ADR-091); the guard lets their tools through.
func TestMCPConfigGateway(t *testing.T) {
	o := &OfficeAccess{MCPURL: "http://127.0.0.1:1/mcp", Token: "tok", Gateway: []string{"context7"}}
	p, err := writeMCPConfig(o)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	raw, _ := os.ReadFile(p)
	var cfg struct {
		MCPServers map[string]struct {
			Type, URL string
			Headers   map[string]string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	c7 := cfg.MCPServers["context7"]
	if len(cfg.MCPServers) != 2 || c7.Type != "http" || c7.URL != "http://127.0.0.1:1/mcp/s/context7" || c7.Headers["Authorization"] != "Bearer tok" ||
		c7.Headers[ClientHeader] != "claude" {
		t.Fatalf("config = %s", raw)
	}

	old := GuardCommand
	defer func() { GuardCommand = old }()
	GuardCommand = "'/x/office' hook guard"
	a := claudeRunner{}.args(RunRequest{Write: true, Office: o}, false)
	if s := a[slices.Index(a, "--settings")+1]; !strings.Contains(s, "--mcp context7") {
		t.Fatalf("guard without the gateway's server: %s", s)
	}
}

// Codex gets office's server and the gateway's through -c, never the token
// in argv (ADR-093).
func TestCodexMCPArgs(t *testing.T) {
	o := &OfficeAccess{MCPURL: "http://127.0.0.1:1/mcp", Token: "secret-tok", Gateway: []string{"context7"}}
	args := strings.Join(codexMCPArgs(RunRequest{Office: o}), " ")
	for _, want := range []string{`mcp_servers.office.url="http://127.0.0.1:1/mcp"`, `mcp_servers.context7.url="http://127.0.0.1:1/mcp/s/context7"`,
		`mcp_servers.context7.bearer_token_env_var="OFFICE_MCP_TOKEN"`, `mcp_servers.context7.http_headers={"X-Office-Client"="codex"}`} {
		if !strings.Contains(args, want) {
			t.Errorf("codex args lack %s: %s", want, args)
		}
	}
	if strings.Contains(args, "secret-tok") {
		t.Fatal("token in argv")
	}
	if codexMCPArgs(RunRequest{Office: o, NoTools: true}) != nil {
		t.Fatal("NoTools run got MCP")
	}
}

type fakeGW struct{ called string }

func (f *fakeGW) List(context.Context) []GatewayTool {
	return []GatewayTool{{Name: "mcp__c7__resolve", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}}
}
func (f *fakeGW) Call(_ context.Context, name string, _ json.RawMessage) (string, bool) {
	f.called = name
	return "ok", false
}

// An API run reaches gateway tools through dispatchTool.
func TestDispatchGatewayTool(t *testing.T) {
	gw := &fakeGW{}
	req := RunRequest{Office: &OfficeAccess{GatewayTools: gw}}
	if len(gatewayTools(context.Background(), req)) != 1 {
		t.Fatal("gateway tools not listed")
	}
	if out, isErr := dispatchTool(context.Background(), req, Workspace{}, "mcp__c7__resolve", nil); out != "ok" || isErr || gw.called != "mcp__c7__resolve" {
		t.Fatalf("dispatch = %q %v %q", out, isErr, gw.called)
	}
	req.NoTools = true
	if gatewayTools(context.Background(), req) != nil {
		t.Fatal("NoTools run got gateway tools")
	}
}
