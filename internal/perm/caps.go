package perm

import (
	"slices"

	"github.com/tung1998/agent-office/internal/storage"
)

// Capabilities: what an agent may do. A package (level) is a preset of them;
// an agent may pick them one by one instead. Each has the lowest level that
// includes it, so a chat/task mode or the project's cap below that level
// switches it off for the run.
const (
	CapPropose   = "propose"         // propose diffs and operations, a person approves
	CapCommands  = "commands.run"    // run the chosen commands on its own
	CapApply     = "code.apply"      // apply clean diffs on its own
	CapCommit    = "git.commit"      // commit on its own
	CapBranch    = "git.branch"      // create a branch on its own
	CapProcess   = "ops.process"     // run/restart/stop allowed processes on its own
	CapContainer = "ops.container"   // start/restart/stop allowed containers on its own
	CapUserMCP   = "tools.mcp"       // use the MCP servers of the person's own Claude Code setup
	CapMCPWrite  = "tools.mcp.write" // call office MCP tools that write without asking (ADR-093)
	CapChatSend  = "chat.send"       // send to another chat in the person's name without asking
)

// Cap describes a capability for the dashboard.
type Cap struct {
	ID          string `json:"id"`
	Group       string `json:"group"` // code | commands | git | ops
	Label       string `json:"label"`
	Description string `json:"description"`
	Min         string `json:"min"` // lowest level that includes it
}

// Caps lists every capability, grouped, lowest level first within a group.
var Caps = []Cap{
	{CapPropose, "code", "Đề xuất", "Đưa diff và đề xuất thao tác, người duyệt mới làm", Propose},
	{CapApply, "code", "Tự áp diff", "Diff áp được sạch được áp ngay, trừ file cấm", Edit},
	{CapCommands, "commands", "Tự chạy lệnh", "Chạy ngay các lệnh được chọn; lệnh khác phải đề xuất", Check},
	{CapUserMCP, "commands", "Dùng MCP của bạn", "Dùng các MCP/connector trong Claude Code của bạn (Graylog, Jira, Discord…); là công cụ nên có ở mọi mức", Read},
	{CapMCPWrite, "commands", "Tự gọi tool MCP có ghi", "Gọi ngay tool có ghi của MCP qua office; không có thì mỗi lần gọi thành đề xuất chờ duyệt", Operate},
	{CapChatSend, "commands", "Tự gửi tin sang chat khác", "Gửi ngay tin vào cuộc chat khác dưới tên người đang chat; không có thì mỗi lần gửi thành đề xuất chờ duyệt", Operate},
	{CapCommit, "git", "Tự commit", "Commit các file đã sửa với message rõ ràng", Edit},
	{CapBranch, "git", "Tự tạo nhánh", "Tạo và chuyển sang nhánh mới", Operate},
	{CapProcess, "ops", "Tự chạy lại tiến trình", "Chạy, chạy lại, dừng tiến trình được phép", Operate},
	{CapContainer, "ops", "Tự điều khiển container", "Bật, chạy lại, dừng container được phép", Operate},
}

func capInfo(id string) (Cap, bool) {
	for _, c := range Caps {
		if c.ID == id {
			return c, true
		}
	}
	return Cap{}, false
}

// Preset is the capabilities of a package.
func Preset(level string) []string {
	out := []string{}
	for _, c := range Caps {
		if Rank(c.Min) <= Rank(level) {
			out = append(out, c.ID)
		}
	}
	return out
}

// agentCaps: the agent's own picks, or its package's preset.
func agentCaps(a storage.Agent) []string {
	if a.Permissions.Caps != nil {
		return *a.Permissions.Caps
	}
	return Preset(Agent(a))
}

// Access is what an agent may do in one run.
type Access struct {
	Level    string   `json:"level"`
	Caps     []string `json:"caps"`
	Commands []string `json:"commands"` // command patterns it may run: safe ones from read, others per Can/worktree
	Safe     []string `json:"safe"`     // those of Commands it runs on its own at any level
	// process ids and compose services it may run/restart on its own (with the capability)
	Processes  []string `json:"processes"`
	Containers []string `json:"containers"`
}

// Can reports a capability.
func (a Access) Can(capID string) bool { return slices.Contains(a.Caps, capID) }

// Resolve works out an agent's access under a mode and the project's policy.
func Resolve(a storage.Agent, mode string, p Policy) Access {
	level := Effective(a, mode, p)
	acc := Access{Level: level, Caps: []string{}, Commands: []string{}, Safe: []string{}}
	for _, id := range agentCaps(a) {
		if c, ok := capInfo(id); ok && Rank(c.Min) <= Rank(level) && !acc.Can(id) {
			acc.Caps = append(acc.Caps, id)
		}
	}
	// the agent's picks from the project's catalog; by default its safe commands
	picks := p.Safe
	if a.Permissions.Commands != nil {
		picks = *a.Permissions.Commands
	}
	for _, c := range p.Catalog {
		if slices.Contains(picks, c) {
			acc.Commands = append(acc.Commands, c)
			if slices.Contains(p.Safe, c) {
				acc.Safe = append(acc.Safe, c)
			}
		}
	}
	acc.Processes, acc.Containers = p.Jobs, []string{}
	if a.Permissions.Processes != nil {
		acc.Processes = *a.Permissions.Processes
	}
	if a.Permissions.Containers != nil {
		acc.Containers = *a.Permissions.Containers
	}
	return acc
}
