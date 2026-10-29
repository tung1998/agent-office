package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/mcpgateway"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// The MCP gateway's view of who calls (ADR-093): a run's scope becomes a
// Caller, its agent resolved to an id for the servers' agent lists.

// gatewayCaller is the caller of a run with scope sc; kind is the AI
// (claude | codex | api).
func gatewayCaller(ctx context.Context, st storage.Store, sc officetools.Scope, kind string) mcpgateway.Caller {
	c := mcpgateway.Caller{Kind: kind, Agent: sc.Agent, ProjectID: sc.ProjectID, ConversationID: sc.ConversationID,
		TaskID: sc.TaskID, JobID: sc.JobID, Scope: sc,
		CanWrite:   sc.Access.Can(perm.CapMCPWrite),
		CanPropose: sc.Access.Can(perm.CapPropose) && !sc.AnswerOnly}
	switch {
	case strings.HasPrefix(sc.RunRef, "cli-"): // a person's own Claude Code (ADR-047)
		c.Kind, c.Agent = "person", ""
	case sc.Office:
		c.AgentID = mcpgateway.AssistantID
	default:
		c.AgentID = agentIDByName(ctx, st, sc.ProjectID, sc.Agent)
	}
	return c
}

// agentIDByName is the id of the project's agent of that name ("" if none).
func agentIDByName(ctx context.Context, st storage.Store, projectID, name string) string {
	if projectID == "" || name == "" {
		return ""
	}
	agents, err := st.Agents().List(ctx, projectID)
	if err != nil {
		return ""
	}
	for _, a := range agents {
		if a.Name == name {
			return a.ID
		}
	}
	return ""
}

// gatewayIdentify authenticates a request to /mcp/s/<name> with the run's
// token (or a person's) and says who calls.
func gatewayIdentify(st storage.Store, mcp *mcpserver.Server) func(r *http.Request) (mcpgateway.Caller, bool) {
	return func(r *http.Request) (mcpgateway.Caller, bool) {
		sc, ok := mcp.ScopeOf(r)
		if !ok {
			return mcpgateway.Caller{}, false
		}
		kind := r.Header.Get(chat.ClientHeader)
		if kind != "codex" && kind != "gemini" && kind != "antigravity" {
			kind = "claude"
		}
		return gatewayCaller(r.Context(), st, sc, kind), true
	}
}

// gatewayProposer turns a call of a tool that writes into an mcp_call
// proposal of the run's conversation/task.
func gatewayProposer(acts *actions.Service) mcpgateway.Proposer {
	return func(ctx context.Context, c mcpgateway.Caller, server, tool string, args json.RawMessage) (mcpgateway.Proposal, error) {
		sc, ok := c.Scope.(officetools.Scope)
		if !ok {
			return mcpgateway.Proposal{}, errors.New("không rõ lượt chạy")
		}
		reason := "Tham số: " + string(args)
		if r := []rune(reason); len(r) > 1500 {
			reason = string(r[:1500]) + "…"
		}
		a, err := acts.Propose(ctx, sc, "mcp_call", server+"/"+tool, reason,
			storage.ActionArgs{MCP: &storage.MCPCallArgs{Server: server, Tool: tool, Arguments: args}})
		if err != nil {
			return mcpgateway.Proposal{}, err
		}
		return mcpgateway.Proposal{ActionID: a.ID, Status: a.Status, Detail: a.Detail}, nil
	}
}

// gatewayFor gives a run the servers its agent gets: names for Claude Code
// and Codex, tools for an API run; and the names of those it does not get.
func gatewayFor(st storage.Store, gw *mcpgateway.Gateway) chat.GatewayFor {
	return func(ctx context.Context, sc officetools.Scope) ([]string, []string, chat.GatewayTools) {
		c := gatewayCaller(ctx, st, sc, gatewayClient(sc))
		return gw.NamesFor(ctx, c), gw.NotFor(ctx, c), &apiGateway{gw: gw, caller: c}
	}
}

// apiGateway is the gateway's tools for one API run.
type apiGateway struct {
	gw     *mcpgateway.Gateway
	caller mcpgateway.Caller
}

// Discovery is supplied by office's find_tool/call_tool instead of sending
// every external schema with each model request.
func (a *apiGateway) List(context.Context) []chat.GatewayTool { return nil }

func (a *apiGateway) Call(ctx context.Context, name string, args json.RawMessage) (string, bool) {
	// Keep old calls in an existing session working, rechecking today's catalog.
	for _, t := range a.gw.APITools(ctx, a.caller) {
		if t.Name == name {
			return a.gw.Invoke(ctx, a.caller, t.Server, t.Tool, args)
		}
	}
	return "không có tool " + name, true
}

// gatewayDiscovery is shared by CLI and API runs, under their original scope.
type gatewayDiscovery struct {
	st storage.Store
	gw *mcpgateway.Gateway
}

