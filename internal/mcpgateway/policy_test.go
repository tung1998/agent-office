package mcpgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// The tool policy and agent assignment (ADR-093): read-only and trusted
// tools pass, a tool that writes becomes a proposal (or is refused), a
// server given to other agents is not there, and each call is logged.
func TestPolicy(t *testing.T) {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.Load(filepath.Join(t.TempDir(), "secret.key"))
	var seen []string
	srv := fakeMCP(t, false, &seen)
	enc, _ := mcpgateway.SealMap(box, map[string]string{"Authorization": upstreamSecret})
	m, _ := st.MCPServers().Create(ctx, storage.MCPServer{Name: "c7", URL: srv.URL, HeadersEnc: enc, Enabled: true})
	only, _ := st.MCPServers().Create(ctx, storage.MCPServer{Name: "mine", URL: srv.URL, HeadersEnc: enc, Enabled: true, Agents: []string{"agt_other"}})

	callers := map[string]mcpgateway.Caller{
		"reader":   {Kind: "claude", Agent: "Reader", AgentID: "agt_r"},
		"proposer": {Kind: "claude", Agent: "Dev", AgentID: "agt_d", CanPropose: true, ConversationID: "cnv_1"},
		"writer":   {Kind: "codex", Agent: "Ops", AgentID: "agt_o", CanWrite: true},
		"person":   {Kind: "person", CanWrite: true, CanPropose: true}, // an admin's token
		"member":   {Kind: "person", Agent: "CLI (member)", CanPropose: true},
	}
	var proposed []string
	gw := &mcpgateway.Gateway{Store: st, Box: box,
		Identify: func(r *http.Request) (mcpgateway.Caller, bool) {
			c, ok := callers[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
			return c, ok
		},
		Propose: func(_ context.Context, c mcpgateway.Caller, server, tool string, args json.RawMessage) (mcpgateway.Proposal, error) {
			proposed = append(proposed, c.Agent+":"+server+"/"+tool+":"+string(args))
			return mcpgateway.Proposal{ActionID: "act_1", Status: "pending"}, nil
		}}
	m, _ = gw.CheckServer(ctx, m) // learn which tools are read-only
	mux := http.NewServeMux()
	mux.Handle("/mcp/s/{name}", gw)
	office := httptest.NewServer(mux)
	t.Cleanup(office.Close)

	call := func(server, who, tool string) (int, string) {
		body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"` + tool + `","arguments":{"x":1}}}`
		req, _ := http.NewRequest("POST", office.URL+"/mcp/s/"+server, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+who)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}

	seen = nil
	if code, body := call("c7", "reader", "resolve"); code != 200 || !strings.Contains(body, "hello resolve") {
		t.Fatalf("read-only tool = %d %s", code, body)
	}
	if code, body := call("c7", "reader", "write"); code != 200 || !strings.Contains(body, "chỉ được đọc") || !strings.Contains(body, `"isError":true`) {
		t.Fatalf("reader writes = %d %s", code, body)
	}
	if code, body := call("c7", "proposer", "write"); code != 200 || !strings.Contains(body, "act_1") || len(proposed) != 1 || proposed[0] != `Dev:c7/write:{"x":1}` {
		t.Fatalf("proposer writes = %d %s %v", code, body, proposed)
	}
	if code, body := call("c7", "writer", "write"); code != 200 || !strings.Contains(body, "hello write") {
		t.Fatalf("writer writes = %d %s", code, body)
	}
	if code, body := call("c7", "person", "write"); code != 200 || !strings.Contains(body, "hello write") {
		t.Fatalf("person writes = %d %s", code, body)
	}
	// a member's token is a person too, but its writes wait for an approval
	if code, body := call("c7", "member", "write"); code != 200 || !strings.Contains(body, "act_1") || len(proposed) != 2 || proposed[1] != `CLI (member):c7/write:{"x":1}` {
		t.Fatalf("member writes = %d %s %v", code, body, proposed)
	}
	// only 3 calls reached the server: resolve, and the two allowed writes
	// (each call opens no session of its own through the gateway)
	if len(seen) != 3 {
		t.Fatalf("server got %d calls", len(seen))
	}

	// a trusted tool passes for anyone
	m.TrustedTools = []string{"write"}
	_ = st.MCPServers().Update(ctx, m)
	if code, body := call("c7", "reader", "write"); code != 200 || !strings.Contains(body, "hello write") {
		t.Fatalf("trusted tool = %d %s", code, body)
	}

	// a server given to another agent is not there
	if code, _ := call("mine", "reader", "resolve"); code != 404 {
		t.Fatalf("unassigned server = %d", code)
	}
	if code, _ := call("mine", "person", "resolve"); code != 200 {
		t.Fatalf("a person gets every server = %d", code)
	}
	if names := gw.NamesFor(ctx, callers["reader"]); strings.Join(names, ",") != "c7" {
		t.Fatalf("NamesFor reader = %v", names)
	}
	if names := gw.NamesFor(ctx, mcpgateway.Caller{Kind: "claude", AgentID: "agt_other"}); strings.Join(names, ",") != "c7,mine" {
		t.Fatalf("NamesFor other = %v", names)
	}
	_ = only

	// the log: every call, with who and how it ended
	calls, _ := st.MCPCalls().List(ctx, m.ID, 50)
	by := map[string]int{}
	for _, c := range calls {
		by[c.Status]++
	}
	if by["ok"] != 4 || by["denied"] != 1 || by["proposed"] != 2 {
		t.Fatalf("log = %v (%+v)", by, calls)
	}
	for _, c := range calls {
		if c.Status == "proposed" && c.Caller != "CLI (member)" && (c.ActionID != "act_1" || c.Caller != "Dev" || c.ConversationID != "cnv_1") {
			t.Fatalf("proposed call logged as %+v", c)
		}
	}

	// an API run: the same policy, office calls the tool itself
	m.TrustedTools = nil
	_ = st.MCPServers().Update(ctx, m)
	if text, isErr := gw.Invoke(ctx, callers["reader"], "c7", "resolve", json.RawMessage(`{}`)); isErr || text != "hello resolve" {
		t.Fatalf("Invoke read = %q %v", text, isErr)
	}
	if text, isErr := gw.Invoke(ctx, callers["reader"], "c7", "write", nil); !isErr || !strings.Contains(text, "chỉ được đọc") {
		t.Fatalf("Invoke reader write = %q %v", text, isErr)
	}
	if text, _ := gw.Invoke(ctx, callers["proposer"], "c7", "write", nil); !strings.Contains(text, "act_1") {
		t.Fatalf("Invoke proposer write = %q", text)
	}
	if text, isErr := gw.Invoke(ctx, callers["reader"], "mine", "resolve", nil); !isErr || !strings.Contains(text, "không có MCP") {
		t.Fatalf("Invoke unassigned = %q %v", text, isErr)
	}
	tools := gw.APITools(ctx, callers["reader"])
	if len(tools) != 2 || tools[0].Name != "mcp__c7__resolve" || len(tools[0].Schema) == 0 {
		t.Fatalf("APITools = %+v", tools)
	}
	// an approved call (actions) goes straight through
	if text, isErr, err := gw.CallTool(ctx, "c7", "write", json.RawMessage(`{"x":1}`)); err != nil || isErr || text != "hello write" {
		t.Fatalf("CallTool = %q %v %v", text, isErr, err)
	}
}
