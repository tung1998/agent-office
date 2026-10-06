package chat

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tung1998/agent-office/internal/mcpserver"
)

// GuardCommand runs `office hook guard`: set by the office binary at start.
// Empty (tests, other hosts): a chat that edits runs under dontAsk, which
// Claude Code does not let write in .claude/.
var GuardCommand string

// guardSettings: under bypassPermissions (so an agent may add a skill in
// .claude/skills) every file edit and MCP call goes through `office hook guard`.
// gateway: the office's MCP servers the run gets (ADR-091), allowed like
// office's own (their names are [a-z0-9-], safe on the command line).
func guardSettings(userMCP bool, gateway []string) string {
	cmd := GuardCommand
	if userMCP {
		cmd += " --user-mcp"
	}
	for _, n := range gateway {
		cmd += " --mcp " + n
	}
	h := []map[string]any{{"type": "command", "command": cmd}}
	b, _ := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []map[string]any{
		{"matcher": "Write|Edit|MultiEdit|NotebookEdit", "hooks": h},
		{"matcher": "mcp__.*", "hooks": h},
	}}})
	return string(b)
}

// Guard is a PreToolUse hook: an edit stays in the working folder (the
// worktree or the project), an MCP tool is office's own unless the agent may
// use the person's servers or it is one of gateway (the office's MCP servers
// the run got, ADR-091). Anything else it lets through.
func Guard(in io.Reader, out io.Writer, userMCP bool, gateway []string) {
	var ev struct {
		Cwd       string         `json:"cwd"`
		ToolName  string         `json:"tool_name"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if err := json.NewDecoder(in).Decode(&ev); err != nil {
		deny(out, "agent-office: không đọc được yêu cầu")
		return
	}
	if strings.HasPrefix(ev.ToolName, "mcp__") {
		if userMCP {
			return
		}
		for _, n := range append([]string{mcpserver.ServerName}, gateway...) {
			if strings.HasPrefix(ev.ToolName, "mcp__"+n+"__") {
				return
			}
		}
		deny(out, "agent-office: agent này không được dùng MCP của người dùng")
		return
	}
	p, _ := ev.ToolInput["file_path"].(string)
	if p == "" {
		p, _ = ev.ToolInput["notebook_path"].(string)
	}
	if p == "" || ev.Cwd == "" || strings.HasPrefix(p, "~") { // unclear: refused, never let through
		deny(out, "agent-office: không rõ file cần sửa")
		return
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(ev.Cwd, p)
	}
	root, target := realPath(ev.Cwd), realPath(p)
	if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 { // a link to what is not there yet
		if to, err := os.Readlink(target); err == nil {
			if !filepath.IsAbs(to) {
				to = filepath.Join(filepath.Dir(target), to)
			}
			target = realPath(to)
		}
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		deny(out, "agent-office: chỉ sửa file trong thư mục project")
		return
	}
	if why := protected(filepath.ToSlash(rel)); why != "" {
		deny(out, "agent-office: "+why)
	}
}

// protected: files that would let an agent run commands on the machine (its
// own Claude Code settings and hooks, MCP servers, git hooks). Skills and
// subagents in .claude/ are the ones it may write.
func protected(rel string) string {
	switch {
	case rel == ".mcp.json":
		return "không sửa .mcp.json (máy chủ MCP chạy lệnh trên máy)"
	case rel == ".git" || strings.HasPrefix(rel, ".git/"):
		return "không sửa .git"
	case strings.HasPrefix(rel, ".claude/skills/"), strings.HasPrefix(rel, ".claude/agents/"):
		return ""
	case rel == ".claude" || strings.HasPrefix(rel, ".claude/"):
		return "trong .claude/ chỉ sửa skills và agents (không sửa settings, hooks)"
	}
	return ""
}

// realPath resolves symlinks of the part that exists (a new file's folder may not).
func realPath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

func deny(out io.Writer, reason string) {
	_ = json.NewEncoder(out).Encode(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason,
	}})
}
