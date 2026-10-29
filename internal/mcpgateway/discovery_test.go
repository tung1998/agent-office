package mcpgateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/storage"
)

func TestFindToolsCatalogAndPagination(t *testing.T) {
	st, box := openStore(t)
	ctx := context.Background()
	gw := &mcpgateway.Gateway{Store: st, Box: box}
	add := func(name string, enabled bool, agents []string, tools []storage.MCPTool) {
		t.Helper()
		m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: name, URL: "http://127.0.0.1:1", Enabled: enabled, Agents: agents})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.MCPServers().SetCheck(ctx, m.ID, "ok", "", tools, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var tools []storage.MCPTool
	for i := 0; i < 159; i++ {
		tools = append(tools, storage.MCPTool{Name: fmt.Sprintf("design_%03d", i), Description: "Read Figma design", InputSchema: json.RawMessage(`{"type":"object","properties":{"node":{"type":"string"}}}`)})
	}
	longName := strings.Repeat("long_tool_", 10)
	tools = append(tools, storage.MCPTool{Name: longName, Description: "Unique rendering"})
	add("figma", true, nil, tools)
	add("private", true, []string{"another"}, []storage.MCPTool{{Name: "private_schema_marker"}})
	add("disabled", false, nil, tools)
	c := mcpgateway.Caller{AgentID: "me"}
	out, err := gw.FindTools(ctx, c, "", 0)
	if err != nil || len(out.Servers) != 2 || len(out.Tools) != 0 || !out.Servers[0].Available || out.Servers[1].Available {
		t.Fatalf("catalog = %+v, %v", out, err)
	}
	for _, query := range []string{"private", "private_schema_marker"} {
		out, err := gw.FindTools(ctx, c, query, 0)
		if err != nil || len(out.Tools) != 0 {
			t.Fatalf("restricted schemas leaked: %+v, %v", out, err)
		}
	}
	seen := map[string]bool{}
	for offset := 0; ; {
		out, err := gw.FindTools(ctx, c, "FIGMA design", offset)
		if err != nil || len(out.Tools) == 0 || len(out.Tools) > 5 {
			t.Fatalf("page %d: %+v, %v", offset, out, err)
		}
		for _, tool := range out.Tools {
			if seen[tool.Tool] || !json.Valid(tool.InputSchema) {
				t.Fatalf("duplicate or invalid schema: %+v", tool)
			}
			seen[tool.Tool] = true
		}
		if out.NextOffset == nil {
			break
		}
		offset = *out.NextOffset
	}
	if len(seen) != len(tools) {
		t.Fatalf("pagination returned %d of %d tools", len(seen), len(tools))
	}
	out, err = gw.FindTools(ctx, c, longName, 0)
	if err != nil || len(out.Tools) == 0 || out.Tools[0].Tool != longName {
		t.Fatalf("exact long tool name was lost: %+v, %v", out, err)
	}
	for _, offset := range []int{10000, int(^uint(0) >> 1)} {
		out, err := gw.FindTools(ctx, c, "figma", offset)
		if err != nil || len(out.Tools) != 0 {
			t.Fatalf("offset %d: %+v, %v", offset, out, err)
		}
	}
	if _, err := gw.FindTools(ctx, c, "figma", -1); err == nil {
		t.Fatal("negative offset accepted")
	}
}