func (p gatewayDiscovery) Tools(sc officetools.Scope) []officetools.Tool {
	if sc.Consult || sc.ClientKind == "claude" || sc.ClientKind == "antigravity" || strings.HasPrefix(sc.RunRef, "cli-") {
		return nil
	}
	return []officetools.Tool{
		{Name: "find_tool", ReadOnly: true,
			Description: "Find external MCP tools in office when needed. Empty query lists servers, access and catalog status; keywords or a server/tool name return up to 5 matching tools with input schemas. Search English descriptions with English keywords, even for a Vietnamese request; if no match, retry with the server name. Use offset for the next page. Finding does not run tools or grant access.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]any{"type": "string"}, "offset": map[string]any{"type": "integer", "minimum": 0},
			}}},
		{Name: "call_tool",
			Description: "Call an external MCP tool found with find_tool. Pass the exact server, tool and arguments matching its input schema. Office checks current access, asks approval when required, and logs the call.",
			Schema: map[string]any{"type": "object", "required": []string{"server", "tool", "arguments"}, "properties": map[string]any{
				"server": map[string]any{"type": "string"}, "tool": map[string]any{"type": "string"},
				"arguments": map[string]any{"type": "object", "additionalProperties": true},
			}}},
	}
}

func (p gatewayDiscovery) Call(ctx context.Context, sc officetools.Scope, name string, raw json.RawMessage) (string, bool) {
	if len(p.Tools(sc)) == 0 {
		return "lượt này không được dùng toolbox MCP", true
	}
	var in struct {
		Query     string          `json:"query"`
		Offset    int             `json:"offset"`
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return "tham số không hợp lệ", true
	}
	c := gatewayCaller(ctx, p.st, sc, gatewayClient(sc))
	switch name {
	case "find_tool":
		out, err := p.gw.FindTools(ctx, c, in.Query, in.Offset)
		if err != nil {
			return err.Error(), true
		}
		data, err := json.Marshal(out)
		if err != nil {
			return err.Error(), true
		}
		return string(data), false
	case "call_tool":
		if in.Server == "" || in.Tool == "" || len(in.Arguments) == 0 || in.Arguments[0] != '{' {
			return "cần server, tool và arguments dạng object từ find_tool", true
		}
		return p.gw.InvokeFound(ctx, c, in.Server, in.Tool, in.Arguments)
	default:
		return "không có tool " + name, true
	}
}

// CallMCP retains native image/resource blocks for Codex and Gemini. The API
// runners use Call's existing text representation instead.
func (p gatewayDiscovery) CallMCP(ctx context.Context, sc officetools.Scope, name string, raw json.RawMessage) map[string]any {
	textResult := func(text string, failed bool) map[string]any {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": failed}
	}
	if name != "call_tool" || len(p.Tools(sc)) == 0 {
		text, failed := p.Call(ctx, sc, name, raw)
		return textResult(text, failed)
	}
	var in struct {
		Server    string          `json:"server"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Server == "" || in.Tool == "" || len(in.Arguments) == 0 || in.Arguments[0] != '{' {
		return textResult("cần server, tool và arguments dạng object từ find_tool", true)
	}
	result := p.gw.InvokeFoundResult(ctx, gatewayCaller(ctx, p.st, sc, gatewayClient(sc)), in.Server, in.Tool, in.Arguments)
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.UseNumber() // preserve large integers in structuredContent and resources
	if decoder.Decode(&out) != nil || out == nil {
		return textResult("MCP trả kết quả không hợp lệ", true)
	}
	return out
}

// The runner kind comes from the run's bound scope, not tool arguments.
func gatewayClient(sc officetools.Scope) string {
	switch sc.ClientKind {
	case "claude", "codex", "gemini", "antigravity":
		return sc.ClientKind
	default:
		return "api"
	}
}

// gatewayRest is the office tool mcp_result (ADR-183): the part of a long
// gateway result office cut, for runs whose agent gets a gateway server.
type gatewayRest struct {
	st storage.Store
	gw *mcpgateway.Gateway
}

func (p gatewayRest) Tools(sc officetools.Scope) []officetools.Tool {
	ctx := context.Background()
	if len(p.gw.NamesFor(ctx, gatewayCaller(ctx, p.st, sc, "api"))) == 0 {
		return nil
	}
	return []officetools.Tool{{Name: "mcp_result", ReadOnly: true,
		Description: "Read the part of a long MCP tool result that office cut (the result's note gives id and offset).",
		Schema: map[string]any{"type": "object", "required": []string{"id"}, "properties": map[string]any{
			"id":     map[string]any{"type": "string"},
			"offset": map[string]any{"type": "integer", "description": "Byte offset to start from"},
			"limit":  map[string]any{"type": "integer", "description": "Bytes to read (max 30000)"},
		}}}}
}

func (p gatewayRest) Call(_ context.Context, sc officetools.Scope, _ string, raw json.RawMessage) (string, bool) {
	var in struct {
		ID     string `json:"id"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &in) != nil {
		return "tham số không hợp lệ", true
	}
	out, err := p.gw.Rest(in.ID, sc.ConversationID, sc.JobID, in.Offset, in.Limit)
	if err != nil {
		return err.Error(), true
	}
	return out, false
}
