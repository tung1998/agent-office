package chat

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
// check adds the quick check after each edit (ADR-133), with the project's
// own checks (ADR-135).
func guardSettings(userMCP bool, gateway []string, check bool, checks string) string {
	cmd := GuardCommand
	if userMCP {
		cmd += " --user-mcp"
	}
	for _, n := range gateway {
		cmd += " --mcp " + n
	}
	h := []map[string]any{{"type": "command", "command": cmd}}
	hooks := map[string]any{"PreToolUse": []map[string]any{
		{"matcher": "Write|Edit|MultiEdit|NotebookEdit", "hooks": h},
		{"matcher": "mcp__.*", "hooks": h},
	}}
	if check && CheckCommand != "" {
		hooks["PostToolUse"] = []map[string]any{checkSettings(checks)}
	}
	b, _ := json.Marshal(map[string]any{"hooks": hooks})
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

// ForegroundCommand runs `office hook foreground`: set by the office binary at start.
var ForegroundCommand string

// headlessDisallowed: tools that wait for later (a monitor, a wakeup, a cron
// of the session); a turn is one `claude -p`, nothing calls the agent back.
const headlessDisallowed = "Monitor ScheduleWakeup CronCreate CronDelete CronList"

// foregroundSettings: Bash, subagents and file tools go through `office hook
// foreground` (runs with every built-in tool: the administrator's, ADR-059;
// file tools for the home's secrets, ADR-227).
func foregroundSettings() string {
	b, _ := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []map[string]any{
		{"matcher": "Bash|Agent|Task|Read|Grep|Glob|Edit|Write|MultiEdit|NotebookEdit", "hooks": []map[string]any{{"type": "command", "command": ForegroundCommand}}},
	}}})
	return string(b)
}

// Foreground is a PreToolUse hook: no run_in_background. A turn is one
// `claude -p`; when the agent answers, the CLI exits, what runs in the
// background is killed and no notification ever wakes the agent, so "I'll
// wait for it" would end the turn with nothing waiting.
// It also keeps an administrator's shell off master/main (ADR-188): a push
// there goes through propose_action git_push, a person approves. And off the
// home's keys and tokens, and from sending data out (ADR-227): a prompt
// injection in what the agent reads must not reach them.
func Foreground(in io.Reader, out io.Writer) {
	var ev struct {
		Cwd       string         `json:"cwd"`
		ToolInput map[string]any `json:"tool_input"`
	}
	if json.NewDecoder(in).Decode(&ev) != nil {
		return
	}
	for _, k := range []string{"file_path", "notebook_path", "path"} {
		if p, _ := ev.ToolInput[k].(string); p != "" {
			if why := secretPath(p, ev.Cwd); why != "" {
				deny(out, "agent-office: "+why)
				return
			}
		}
	}
	if cmd, _ := ev.ToolInput["command"].(string); cmd != "" {
		if why := secretInCommand(cmd); why != "" {
			deny(out, "agent-office: "+why)
			return
		}
		if why := exfiltrates(cmd); why != "" {
			deny(out, "agent-office: không gửi dữ liệu của máy ra ngoài ("+why+"). Cần thật thì nhờ người chạy")
			return
		}
	}
	if bg, _ := ev.ToolInput["run_in_background"].(bool); bg {
		deny(out, "agent-office: không chạy nền được — lượt chạy kết thúc khi bạn trả lời, việc chạy nền bị dừng và không có thông báo nào gọi lại bạn. Chạy ở tiền cảnh (Bash timeout tối đa 600000 ms) và đợi kết quả ngay trong lượt này; việc lâu hơn thì dùng propose_action (tiến trình) hoặc propose_automation")
		return
	}
	if cmd, _ := ev.ToolInput["command"].(string); cmd != "" && pushesProtected(cmd, ev.Cwd, currentBranch) {
		deny(out, "agent-office: không được push lên master/main. Push nhánh khác, hoặc đề xuất bằng propose_action git_push để người duyệt")
	}
}

// protectedBranches: no agent pushes there itself (ADR-188).
var protectedBranches = []string{"master", "main"}

// pushesProtected: a shell command with a git push that may land on
// master/main: a refspec naming one, --all/--mirror, or no refspec while
// the branch (or what it tracks) is one. Best effort on the text: unsure
// counts as protected.
func pushesProtected(cmd, cwd string, branchOf func(dir string) (branch, upstream string)) bool {
	split := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "(", "\n", ")", "\n", "`", "\n", "$(", "\n")
	for _, seg := range strings.Split(split.Replace(cmd), "\n") {
		f := strings.Fields(seg)
		dir := cwd
		i := 0 // the command itself: past VAR=x, sudo, env, command
		for i < len(f) && (strings.Contains(f[i], "=") || f[i] == "sudo" || f[i] == "env" || f[i] == "command" || f[i] == "exec") {
			i++
		}
		if i >= len(f) || filepath.Base(f[i]) != "git" {
			continue
		}
		j := i + 1
		for j < len(f) && strings.HasPrefix(f[j], "-") { // git -C dir, -c k=v
			if (f[j] == "-C" || f[j] == "-c") && j+1 < len(f) {
				if f[j] == "-C" {
					dir = f[j+1]
					if !filepath.IsAbs(dir) {
						dir = filepath.Join(cwd, dir)
					}
				}
				j++
			}
			j++
		}
		if j >= len(f) || f[j] != "push" {
			continue
		}
		var rest []string
		for _, a := range f[j+1:] {
			if a == "--all" || a == "--mirror" {
				return true
			}
			if !strings.HasPrefix(a, "-") {
				rest = append(rest, strings.Trim(a, `"'`))
			}
		}
		if len(rest) > 1 { // remote, then refspecs
			for _, r := range rest[1:] {
				_, dst, ok := strings.Cut(r, ":")
				if !ok {
					dst = r
				}
				dst = strings.TrimPrefix(strings.TrimPrefix(dst, "+"), "refs/heads/")
				if dst == "HEAD" || dst == "@" {
					dst, _ = branchOf(dir)
				}
				if dst == "" || slices.Contains(protectedBranches, dst) {
					return true
				}
			}
			continue
		}
		branch, upstream := branchOf(dir)
		_, up, _ := strings.Cut(upstream, "/")
		if branch == "" || slices.Contains(protectedBranches, branch) || slices.Contains(protectedBranches, up) {
			return true
		}
	}
	return false
}

// currentBranch: the branch checked out in dir and what it tracks.
func currentBranch(dir string) (string, string) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", ""
	}
	up, _ := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "@{upstream}").Output()
	return strings.TrimSpace(string(out)), strings.TrimSpace(string(up))
}
