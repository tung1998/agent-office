package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// An administrator's run fixes an office MCP server through propose_change
// (a command path from another machine): applied at once, the stored env
// kept as it was (never the masks get shows). A project run without admin
// access is sent to the office assistant, and no proposal carries env.
func TestConfigChangeMCPServer(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/mcp/servers", map[string]any{"name": "figma", "kind": "stdio",
		"command": "/Users/old/bin/figma", "env": map[string]string{"FIGMA_KEY": "s3cret-value"}, "enabled": false}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	id := body["server"].(map[string]any)["id"].(string)
	before, _ := e.st.MCPServers().Get(ctx, id)

	propose := func(sc actions.Scope, patch map[string]any) (storage.Action, error) {
		raw, _ := json.Marshal(patch)
		return e.acts.Propose(ctx, sc, "config_change", "", "sửa lệnh MCP", storage.ActionArgs{Change: &storage.ConfigChange{Resource: "mcp_server", Op: "update", ID: id, Patch: raw}})
	}
	adminRun := actions.Scope{ProjectID: pid, RunRef: "r1", Agent: "Lead", Level: perm.Operate, Access: perm.Admin(perm.Access{}), Admin: true, AdminBy: "admin@x.io"}

	if _, err := propose(actions.Scope{ProjectID: pid, Agent: "Lead", Level: perm.Operate}, map[string]any{"command": "npx"}); err == nil {
		t.Fatal("a project run without admin access changed an office MCP server")
	}
	if _, err := propose(adminRun, map[string]any{"env": map[string]string{"FIGMA_KEY": "x"}}); err == nil {
		t.Fatal("a proposal carried env (may hold keys)")
	}

	a, err := propose(adminRun, map[string]any{"command": "npx", "args": []string{"-y", "figma-mcp"}})
	if err != nil || a.Status != "done" {
		t.Fatalf("propose = %+v %v", a, err)
	}
	got, _ := e.st.MCPServers().Get(ctx, id)
	if got.Command != "npx" || len(got.Args) != 2 || got.Args[1] != "figma-mcp" {
		t.Fatalf("server = %q %v", got.Command, got.Args)
	}
	if got.EnvEnc != before.EnvEnc || got.Enabled {
		t.Fatalf("fields the patch did not name changed: env same=%v enabled=%v", got.EnvEnc == before.EnvEnc, got.Enabled)
	}

}
