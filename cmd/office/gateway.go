package main

import (
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
	model, err := st.OrgModels().GetForRepo(ctx, projectID)
	if err != nil {
		return ""
	}
	agents, err := st.Agents().List(ctx, model.ID)
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
		if kind != "codex" {
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
// and Codex, tools for an API run.
func gatewayFor(st storage.Store, gw *mcpgateway.Gateway) chat.GatewayFor {
	return func(ctx context.Context, sc officetools.Scope) ([]string, chat.GatewayTools) {
		c := gatewayCaller(ctx, st, sc, "api")
		return gw.NamesFor(ctx, c), &apiGateway{gw: gw, caller: c}
	}
}

// apiGateway is the gateway's tools for one API run.
type apiGateway struct {
	gw     *mcpgateway.Gateway
	caller mcpgateway.Caller
	tools  map[string]mcpgateway.APITool
}

func (a *apiGateway) List(ctx context.Context) []chat.GatewayTool {
	list := a.gw.APITools(ctx, a.caller)
	a.tools = map[string]mcpgateway.APITool{}
	out := make([]chat.GatewayTool, 0, len(list))
	for _, t := range list {
		a.tools[t.Name] = t
		out = append(out, chat.GatewayTool{Name: t.Name, Description: t.Description, Schema: t.Schema})
	}
	return out
}

func (a *apiGateway) Call(ctx context.Context, name string, args json.RawMessage) (string, bool) {
	t, ok := a.tools[name]
	if !ok {
		return "không có tool " + name, true
	}
	return a.gw.Invoke(ctx, a.caller, t.Server, t.Tool, args)
}
