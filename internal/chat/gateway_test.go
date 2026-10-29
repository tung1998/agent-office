package chat

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/storage"
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
	if _, ok := cfg.MCPServers["other_office"]; ok {
		t.Fatal("other_office given to a run that reaches no other office")
	}
	if len(cfg.MCPServers) != 2 || c7.Type != "http" || c7.URL != "http://127.0.0.1:1/mcp/s/context7" || c7.Headers["Authorization"] != "Bearer tok" ||
		c7.Headers[ClientHeader] != "claude" {
		t.Fatalf("config = %s", raw)
	}

	o.Other = true // reaches another office (ADR-169): its server, same token
	p2, _ := writeMCPConfig(o)
	defer os.Remove(p2)
	raw, _ = os.ReadFile(p2)
	cfg.MCPServers = nil
	json.Unmarshal(raw, &cfg)
	if oo := cfg.MCPServers["other_office"]; oo.URL != "http://127.0.0.1:1/mcp/other_office" || oo.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("other_office config = %s", raw)
	}
	if !strings.Contains(officeAllow(o), "mcp__other_office__*") {
		t.Fatal("other_office not allowed")
	}
	if a := strings.Join(codexMCPArgs(RunRequest{Office: o}), " "); !strings.Contains(a, `mcp_servers.other_office.url="http://127.0.0.1:1/mcp/other_office"`) {
		t.Fatalf("codex: %s", a)
	}
	if g := geminiMCP(RunRequest{Office: o}); g["other_office"] == nil {
		t.Fatal("gemini without other_office")
	}
	if s := guardSettings(false, []string{"other_office"}, false, ""); !strings.Contains(s, "--mcp other_office") {
		t.Fatal("guard without other_office")
	}

	old := GuardCommand
	defer func() { GuardCommand = old }()
	GuardCommand = "'/x/office' hook guard"
	a := claudeRunner{}.args(RunRequest{Write: true, Office: o}, false)
	if s := a[slices.Index(a, "--settings")+1]; !strings.Contains(s, "--mcp context7") || !strings.Contains(s, "--mcp other_office") {
		t.Fatalf("guard without the gateway's server or other_office: %s", s)
	}
	o.Other = false
	a = claudeRunner{}.args(RunRequest{Write: true, Office: o}, false)
	if s := a[slices.Index(a, "--settings")+1]; strings.Contains(s, "other_office") {
		t.Fatalf("guard lets other_office through for a run without it: %s", s)
	}
}

func TestResumedCLIReceivesGatewayBrief(t *testing.T) {
	dir := t.TempDir()
	stdinFile := filepath.Join(dir, "stdin")
	t.Setenv("TOOLBOX_TEST_STDIN", stdinFile)
	t.Setenv("GEMINI_CLI_HOME", dir)
	bin := filepath.Join(dir, "cli")
	if err := fakebin.Write(bin, []byte(`#!/bin/sh
cat > "$TOOLBOX_TEST_STDIN"
echo '{"type":"item.completed","item":{"type":"agent_message","text":"ok"}}'
echo '{"type":"message","role":"assistant","content":"ok","delta":true}'
echo '{"type":"result","status":"success"}'
`)); err != nil {
		t.Fatal(err)
	}
	box := officetools.New(nil, nil, nil)
	box.SetMCPTools(discoveryTools{})
	for _, runner := range []Runner{codexRunner{}, geminiRunner{}} {
		req := RunRequest{Bin: bin, WorkDir: dir, Prompt: "NEW_MESSAGE", SessionID: "saved", Office: &OfficeAccess{Tools: box, Gateway: []string{"figma"}, GatewayOthers: []string{"restricted"}}}
		if _, err := runner.Run(context.Background(), req, func(Event) {}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(stdinFile)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"figma", "restricted", "find_tool", "NEW_MESSAGE"} {
			if !strings.Contains(string(data), text) {
				t.Fatalf("%T resumed without %q: %s", runner, text, data)
			}
		}
	}
}

