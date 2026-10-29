package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/storage"
)

type apiDiscovery struct{ calls []string }

func (*apiDiscovery) Tools(officetools.Scope) []officetools.Tool {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	return []officetools.Tool{{Name: "find_tool", Schema: schema, ReadOnly: true}, {Name: "call_tool", Schema: schema}}
}

func (p *apiDiscovery) Call(_ context.Context, _ officetools.Scope, name string, _ json.RawMessage) (string, bool) {
	p.calls = append(p.calls, name)
	if name == "find_tool" {
		return "FOUND_TOOL", false
	}
	return "CALLED_TOOL", false
}

// Like the production apiGateway: no external schemas up front, while old
// names can still be dispatched separately for compatibility.
type deferredGateway struct{}

func (deferredGateway) List(context.Context) []GatewayTool { return nil }
func (deferredGateway) Call(context.Context, string, json.RawMessage) (string, bool) {
	return "legacy call should not run", true
}

func TestAPIToolboxPayloadAndDispatch(t *testing.T) {
	for _, kind := range []storage.ProviderKind{storage.ProviderAnthropic, storage.ProviderOpenAI, storage.ProviderOpenAICompatible} {
		t.Run(string(kind), func(t *testing.T) {
			calls := 0
			p := &apiDiscovery{}
			box := officetools.New(nil, nil, nil)
			box.SetMCPTools(p)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Tools    []map[string]any `json:"tools"`
					Messages json.RawMessage  `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				names := map[string]bool{}
				for _, tool := range body.Tools {
					def := tool
					if kind != storage.ProviderAnthropic {
						def, _ = tool["function"].(map[string]any)
					}
					name, _ := def["name"].(string)
					names[name] = true
					if strings.HasPrefix(name, "mcp__") {
						t.Errorf("external tool schema in API payload: %s", name)
					}
				}
				if !names["find_tool"] || !names["call_tool"] {
					t.Errorf("discovery missing from actual payload: %+v", names)
				}
				if calls == 1 && !strings.Contains(string(body.Messages), "FOUND_TOOL") {
					t.Error("find result missing from next request")
				}
				if calls == 2 && !strings.Contains(string(body.Messages), "CALLED_TOOL") {
					t.Error("call result missing from next request")
				}
				w.Header().Set("Content-Type", "application/json")
				if calls < 2 {
					name := []string{"find_tool", "call_tool"}[calls]
					if kind == storage.ProviderAnthropic {
						_ = json.NewEncoder(w).Encode(map[string]any{"model": "test", "stop_reason": "tool_use", "content": []map[string]any{{"type": "tool_use", "id": name, "name": name, "input": map[string]any{}}}})
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"model": "test", "choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": name, "type": "function", "function": map[string]any{"name": name, "arguments": "{}"}}}}}}})
					}
				} else if kind == storage.ProviderAnthropic {
					_, _ = w.Write([]byte(`{"model":"test","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}`))
				} else {
					_, _ = w.Write([]byte(`{"model":"test","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
				}
				calls++
			}))
			defer srv.Close()
			req := RunRequest{WorkDir: t.TempDir(), Prompt: "read figma", Model: "test", Provider: storage.Provider{Kind: kind, BaseURL: srv.URL}, Office: &OfficeAccess{Tools: box, Gateway: []string{"figma"}, Scope: officetools.Scope{ClientKind: "api"}, GatewayTools: deferredGateway{}}}
			req.System = gatewayBrief(req.Office)
			var runner Runner = openAIRunner{}
			if kind == storage.ProviderAnthropic {
				runner = anthropicRunner{}
			}
			out, err := runner.Run(context.Background(), req, func(Event) {})
			if err != nil || out.Text != "ok" || calls != 3 || !reflect.DeepEqual(p.calls, []string{"find_tool", "call_tool"}) {
				t.Fatalf("toolbox loop: %+v %v; requests=%d, tools=%v", out, err, calls, p.calls)
			}
		})
	}
}
