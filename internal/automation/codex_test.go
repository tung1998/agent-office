package automation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codexTOML = `model = "gpt-5"

[mcp_servers.fs]
command = "npx"
args = [
  "-y",
  "@modelcontextprotocol/server-filesystem", # the server
  "/tmp/a b",
]
env = { API_KEY = "sk-1", "MODE" = 'x' }

[mcp_servers.fs.tools.read]
approve = true

[mcp_servers."web-api"]
url = "https://mcp.example/mcp"
bearer_token_env_var = "OFFICE_TEST_CODEX_TOKEN"

[mcp_servers."web-api".http_headers]
X-Team = "core"

[profiles.fast]
model = "gpt-5-mini"
`

func TestCodexServer(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(codexTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := codexServer(p, "fs")
	if err != nil {
		t.Fatal(err)
	}
	args, _ := fs["args"].([]any)
	env, _ := fs["env"].(map[string]any)
	if fs["command"] != "npx" || len(args) != 3 || args[2] != "/tmp/a b" || env["API_KEY"] != "sk-1" || env["MODE"] != "x" {
		t.Fatalf("fs = %#v", fs)
	}
	t.Setenv("OFFICE_TEST_CODEX_TOKEN", "tok-9")
	web, err := codexServer(p, "web-api")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := web["headers"].(map[string]any)
	if web["url"] != "https://mcp.example/mcp" || web["type"] != "http" || h["Authorization"] != "Bearer tok-9" || h["X-Team"] != "core" {
		t.Fatalf("web = %#v", web)
	}
	if _, err := codexServer(p, "nope"); err != ErrNotFound {
		t.Fatalf("missing server err = %v", err)
	}

	in := Installer{Trash: filepath.Join(t.TempDir(), "trash")}
	backup, err := in.removeCodexServer(p, "fs")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "server-filesystem") || strings.Contains(string(raw), "approve") ||
		!strings.Contains(string(raw), "web-api") || !strings.Contains(string(raw), "[profiles.fast]") {
		t.Fatalf("after remove:\n%s", raw)
	}
	if b, _ := os.ReadFile(backup); string(b) != codexTOML {
		t.Fatal("backup is not the file as it was")
	}

	// putting it back gives the same config
	if err := appendCodexServer(p, "fs", fs); err != nil {
		t.Fatal(err)
	}
	again, err := codexServer(p, "fs")
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := again["args"].([]any); again["command"] != "npx" || len(a) != 3 {
		t.Fatalf("put back = %#v", again)
	}
	if err := appendCodexServer(p, "fs", fs); err != ErrExists {
		t.Fatalf("second put back err = %v", err)
	}
}
