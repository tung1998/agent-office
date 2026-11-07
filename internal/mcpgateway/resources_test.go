package mcpgateway_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/storage"
)

// legacyMCPWithResources is a legacy (initialize) server that also answers
// resources/list and resources/read.
func legacyMCPWithResources(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				URI string `json:"uri"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		reply := func(v map[string]any) {
			v["jsonrpc"], v["id"] = "2.0", m.ID
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		}
		var result map[string]any
		switch m.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}
		case "resources/list":
			result = map[string]any{"resources": []any{
				map[string]any{"uri": "file:///notes.md", "name": "notes", "description": "team notes", "mimeType": "text/markdown"},
			}}
		case "resources/read":
			switch m.Params.URI {
			case "file:///notes.md":
				result = map[string]any{"contents": []any{
					map[string]any{"uri": m.Params.URI, "mimeType": "text/markdown", "text": "hello team"},
				}}
			case "file:///logo.png":
				result = map[string]any{"contents": []any{
					map[string]any{"uri": m.Params.URI, "mimeType": "image/png", "blob": base64.StdEncoding.EncodeToString([]byte("PNGDATA"))},
				}}
			case "file:///empty.md":
				result = map[string]any{"contents": []any{}}
			default:
				reply(map[string]any{"error": map[string]any{"code": -32602, "message": "unknown resource"}})
				return
			}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Mcp-Session-Id", "s1")
		reply(map[string]any{"result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResourcesListsAndReads(t *testing.T) {
	srv := legacyMCPWithResources(t)
	resources, err := mcpgateway.Resources(context.Background(), nil, srv.URL, nil)
	if err != nil || len(resources) != 1 || resources[0].URI != "file:///notes.md" || resources[0].Name != "notes" {
		t.Fatalf("resources = %+v %v", resources, err)
	}
	text, blob, mime, err := mcpgateway.ReadResource(context.Background(), nil, srv.URL, nil, "file:///notes.md")
	if err != nil || text != "hello team" || blob != nil || mime != "text/markdown" {
		t.Fatalf("read = %q %v %q %v", text, blob, mime, err)
	}
}

// A server with no resources capability (resources/list -> method not
// found) is not an error: it just has none.
func TestResourcesNoCapability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		result := map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}
		body := map[string]any{"jsonrpc": "2.0", "id": m.ID}
		if m.Method == "initialize" {
			body["result"] = result
		} else {
			body["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		w.Header().Set("Mcp-Session-Id", "s1")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	resources, err := mcpgateway.Resources(context.Background(), nil, srv.URL, nil)
	if err != nil || len(resources) != 0 {
		t.Fatalf("resources = %+v %v", resources, err)
	}
}

// A modern (2026-07-28, stateless) server with no resources capability
// answers resources/list as an HTTP error carrying the RPC code
// (StatusError), not a JSON-RPC error over 200 OK: that must count as "no
// resources" too, not a failure.
func TestResourcesNoCapabilityModern(t *testing.T) {
	var methods []string
	srv := modernMCP(t, &methods, nil)
	resources, err := mcpgateway.Resources(context.Background(), nil, srv.URL, nil)
	if err != nil || len(resources) != 0 {
		t.Fatalf("resources = %+v %v", resources, err)
	}
}

// ReadResource decodes a binary (blob) resource and reports an error when
// the server answers with no content for the URI asked.
func TestReadResourceBlobAndEmpty(t *testing.T) {
	srv := legacyMCPWithResources(t)
	_, blob, mime, err := mcpgateway.ReadResource(context.Background(), nil, srv.URL, nil, "file:///logo.png")
	if err != nil || string(blob) != "PNGDATA" || mime != "image/png" {
		t.Fatalf("blob read = %v %q %v", blob, mime, err)
	}
	if _, _, _, err := mcpgateway.ReadResource(context.Background(), nil, srv.URL, nil, "file:///empty.md"); err == nil {
		t.Fatal("expected an error for a resource with no content")
	}
}

// CheckServer saves the resources a check found, alongside its tools.
func TestCheckServerSavesResources(t *testing.T) {
	st, box := openStore(t)
	ctx := context.Background()
	srv := legacyMCPWithResources(t)
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "notes", URL: srv.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	gw := &mcpgateway.Gateway{Store: st, Box: box}
	if _, err := gw.CheckServer(ctx, m); err != nil {
		t.Fatal(err)
	}
	got, err := st.MCPServers().Get(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LastResources) != 1 || got.LastResources[0].URI != "file:///notes.md" || got.LastResources[0].Name != "notes" {
		t.Fatalf("LastResources = %+v", got.LastResources)
	}
}

// Gateway.ReadResource reads a resource through the gateway, by server name,
// resolving the stored headers and OAuth token the way CallTool does.
func TestGatewayReadResource(t *testing.T) {
	st, box := openStore(t)
	ctx := context.Background()
	srv := legacyMCPWithResources(t)
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "notes", URL: srv.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	gw := &mcpgateway.Gateway{Store: st, Box: box}
	text, blob, mime, err := gw.ReadResource(ctx, m.Name, "file:///notes.md")
	if err != nil || text != "hello team" || blob != nil || mime != "text/markdown" {
		t.Fatalf("read = %q %v %q %v", text, blob, mime, err)
	}
	if _, _, _, err := gw.ReadResource(ctx, "no-such-server", "file:///notes.md"); err == nil {
		t.Fatal("expected an error for an unknown server")
	}
}
