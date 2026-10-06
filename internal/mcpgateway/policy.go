package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Caller is who calls through the gateway (ADR-093): an agent's run or a
// person's own CLI.
type Caller struct {
	Kind      string // claude | codex | api | person
	Agent     string // agent name ("" for a person)
	AgentID   string // "" when unknown (it then gets only servers given to every agent)
	ProjectID string
	// where a proposal goes, and the run it came from
	ConversationID, TaskID, JobID string
	// CanWrite: tools that write run without asking (perm.CapMCPWrite).
	// CanPropose: otherwise they become a proposal a person approves.
	CanWrite, CanPropose bool
	// Scope is the run's own scope, handed back to Propose.
	Scope any
}

// AssistantID stands for the office assistant in a server's agent list.
const AssistantID = "assistant"

// Person reports a person calling from their own CLI.
func (c Caller) Person() bool { return c.Kind == "person" }

// Assigned reports whether a caller gets m: every agent when m names none,
// else only the agents it names. A person's own CLI gets every server.
func Assigned(m storage.MCPServer, c Caller) bool {
	if c.Person() || len(m.Agents) == 0 {
		return true
	}
	return c.AgentID != "" && slices.Contains(m.Agents, c.AgentID)
}

// ToolReadOnly reports whether m's tool only reads: it said so itself
// (readOnlyHint), or a person trusts it.
func ToolReadOnly(m storage.MCPServer, tool string) (readOnly, trusted bool) {
	trusted = slices.Contains(m.TrustedTools, tool)
	for _, t := range m.LastTools {
		if t.Name == tool {
			return t.ReadOnly != nil && *t.ReadOnly, trusted
		}
	}
	return false, trusted // unknown: counts as writing
}

type verdict int

const (
	pass verdict = iota
	propose
	deny
)

// decide says what happens to a call of tool by c. A person's CLI is no
// exception: their token's rights (CanWrite for an admin) decide, so a member
// never writes with the servers' credentials without an approval.
func decide(m storage.MCPServer, c Caller, tool string, canPropose bool) verdict {
	if ro, trusted := ToolReadOnly(m, tool); ro || trusted || c.CanWrite {
		return pass
	}
	if c.CanPropose && canPropose {
		return propose
	}
	return deny
}

// Proposal is what became of a call that needs a person.
type Proposal struct {
	ActionID string
	Status   string // pending | done | failed (a bot's chat in direct mode decides at once)
	Detail   string // the result when done, the error when failed
}

// Proposer turns a call into a proposal (the actions service, ADR-093).
type Proposer func(ctx context.Context, c Caller, server, tool string, args json.RawMessage) (Proposal, error)

// proposalText is the tool result the agent gets for a call that waits.
func proposalText(m storage.MCPServer, tool string, p Proposal) (string, bool) {
	switch p.Status {
	case "done":
		return p.Detail, false
	case "failed":
		return p.Detail, true
	}
	return fmt.Sprintf("Tool %s của MCP %s có ghi dữ liệu nên office đã tạo đề xuất (mã %s) chờ người dùng duyệt. Chưa chạy gì; hãy báo người dùng, duyệt xong office sẽ chạy và gửi kết quả.", tool, m.Name, p.ActionID), false
}

func denyText(m storage.MCPServer, tool string) string {
	return fmt.Sprintf("Tool %s của MCP %s có ghi dữ liệu; lượt này chỉ được đọc nên không gọi được. Nhờ người dùng nâng quyền hoặc đánh dấu tool là tin cậy trên dashboard.", tool, m.Name)
}

// toolResult is an MCP tools/call result carrying text.
func toolResult(text string, isErr bool) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr})
	return b
}

// gateCall applies the policy to one tools/call request of a run. ok means
// forward it; otherwise reply is the answer to send back.
func (g *Gateway) gateCall(ctx context.Context, m storage.MCPServer, c Caller, id json.RawMessage, tool string, args json.RawMessage) (reply json.RawMessage, ok bool) {
	switch decide(m, c, tool, g.Propose != nil) {
	case pass:
		return nil, true
	case deny:
		g.logCall(ctx, m, c, tool, "denied", "", 0, "")
		return rpcResult(id, toolResult(denyText(m, tool), true)), false
	}
	p, err := g.Propose(ctx, c, m.Name, tool, args)
	if err != nil {
		g.logCall(ctx, m, c, tool, "error", err.Error(), 0, "")
		return rpcResult(id, toolResult("office không tạo được đề xuất: "+err.Error(), true)), false
	}
	g.logCall(ctx, m, c, tool, "proposed", "", 0, p.ActionID)
	text, isErr := proposalText(m, tool, p)
	return rpcResult(id, toolResult(text, isErr)), false
}

// logCall records one call (best effort; a log failure never fails a call).
func (g *Gateway) logCall(ctx context.Context, m storage.MCPServer, c Caller, tool, status, errMsg string, ms int64, actionID string) {
	if g.Store == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	_ = g.Store.MCPCalls().Add(ctx, storage.MCPCall{ServerID: m.ID, ServerName: m.Name, Tool: tool,
		Caller: c.Agent, CallerKind: c.Kind, ProjectID: c.ProjectID, ConversationID: c.ConversationID, JobID: c.JobID, ActionID: actionID,
		Status: status, Error: firstLine(errMsg, 300), DurationMS: ms})
	if g.logged.Add(1)%500 == 1 { // now and then, the log keeps its last CallLogDays
		_, _ = g.Store.MCPCalls().Prune(ctx, time.Now().UTC().AddDate(0, 0, -CallLogDays))
	}
}

// CallLogDays is how long the call log keeps a call.
const CallLogDays = 30