func TestDiscoveredCallKeepsGatewayPolicy(t *testing.T) {
	st, box := openStore(t)
	ctx := context.Background()
	var seen []string
	srv := fakeMCP(t, false, &seen)
	enc, _ := mcpgateway.SealMap(box, map[string]string{"Authorization": upstreamSecret})
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "test", URL: srv.URL, Enabled: true, HeadersEnc: enc})
	if err != nil {
		t.Fatal(err)
	}
	proposals := 0
	gw := &mcpgateway.Gateway{Store: st, Box: box, Propose: func(_ context.Context, _ mcpgateway.Caller, server, tool string, args json.RawMessage) (mcpgateway.Proposal, error) {
		proposals++
		if server != "test" || tool != "write" || string(args) != `{"x":1}` {
			t.Fatalf("proposal lost call: %s/%s %s", server, tool, args)
		}
		return mcpgateway.Proposal{ActionID: "act_discovery", Status: "pending"}, nil
	}}
	m, err = gw.CheckServer(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	c := mcpgateway.Caller{Kind: "api", AgentID: "me", ConversationID: "conv", JobID: "job"}
	if text, failed := gw.InvokeFound(ctx, c, "test", "resolve", json.RawMessage(`{}`)); failed || text != "hello resolve" {
		t.Fatalf("read: %q %v", text, failed)
	}
	requests := len(seen)
	if _, failed := gw.InvokeFound(ctx, c, "test", "write", json.RawMessage(`{"x":1}`)); !failed || len(seen) != requests {
		t.Fatal("read-only caller wrote upstream")
	}
	c.CanPropose = true
	if text, failed := gw.InvokeFound(ctx, c, "test", "write", json.RawMessage(`{"x":1}`)); failed || !strings.Contains(text, "act_discovery") || proposals != 1 || len(seen) != requests {
		t.Fatalf("proposal: %q %v, count %d", text, failed, proposals)
	}
	c.CanWrite = true
	if _, failed := gw.InvokeFound(ctx, c, "test", "unknown", json.RawMessage(`{}`)); !failed || len(seen) != requests {
		t.Fatal("unknown tool reached upstream")
	}
	if _, failed := gw.InvokeFound(ctx, c, "test", "write", json.RawMessage(`{"x":1}`)); failed {
		t.Fatal("writer failed")
	}
	calls, err := st.MCPCalls().List(ctx, m.ID, 20)
	if err != nil || len(calls) != 4 {
		t.Fatalf("missing audit calls: %+v %v", calls, err)
	}
	m.Agents = []string{"another"}
	if err := st.MCPServers().Update(ctx, m); err != nil {
		t.Fatal(err)
	}
	requests = len(seen)
	if _, failed := gw.InvokeFound(ctx, c, "test", "resolve", json.RawMessage(`{}`)); !failed || len(seen) != requests {
		t.Fatal("assignment revocation did not take effect")
	}
}

func TestFindToolsEmptyCatalogAndVietnamese(t *testing.T) {
	st, box := openStore(t)
	ctx := context.Background()
	gw := &mcpgateway.Gateway{Store: st, Box: box}
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "figma", URL: "http://127.0.0.1:1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := gw.FindTools(ctx, mcpgateway.Caller{}, "figma", 0)
	if err != nil || len(out.Servers) != 1 || out.Servers[0].CatalogStatus != "unchecked" || len(out.Tools) != 0 || out.Hint == "" {
		t.Fatalf("unchecked catalog: %+v %v", out, err)
	}
	if text, failed := gw.InvokeFound(ctx, mcpgateway.Caller{}, "figma", "screenshot", json.RawMessage(`{}`)); !failed || !strings.Contains(text, "kiểm tra MCP") {
		t.Fatalf("empty call needs actionable error: %s %v", text, failed)
	}
	if err := st.MCPServers().SetCheck(ctx, m.ID, "ok", "", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	out, err = gw.FindTools(ctx, mcpgateway.Caller{}, "", 0)
	if err != nil || len(out.Servers) != 1 || out.Servers[0].CatalogStatus != "empty" {
		t.Fatalf("checked empty catalog: %+v %v", out, err)
	}
	if err := st.MCPServers().SetCheck(ctx, m.ID, "ok", "", []storage.MCPTool{{Name: "screenshot", Description: "Chụp ảnh thiết kế"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"chụp ảnh", "chup anh", "CHỤP ẢNH", "screenshot"} {
		out, err = gw.FindTools(ctx, mcpgateway.Caller{}, query, 0)
		if err != nil || len(out.Tools) != 1 || out.Tools[0].Tool != "screenshot" {
			t.Fatalf("query %q: %+v %v", query, out, err)
		}
	}
	// Translation is deliberately left to the agent, with an explicit retry hint.
	out, err = gw.FindTools(ctx, mcpgateway.Caller{}, "no_such_tool", 0)
	if err != nil || len(out.Tools) != 0 || !strings.Contains(out.Hint, "English") {
		t.Fatalf("no-match hint: %+v %v", out, err)
	}
}