func TestGatewayBriefAndFallbackScope(t *testing.T) {
	box := officetools.New(nil, nil, nil)
	box.SetMCPTools(discoveryTools{})
	server := mcpserver.New(box, "test")
	e := &Engine{office: box, mcp: server}
	o := &OfficeAccess{Tools: box, Scope: officetools.Scope{ClientKind: "api"}, Gateway: []string{"figma"}, GatewayOthers: []string{"private"}}
	brief := gatewayBrief(o)
	for _, word := range []string{"figma", "private", "find_tool", "call_tool"} {
		if !strings.Contains(brief, word) {
			t.Fatalf("brief lacks %s: %s", word, brief)
		}
	}
	for _, kind := range []storage.ProviderKind{storage.ProviderCodexCLI, storage.ProviderGeminiCLI, storage.ProviderAnthropic, storage.ProviderClaudeCLI} {
		next, revoke := e.officeForClient(context.Background(), o, kind)
		if next == nil || next.Scope.ClientKind != clientKind(kind) {
			t.Fatalf("fallback lost runner: %+v", next)
		}
		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+next.Token)
		sc, ok := server.ScopeOf(req)
		if !ok || sc.ClientKind != clientKind(kind) {
			t.Fatalf("token not bound to runner: %+v %t", sc, ok)
		}
		revoke()
		o = next
	}
	if got := gatewayBrief(&OfficeAccess{Tools: box, Scope: officetools.Scope{Consult: true}, Gateway: []string{"figma"}}); got != "" {
		t.Fatalf("expert got gateway brief: %s", got)
	}
}

type discoveryTools struct{}

func (discoveryTools) Tools(officetools.Scope) []officetools.Tool {
	return []officetools.Tool{{Name: "find_tool"}, {Name: "call_tool"}}
}
func (discoveryTools) Call(context.Context, officetools.Scope, string, json.RawMessage) (string, bool) {
	return "", false
}

func TestCLIUsesDiscoveryWithoutConnectingEveryGateway(t *testing.T) {
	box := officetools.New(nil, nil, nil)
	box.SetMCPTools(discoveryTools{})
	o := &OfficeAccess{MCPURL: "http://127.0.0.1:1/mcp", Gateway: []string{"figma", "context7"}, Tools: box}
	req := RunRequest{Office: o}
	args := strings.Join(codexMCPArgs(req), " ")
	if !strings.Contains(args, "mcp_servers.office.url") || strings.Contains(args, "/s/") {
		t.Fatalf("Codex still connects to external servers: %s", args)
	}
	if servers := geminiMCP(req); len(servers) != 1 || servers["office"] == nil {
		t.Fatalf("Gemini still connects to external servers: %+v", servers)
	}
	req.NoTools = true
	if _, failed := dispatchTool(context.Background(), req, Workspace{}, "call_tool", json.RawMessage(`{}`)); !failed {
		t.Fatal("NoTools run called the office gateway")
	}
	// Claude retains its native server connections and discovery behavior.
	path, err := writeMCPConfig(o)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "/s/figma") || !strings.Contains(string(raw), "/s/context7") {
		t.Fatalf("Claude native MCP connections changed: %s", raw)
	}
}

// Codex gets office's server and the gateway's through -c, never the token
// in argv (ADR-093), their tools approved: `codex exec` asks nobody and
// refuses a write tool (propose_action) Codex would ask about.
func TestCodexMCPArgs(t *testing.T) {
	o := &OfficeAccess{MCPURL: "http://127.0.0.1:1/mcp", Token: "secret-tok", Gateway: []string{"context7"}}
	args := strings.Join(codexMCPArgs(RunRequest{Office: o}), " ")
	for _, want := range []string{`mcp_servers.office.url="http://127.0.0.1:1/mcp"`, `mcp_servers.context7.url="http://127.0.0.1:1/mcp/s/context7"`,
		`mcp_servers.context7.bearer_token_env_var="OFFICE_MCP_TOKEN"`, `mcp_servers.context7.http_headers={"X-Office-Client"="codex"}`,
		`mcp_servers.office.default_tools_approval_mode="approve"`, `mcp_servers.context7.default_tools_approval_mode="approve"`} {
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
