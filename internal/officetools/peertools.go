package officetools

import (
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/perm"
)

// peerReadTools: what an office a project is shared with may read there
// through other_office call (ADR-212): its settings, board, data and report,
// as a pack of its config would show.
var peerReadTools = []string{"describe", "list", "get", "project_report", "burn_quests", "data_collections", "data_query", "recall"}

// peerAdminTools: what an office made administrator here may also read and
// change, at once and in the name of the admin who paired (it may already
// run the assistant with full access to this machine).
var peerAdminTools = []string{"git_status", "git_log", "git_diff", "process_logs", "ops_overview", "monitor_detail",
	"pending_requests", "decide_request", "propose_change", "propose_automation", "burn_quest", "burn_assign", "burn_drop", "data_upsert", "data_delete", "remember", "forget"}

// PeerAgentTools act on one agent's notes: the call names the agent.
var PeerAgentTools = []string{"recall", "remember", "forget"}

// PeerScope is the scope another office's call runs in on projectID: read
// only, or as administrator for admin (an email) when it was made one.
func PeerScope(projectID, office, admin string) Scope {
	sc := Scope{ProjectID: projectID, Agent: "office " + office, Level: perm.Read}
	if admin != "" {
		sc.Level, sc.Admin, sc.AdminBy = perm.Operate, true, strings.TrimPrefix(admin, "human:")
	}
	return sc
}

// PeerTools lists the tools another office may call in sc (see PeerScope).
func (t *Toolbox) PeerTools(sc Scope) []Tool {
	var out []Tool
	for _, x := range t.ToolsFor(sc) {
		if slices.Contains(peerReadTools, x.Name) || sc.Admin && slices.Contains(peerAdminTools, x.Name) {
			if slices.Contains(PeerAgentTools, x.Name) {
				x = withAgent(x)
			}
			out = append(out, x)
		}
	}
	return out
}

// withAgent gives a tool on an agent's notes an "agent" argument: the agent
// of that project whose notes it reads or writes.
func withAgent(x Tool) Tool {
	props, _ := x.Schema["properties"].(map[string]any)
	schema := make(map[string]any, len(x.Schema))
	for k, v := range x.Schema {
		schema[k] = v
	}
	np := make(map[string]any, len(props)+1)
	for k, v := range props {
		np[k] = v
	}
	np["agent"] = map[string]any{"type": "string", "description": "The agent whose notes (its name, see list agent)"}
	schema["properties"] = np
	req, _ := x.Schema["required"].([]string)
	schema["required"] = append(slices.Clone(req), "agent")
	x.Schema = schema
	return x
}