// callOf reads a single tools/call request: its id, tool and arguments.
func callOf(body []byte) (id json.RawMessage, tool string, args json.RawMessage, ok bool) {
	var one struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}
	t := bytes.TrimSpace(body)
	if len(t) == 0 || t[0] != '{' || json.Unmarshal(t, &one) != nil || one.Method != "tools/call" {
		return nil, "", nil, false
	}
	return one.ID, one.Params.Name, one.Params.Arguments, true
}

// batchCalls reports whether a batch carries a tools/call.
func batchCalls(body []byte) bool {
	t := bytes.TrimSpace(body)
	if len(t) == 0 || t[0] != '[' {
		return false
	}
	var batch []struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(t, &batch) != nil {
		return false
	}
	return slices.ContainsFunc(batch, func(x struct {
		Method string `json:"method"`
	}) bool {
		return x.Method == "tools/call"
	})
}

// CallTool calls one tool of the server named server, as office (an API
// run, or a call a person approved). It applies no policy: callers do.
func (g *Gateway) CallTool(ctx context.Context, server, tool string, args json.RawMessage) (string, bool, error) {
	m, err := g.Store.MCPServers().GetByName(ctx, server)
	if err != nil || !m.Enabled {
		return "", true, fmt.Errorf("office không có MCP %s đang bật", server)
	}
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	var raw json.RawMessage
	if m.Kind == "stdio" {
		p, perr := g.stdio(ctx, m)
		if perr != nil {
			return "", true, perr
		}
		raw, err = p.call(ctx, "tools/call", toolParams(tool, args))
	} else {
		headers, herr := OpenMap(g.Box, m.HeadersEnc)
		if herr != nil {
			return "", true, errors.New("không mở được bí mật đã lưu: nhập lại header")
		}
		tok, berr := g.bearer(ctx, &m, false)
		if berr != nil {
			return "", true, berr
		}
		if tok != "" {
			headers["Authorization"] = "Bearer " + tok
		}
		raw, err = callHTTP(ctx, g.client(), m.URL, headers, tool, args)
		var se *StatusError
		if errors.As(err, &se) && se.Code == 401 && tok != "" {
			if tok, berr = g.bearer(ctx, &m, true); berr != nil {
				return "", true, berr
			}
			headers["Authorization"] = "Bearer " + tok
			raw, err = callHTTP(ctx, g.client(), m.URL, headers, tool, args)
		}
	}
	if err != nil {
		return "", true, err
	}
	text, isErr := resultText(raw)
	return text, isErr, nil
}

// resultText flattens a tools/call result into text: its text parts, and a
// note for anything else (images, resources).
func resultText(raw json.RawMessage) (string, bool) {
	var res struct {
		Content []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			MimeType string          `json:"mimeType"`
			Resource json.RawMessage `json:"resource"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return string(raw), false
	}
	var parts []string
	for _, c := range res.Content {
		switch c.Type {
		case "text":
			parts = append(parts, c.Text)
		case "resource":
			parts = append(parts, string(c.Resource))
		default:
			parts = append(parts, "["+c.Type+" "+c.MimeType+"]")
		}
	}
	if len(parts) == 0 && len(res.StructuredContent) > 0 {
		parts = append(parts, string(res.StructuredContent))
	}
	return strings.Join(parts, "\n"), res.IsError
}

// Invoke is a call of an API run: the same policy as a call through
// /mcp/s/<name>, then office calls the tool itself. The text is what the
// model gets back.
func (g *Gateway) Invoke(ctx context.Context, c Caller, server, tool string, args json.RawMessage) (string, bool) {
	m, err := g.Store.MCPServers().GetByName(ctx, server)
	if err != nil || !m.Enabled || !Assigned(m, c) {
		return "office không có MCP " + server + " cho agent này", true
	}
	switch decide(m, c, tool, g.Propose != nil) {
	case deny:
		g.logCall(ctx, m, c, tool, "denied", "", 0, "")
		return denyText(m, tool), true
	case propose:
		p, err := g.Propose(ctx, c, m.Name, tool, args)
		if err != nil {
			g.logCall(ctx, m, c, tool, "error", err.Error(), 0, "")
			return "office không tạo được đề xuất: " + err.Error(), true
		}
		g.logCall(ctx, m, c, tool, "proposed", "", 0, p.ActionID)
		return proposalText(m, tool, p)
	}
	start := time.Now()
	text, isErr, err := g.CallTool(ctx, server, tool, args)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		g.logCall(ctx, m, c, tool, "error", err.Error(), ms, "")
		return "MCP " + server + ": " + err.Error(), true
	}
	status, msg := "ok", ""
	if isErr {
		status, msg = "error", text
	}
	g.logCall(ctx, m, c, tool, status, msg, ms, "")
	return text, isErr
}

// APITool is one gateway tool offered to an API run, named as Claude Code
// names it (mcp__<server>__<tool>).
type APITool struct {
	Name, Server, Tool, Description string
	Schema                          json.RawMessage
}

// apiName keeps to what model APIs take as a tool name.
func apiName(server, tool string) (string, bool) {
	n := "mcp__" + server + "__" + tool
	if len(n) > 64 {
		return "", false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "", false
		}
	}
	return n, true
}

// APITools lists the tools of the servers c gets (from their last check).
func (g *Gateway) APITools(ctx context.Context, c Caller) []APITool {
	list, err := g.Store.MCPServers().List(ctx)
	if err != nil {
		return nil
	}
	var out []APITool
	for _, m := range list {
		if !m.Enabled || m.Scope != "machine" || !Assigned(m, c) {
			continue
		}
		for _, t := range m.LastTools {
			name, ok := apiName(m.Name, t.Name)
			if !ok {
				continue
			}
			schema := t.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			out = append(out, APITool{Name: name, Server: m.Name, Tool: t.Name, Description: t.Description, Schema: schema})
		}
	}
	return out
}
