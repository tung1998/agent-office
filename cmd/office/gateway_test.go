package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func TestGatewayDiscoveryOverOfficeMCPAndAPI(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "figma", URL: "http://127.0.0.1:1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var tools []storage.MCPTool
	for i := 0; i < 159; i++ {
		tools = append(tools, storage.MCPTool{Name: fmt.Sprintf("design_%03d", i), Description: "EXTERNAL_SCHEMA_MARKER", InputSchema: json.RawMessage(`{"type":"object","properties":{"node":{"type":"string"}}}`)})
	}
	if err := st.MCPServers().SetCheck(ctx, m.ID, "ok", "", tools, time.Now()); err != nil {
		t.Fatal(err)
	}
	gw := &mcpgateway.Gateway{Store: st}
	box := officetools.New(st, nil, nil)
	box.SetMCPTools(gatewayDiscovery{st: st, gw: gw})
	mcp := mcpserver.New(box, "test")
	for _, office := range []bool{false, true} {
		t.Run(fmt.Sprintf("office=%t", office), func(t *testing.T) {
			sc := officetools.Scope{Office: office, Level: "read"}
			token, revoke := mcp.Grant(sc, time.Minute)
			defer revoke()
			call := func(method, params string) string {
				t.Helper()
				req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":`+params+`}`))
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				mcp.ServeHTTP(rec, req)
				data, _ := io.ReadAll(rec.Result().Body)
				if rec.Code != 200 {
					t.Fatalf("MCP %s: %d %s", method, rec.Code, data)
				}
				return string(data)
			}
			listed := call("tools/list", `{}`)
			if !strings.Contains(listed, `"name":"find_tool"`) || !strings.Contains(listed, `"name":"call_tool"`) || strings.Contains(listed, "EXTERNAL_SCHEMA_MARKER") {
				t.Fatalf("MCP did not defer schemas: %s", listed)
			}
			for _, query := range []string{"", "figma"} {
				out := call("tools/call", `{"name":"find_tool","arguments":{"query":"`+query+`"}}`)
				if strings.Contains(out, `"isError":true`) || strings.Contains(out, `"error":`) || !strings.Contains(out, "figma") {
					t.Fatalf("find_tool: %s", out)
				}
				count := strings.Count(out, "EXTERNAL_SCHEMA_MARKER")
				if (query == "" && count != 0) || (query != "" && count != 5) {
					t.Fatalf("query %q returned %d schemas", query, count)
				}
			}
			_, _, api := gatewayFor(st, gw)(ctx, sc)
			if got := api.List(ctx); len(got) != 0 {
				t.Fatalf("API still sends external schemas: %+v", got)
			}
			defs, _ := json.Marshal(box.ToolsFor(sc))
			if strings.Contains(string(defs), "EXTERNAL_SCHEMA_MARKER") || !strings.Contains(string(defs), "find_tool") {
				t.Fatal("API office tools lack discovery or contain external schemas")
			}
			out, failed := box.Call(ctx, sc, "find_tool", json.RawMessage(`{"query":"figma"}`))
			if failed || strings.Count(out, "EXTERNAL_SCHEMA_MARKER") != 5 {
				t.Fatalf("API discovery: %s %v", out, failed)
			}
		})
	}
	if box.Has(officetools.Scope{Consult: true}, "call_tool") || box.Has(officetools.Scope{Consult: true}, "find_tool") {
		t.Fatal("expert scope gained external tools")
	}
	p := gatewayDiscovery{st: st, gw: gw}
	for _, raw := range []string{`{`, `{}`, `{"server":"figma","tool":"design_000","arguments":[]}`, `{"server":"figma","tool":"design_000","arguments":null}`} {
		if _, failed := p.Call(ctx, officetools.Scope{}, "call_tool", json.RawMessage(raw)); !failed {
			t.Fatalf("invalid arguments accepted: %s", raw)
		}
	}
}

