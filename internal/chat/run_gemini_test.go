package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/fakebin"
)

func TestGeminiRunnerStreams(t *testing.T) {
	tmp := t.TempDir()
	// the person's ~/.gemini: their settings and a sign-in to link through
	real := filepath.Join(tmp, "home")
	os.MkdirAll(filepath.Join(real, ".gemini"), 0o700)
	os.WriteFile(filepath.Join(real, ".gemini", "settings.json"), []byte(`{"security":{"auth":{"selectedType":"oauth-personal"}},"mcpServers":{"mine":{"command":"x"}}}`), 0o600)
	os.WriteFile(filepath.Join(real, ".gemini", "oauth_creds.json"), []byte(`{}`), 0o600)
	t.Setenv("GEMINI_CLI_HOME", real)

	bin, seen := filepath.Join(tmp, "gemini"), filepath.Join(tmp, "seen")
	fakebin.Write(bin, []byte(`#!/bin/sh
{ echo "$GEMINI_CLI_HOME"; echo "$*"; echo "token=$OFFICE_MCP_TOKEN"; cat "$GEMINI_CLI_HOME/.gemini/settings.json"; echo;
  test -e "$GEMINI_CLI_HOME/.gemini/oauth_creds.json" && echo creds-linked; } > `+seen+`
cat >/dev/null
echo 'Loaded cached credentials.'
echo '{"type":"init","session_id":"s","model":"gemini-3.5-flash"}'
echo '{"type":"message","role":"user","content":"chào"}'
echo '{"type":"message","role":"assistant","content":"Xin ","delta":true}'
echo '{"type":"tool_use","tool_name":"read_file","tool_id":"1","parameters":{"file_path":"a.txt"}}'
echo '{"type":"tool_use","tool_name":"mcp_office_ops_overview","tool_id":"2","parameters":{}}'
echo '{"type":"tool_use","tool_name":"mcp_my_srv_list","tool_id":"3","parameters":{}}'
echo '{"type":"message","role":"assistant","content":"chào","delta":true}'
echo '{"type":"result","status":"success","stats":{"input_tokens":12,"output_tokens":3}}'
`))

	req := RunRequest{Bin: bin, Prompt: "chào", WorkDir: tmp, Write: true,
		Office: &OfficeAccess{MCPURL: "http://127.0.0.1:1/mcp", Token: "tok", Gateway: []string{"my srv"}}}
	var text strings.Builder
	res, err := geminiRunner{}.Run(context.Background(), req, func(e Event) {
		if e.Type == "text" {
			text.WriteString(e.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Xin chào" || res.Text != "Xin chào" || res.Usage.Model != "gemini-3.5-flash" || res.Usage.InputTokens != 12 || res.Usage.OutputTokens != 3 {
		t.Fatalf("text=%q res=%+v", text.String(), res)
	}
	if len(res.Tools) != 3 || res.Tools[0].Summary != "Đọc a.txt" || res.Tools[1].Name != "mcp__office__ops_overview" || res.Tools[2].Name != "mcp__my srv__list" {
		t.Fatalf("tools = %+v", res.Tools)
	}

	raw, _ := os.ReadFile(seen)
	got := string(raw)
	for _, want := range []string{"--approval-mode auto_edit", "--allowed-mcp-server-names", "token=tok", "creds-linked"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	lines := strings.Split(got, "\n")
	var settings struct {
		Security   map[string]any `json:"security"`
		MCPServers map[string]struct {
			HTTPURL string            `json:"httpUrl"`
			Headers map[string]string `json:"headers"`
			Trust   bool              `json:"trust"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(lines[3]), &settings); err != nil {
		t.Fatalf("settings %q: %v", lines[3], err)
	}
	off := settings.MCPServers["office"]
	if settings.Security == nil || off.HTTPURL != "http://127.0.0.1:1/mcp" || !off.Trust || off.Headers["Authorization"] != "Bearer $OFFICE_MCP_TOKEN" {
		t.Fatalf("settings = %+v", settings)
	}
	if _, ok := settings.MCPServers["mine"]; ok {
		t.Fatal("the person's MCP servers leak in without UserMCP")
	}
	if gw := settings.MCPServers["my srv"]; gw.HTTPURL != "http://127.0.0.1:1/mcp/s/my srv" || gw.Headers[ClientHeader] != "gemini" {
		t.Fatalf("gateway = %+v", gw)
	}
	// the throwaway home is gone, the real sign-in is not
	if _, err := os.Stat(lines[0]); lines[0] == real || !os.IsNotExist(err) {
		t.Fatalf("throwaway home %q left behind", lines[0])
	}
	if _, err := os.Stat(filepath.Join(real, ".gemini", "oauth_creds.json")); err != nil {
		t.Fatal("real credentials removed")
	}
}

// A JSON line over the scanner's 16MB cap must fail the run instead of
// silently returning an empty "success" (the "result" event never gets read).
func TestGeminiFailsOnOversizedLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "gemini")
	script := `#!/bin/sh
cat >/dev/null
echo '{"type":"init","session_id":"s1"}'
python3 -c 'print("{\"type\":\"message\",\"role\":\"assistant\",\"content\":\"" + "x"*(17*1024*1024) + "\"}")'
`
	fakebin.Write(bin, []byte(script))
	res, err := geminiRunner{}.Run(context.Background(), RunRequest{Bin: bin, WorkDir: dir, Prompt: "hi"}, func(Event) {})
	if err == nil {
		t.Fatalf("expected an error for an oversized line, got res = %+v", res)
	}
	if res.Text != "" {
		t.Fatalf("result must not be read past the oversized line: res = %+v", res)
	}
}

func TestGeminiRunnerError(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "gemini")
	fakebin.Write(bin, []byte(`#!/bin/sh
cat >/dev/null
echo 'Please set an Auth method in your settings.json' >&2
exit 41
`))
	_, err := geminiRunner{}.Run(context.Background(), RunRequest{Bin: bin, Prompt: "x", WorkDir: tmp}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "Auth method") {
		t.Fatalf("err = %v", err)
	}
}