func TestToolboxPreservesMCPContentAndCaller(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "office.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var upstreamCalls atomic.Int64
	fixture := `{"content":[{"type":"text","text":"design ready"},{"type":"image","mimeType":"image/png","data":"aW1hZ2U="},{"type":"resource","resource":{"uri":"design://node","mimeType":"application/json","text":"{\"node\":1}"}},{"type":"resource","resource":{"uri":"design://asset","mimeType":"application/octet-stream","blob":"YmluYXJ5"}},{"type":"resource_link","uri":"design://more","name":"more"}],"structuredContent":{"node":9007199254740993},"_meta":{"upstream":"kept"},"isError":false}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		var q struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Arguments struct {
					Fail bool `json:"fail"`
					Long bool `json:"long"`
				} `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		var result any = map[string]any{"supportedVersions": []string{"2026-07-28"}}
		if q.Method == "tools/call" {
			upstreamCalls.Add(1)
			var data map[string]any
			decoder := json.NewDecoder(strings.NewReader(fixture))
			decoder.UseNumber()
			_ = decoder.Decode(&data)
			data["isError"] = q.Params.Arguments.Fail
			if q.Params.Arguments.Long {
				content := data["content"].([]any)
				content[0].(map[string]any)["text"] = strings.Repeat("long text\n", 10000)
				content[2].(map[string]any)["resource"].(map[string]any)["text"] = strings.Repeat("resource text\n", 10000)
				delete(data, "structuredContent")
			}
			result = data
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
	}))
	defer upstream.Close()
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "figma", URL: upstream.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ro := true
	if err := st.MCPServers().SetCheck(ctx, m.ID, "ok", "", []storage.MCPTool{{Name: "screenshot", ReadOnly: &ro}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	gw := &mcpgateway.Gateway{Store: st}
	box := officetools.New(st, nil, nil)
	box.SetMCPTools(gatewayDiscovery{st: st, gw: gw})
	server := mcpserver.New(box, "test")
	for _, kind := range []string{"codex", "gemini"} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/error=%t", kind, failed), func(t *testing.T) {
				sc := officetools.Scope{ClientKind: kind, Level: perm.Read, JobID: "job", RunRef: "run"}
				token, revoke := server.Grant(sc, time.Minute)
				defer revoke()
				body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"call_tool","arguments":{"server":"figma","tool":"screenshot","arguments":{"fail":%t}}}}`, failed)
				if failed { // also exercise modern response metadata without losing upstream metadata
					body = strings.Replace(body, `"params":{`, `"params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},`, 1)
				}
				req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				server.ServeHTTP(rec, req)
				if !strings.Contains(rec.Body.String(), `"node":9007199254740993`) {
					t.Fatalf("structured output lost numeric precision: %s", rec.Body.String())
				}
				var response struct {
					Result map[string]any  `json:"result"`
					Error  json.RawMessage `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != 200 || len(response.Error) > 0 {
					t.Fatalf("MCP failed: %s (%v)", rec.Body.String(), err)
				}
				var want map[string]any
				_ = json.Unmarshal([]byte(fixture), &want)
				want["isError"] = failed
				if failed {
					want["resultType"] = "complete"
					want["_meta"].(map[string]any)["io.modelcontextprotocol/serverInfo"] = map[string]any{"name": "agent-office", "version": "test"}
				}
				if !reflect.DeepEqual(response.Result, want) {
					t.Fatalf("content lost:\ngot: %#v\nwant: %#v", response.Result, want)
				}
				calls, err := st.MCPCalls().List(ctx, m.ID, 1)
				if err != nil || len(calls) != 1 || calls[0].CallerKind != kind || calls[0].JobID != "job" {
					t.Fatalf("incorrect audit caller: %+v %v", calls, err)
				}
			})
		}
	}
	if upstreamCalls.Load() != 4 {
		t.Fatalf("tool was repeated: %d calls", upstreamCalls.Load())
	}
	for _, sc := range []officetools.Scope{{ClientKind: "claude"}, {ClientKind: "antigravity"}, {Consult: true}} {
		if box.Has(sc, "find_tool") || box.Has(sc, "call_tool") {
			t.Fatalf("duplicate or forbidden toolbox: %+v", sc)
		}
		out := box.CallMCP(ctx, sc, "call_tool", json.RawMessage(`{"server":"figma","tool":"screenshot","arguments":{}}`))
		if out["isError"] != true || upstreamCalls.Load() != 4 {
			t.Fatalf("hidden toolbox was callable: %+v", out)
		}
	}
	// Native MCP content survives text truncation, while API callers retain the
	// previous limit on flattened resources as well as ordinary text blocks.
	gw.MaxResult = 512
	caller := mcpgateway.Caller{Kind: "codex", JobID: "job"}
	args := json.RawMessage(`{"long":true}`)
	raw := gw.InvokeFoundResult(ctx, caller, "figma", "screenshot", args)
	var result struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			Resource struct {
				Text string `json:"text"`
			} `json:"resource"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Content) != 5 {
		t.Fatalf("long result: %s %v", raw, err)
	}
	if !strings.Contains(result.Content[0].Text, "mcp_result") || result.Content[1].Data != "aW1hZ2U=" || result.Content[2].Resource.Text != strings.Repeat("resource text\n", 10000) {
		t.Fatal("MCP text truncation lost image or resource")
	}
	text, failed := gw.InvokeFound(ctx, caller, "figma", "screenshot", args)
	if failed || len(text) > 2000 || !strings.Contains(text, "mcp_result") {
		t.Fatalf("API flattened result no longer bounded: %d bytes, error=%t", len(text), failed)
	}
}
