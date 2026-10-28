package chat

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/proctrack"
	"github.com/tung1998/agent-office/internal/storage"
)

// claudeRunner drives Claude Code headless with read-only built-in tools
// (plus Edit/Write when the run may edit its folder).
// By default it loads the same setup as the user's own CLI (user, project and
// local settings, MCP servers, plugins, skills), so MCP tools the user allowed
// there work here too; anything that would prompt is denied. Sessions are
// resumed across turns.
type claudeRunner struct{}

var claudeReadTools = []string{"Read", "Glob", "Grep"}

// userMCPSettings: a PreToolUse hook that allows the tools of any MCP server
// (the person's own Claude Code setup), for agents with perm.CapUserMCP.
const userMCPSettings = `{"hooks":{"PreToolUse":[{"matcher":"mcp__.*","hooks":[{"type":"command","command":"printf '%s' '{\"hookSpecificOutput\":{\"hookEventName\":\"PreToolUse\",\"permissionDecision\":\"allow\",\"permissionDecisionReason\":\"agent-office: an MCP of the person\"}}'"}]}]}}`

// withEffort asks Claude Code to think this hard (--effort: low … max).
func withEffort(a []string, effort string) []string {
	if effort == "" {
		return a
	}
	return append(a, "--effort", effort)
}

func (claudeRunner) args(req RunRequest, resume bool) []string {
	if req.NoTools { // answered from the conversation only
		a := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--permission-mode", "dontAsk",
			"--setting-sources", "user,project,local", "--tools", "",
			"--strict-mcp-config"} // and no MCP: the person's own servers (their Jira…) would load from their settings
		if req.Model != "" {
			a = append(a, "--model", req.Model)
		}
		a = withEffort(a, req.Effort)
		if req.System != "" {
			a = append(a, "--append-system-prompt", req.System)
		}
		if resume && req.SessionID != "" {
			a = append(a, "--resume", req.SessionID)
		}
		return a
	}
	if req.FullAccess { // the office assistant as administrator (ADR-059)
		a := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--permission-mode", "bypassPermissions",
			"--setting-sources", "user,project,local"}
		if req.Model != "" {
			a = append(a, "--model", req.Model)
		}
		a = withEffort(a, req.Effort)
		if req.System != "" {
			a = append(a, "--append-system-prompt", req.System)
		}
		if resume && req.SessionID != "" {
			a = append(a, "--resume", req.SessionID)
		}
		// nothing wakes the agent after its turn: no monitors, wakeups or
		// background runs (they would be killed when the CLI exits)
		disallow := headlessDisallowed
		if req.NoSubagents {
			disallow += " Agent Task"
		}
		a = append(a, "--disallowedTools", disallow)
		if ForegroundCommand != "" {
			a = append(a, "--settings", foregroundSettings())
		}
		return a
	}
	tools := append(slices.Clone(claudeReadTools), "Skill")
	if req.Web { // ADR-192
		tools = append(tools, "WebSearch", "WebFetch")
	}
	// dontAsk: whatever is not allowed is denied, whatever the user's defaultMode
	a := []string{"-p", "--output-format", "stream-json", "--verbose", "--include-partial-messages", "--permission-mode", "dontAsk",
		"--setting-sources", "user,project,local"}
	deny := []string{"Read(./.env)", "Read(./.env.*)", "Read(**/.env)", "Read(**/*.pem)", "Read(**/*.key)"}
	deny = append(deny, homeSecretDenies()...) // ADR-227
	if req.Write {
		// edits stay in the working folder (dontAsk refuses the rest), never
		// secrets or the project's protected files
		tools = append(slices.Clone(tools), "Edit", "Write")
		for _, p := range append([]string{".env", ".env.*", "**/.env", "**/*.pem", "**/*.key", ".git/**"}, req.DenyPaths...) {
			p = strings.TrimSpace(p)
			if p == "" || strings.ContainsAny(p, " ()") {
				continue
			}
			if !strings.HasPrefix(p, "**/") && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "./") {
				p = "./" + p
			}
			if strings.HasSuffix(p, "/") {
				p += "**"
			}
			deny = append(deny, "Edit("+p+")", "Write("+p+")")
		}
	}
	// extra read dirs (ADR-074): without full access, read only — Edit/Write
	// there are denied even though --add-dir below lets the run read them.
	for _, d := range req.ExtraDirs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		// a single leading "/" is relative to the settings source (the working
		// directory), not the filesystem root, in Claude Code's permission
		// rules; an absolute path needs a second leading "/" (code.claude.com/docs/en/permissions)
		deny = append(deny, "Edit(/"+d+"/**)", "Write(/"+d+"/**)")
	}
	if req.Write && GuardCommand != "" {
		// Claude Code refuses writes in .claude/ under dontAsk: bypass, with the
		// office's hook keeping edits in the folder (and MCP as before); the deny
		// rules below still hold, and --tools still bounds what exists
		a[slices.Index(a, "dontAsk")] = "bypassPermissions"
		var gw []string
		if req.Office != nil {
			gw = slices.Clone(req.Office.Gateway)
			if req.Office.Other { // other_office only when the run has it (ADR-169)
				gw = append(gw, officetools.OtherServer)
			}
		}
		a = append(a, "--settings", guardSettings(req.UserMCP, gw, req.QuickCheck, req.QuickChecks))
	} else if req.UserMCP {
		// dontAsk denies tools it was not told about, and the user's MCP
		// servers are not known up front: a hook allows every mcp__ tool
		a = append(a, "--settings", userMCPSettings)
	}
	a = append(a,
		"--tools", strings.Join(tools, ","),
		"--allowedTools", strings.Join(tools, " "),
		"--disallowedTools", strings.Join(deny, " "),
	)
	if req.Model != "" {
		a = append(a, "--model", req.Model)
	}
	a = withEffort(a, req.Effort)
	if req.System != "" {
		a = append(a, "--append-system-prompt", req.System)
	}
	if resume && req.SessionID != "" {
		a = append(a, "--resume", req.SessionID)
	}
	return a
}

func (r claudeRunner) Run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	res, err := r.run(ctx, req, emit, true)
	// A session the CLI no longer has: start over with the transcript instead.
	if err != nil && req.SessionID != "" && strings.Contains(strings.ToLower(err.Error()), "no conversation found") {
		req.Prompt = transcript(req.History, req.Prompt)
		req.SessionID = ""
		return r.run(ctx, req, emit, false)
	}
	return res, err
}

func (r claudeRunner) run(ctx context.Context, req RunRequest, emit func(Event), resume bool) (RunResult, error) {
	bin := req.Bin
	if bin == "" {
		bin = "claude"
	}
	start := time.Now()
	prompt, dirs := claudePrompt(req.Prompt, req.Attachments)
	if !resume || req.SessionID == "" {
		prompt = transcript(req.History, prompt)
	}
	args := r.args(req, resume)
	for _, d := range dirs {
		args = append(args, "--add-dir", d)
	}
	// thêm extra directories từ automation (ADR-074)
	for _, d := range req.ExtraDirs {
		d = strings.TrimSpace(d)
		if d != "" {
			args = append(args, "--add-dir", d)
		}
	}
	if req.Office != nil && !req.NoTools {
		// the office MCP server and the gateway's (ADR-091); the token goes in
		// a private temp file, not argv
		cfg, err := writeMCPConfig(req.Office)
		if err != nil {
			return RunResult{}, err
		}
		defer os.Remove(cfg)
		args = append(args, "--mcp-config", cfg)
		for i := range args {
			if args[i] == "--allowedTools" && i+1 < len(args) {
				args[i+1] += officeAllow(req.Office)
			}
		}
	}
	runCtx, quiet, stop := watchSilence(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, bin, args...)
	ownGroup(cmd)
	cmd.Dir = req.WorkDir
	cmd.Env = cliEnv(req.Provider)
	cmd.Stdin = strings.NewReader(strings.ToValidUTF8(prompt, "�")) // a stray cut byte must not make the CLI refuse the whole turn
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return RunResult{}, fmt.Errorf("không tìm thấy lệnh %q", bin)
		}
		return RunResult{}, err
	}
	defer proctrack.Track(ctx, cmd.Process.Pid)() // the dashboard's process list says whose it is
	var (
		res      RunResult
		resErr   error
		gotFinal bool
	)
	pending := map[string]int{}      // tool_use id → index in res.Tools
	subagents := map[string]string{} // a Task call's id → the subagent's label, for the steps it takes
	mainModel := ""                  // the model of the last call (its context window is the one shown)
	sc := bufio.NewScanner(quiet.out(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type      string `json:"type"`
			Subtype   string `json:"subtype"`
			SessionID string `json:"session_id"`
			// set on what a subagent of Claude's own (Task) does: the Task call it runs under
			ParentToolUseID string `json:"parent_tool_use_id"`
			Event           struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			} `json:"event"`
			RateLimit struct {
				Status  string `json:"status"`
				Windows map[string]struct {
					Utilization float64 `json:"utilization"`
					ResetsAt    int64   `json:"resetsAt"`
				} `json:"unifiedWindows"`
			} `json:"rate_limit_info"`
			ModelUsage map[string]struct {
				ContextWindow int `json:"contextWindow"`
			} `json:"modelUsage"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens         int `json:"input_tokens"`
					CacheReadTokens     int `json:"cache_read_input_tokens"`
					CacheCreationTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
				Content []struct {
					Type      string          `json:"type"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Input     json.RawMessage `json:"input"`
					ToolUseID string          `json:"tool_use_id"`
					IsError   bool            `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
			Result         string   `json:"result"`
			Errors         []string `json:"errors"`
			APIErrorStatus string   `json:"api_error_status"`
			IsError        bool     `json:"is_error"`
			TotalCostUSD   float64  `json:"total_cost_usd"`
			Usage          struct {
				InputTokens         int `json:"input_tokens"`
				OutputTokens        int `json:"output_tokens"`
				CacheReadTokens     int `json:"cache_read_input_tokens"`
				CacheCreationTokens int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.SessionID != "" {
			res.SessionID = ev.SessionID
		}
		switch ev.Type {
		case "stream_event":
			if ev.Event.Type == "content_block_delta" && ev.Event.Delta.Type == "text_delta" && ev.Event.Delta.Text != "" {
				emit(Event{Type: "text", Text: ev.Event.Delta.Text})
			}
		case "rate_limit_event":
			lim := &Limits{Status: ev.RateLimit.Status, Windows: map[string]LimitWindow{}, UpdatedAt: time.Now().UTC()}
			for k, w := range ev.RateLimit.Windows {
				lim.Windows[k] = LimitWindow{Utilization: w.Utilization, ResetsAt: time.Unix(w.ResetsAt, 0)}
			}
			res.Limits = lim
		case "assistant":
			// the last call's input is what the context holds
			if u := ev.Message.Usage; u.InputTokens+u.CacheReadTokens+u.CacheCreationTokens > 0 {
				res.Context.Tokens = u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
				mainModel = ev.Message.Model
			}
			for _, c := range ev.Message.Content {
				if c.Type == "tool_use" {
					tc := storage.ToolCall{Name: c.Name, Summary: toolSummary(c.Name, c.Input)}
					if label, ok := subagents[ev.ParentToolUseID]; ok { // a step of a subagent: shown under it
						tc.Summary = "↳ " + label + ": " + tc.Summary
					}
					if l := subagentLabel(c.Name, c.Input); l != "" {
						subagents[c.ID] = l
					}
					pending[c.ID] = len(res.Tools)
					res.Tools = append(res.Tools, tc)
					emit(Event{Type: "tool", Tool: &tc})
				}
			}
		case "user":
			for _, c := range ev.Message.Content {
				if c.Type == "tool_result" && c.IsError {
					if i, ok := pending[c.ToolUseID]; ok {
						res.Tools[i].Error = true
					}
				}
			}
		case "result":
			gotFinal = true
			if m, ok := ev.ModelUsage[mainModel]; ok {
				res.Context.Window = m.ContextWindow
			} else {
				for _, m := range ev.ModelUsage {
					res.Context.Window = max(res.Context.Window, m.ContextWindow)
				}
			}
			res.Text = ev.Result
			res.Usage.InputTokens = ev.Usage.InputTokens + ev.Usage.CacheReadTokens + ev.Usage.CacheCreationTokens
			res.Usage.OutputTokens = ev.Usage.OutputTokens
			res.Usage.CostUSD = ev.TotalCostUSD
			// the model the CLI really ran: an alias ("sonnet") names no model
			res.Usage.Model = req.Model
			if mainModel != "" {
				res.Usage.Model = mainModel
			}
			if ev.IsError || ev.Subtype != "success" {
				// the subtype alone ("error_during_execution") says nothing:
				// keep whatever the CLI did explain, stderr included
				why := []string{}
				for _, m := range append(append([]string{ev.Result}, ev.Errors...), ev.APIErrorStatus) {
					if m = strings.TrimSpace(m); m != "" && !slices.Contains(why, m) {
						why = append(why, m)
					}
				}
				if msg := strings.TrimSpace(stderr.String()); msg != "" && !slices.Contains(why, msg) {
					why = append(why, truncate(msg, 300))
				}
				if len(why) == 0 {
					why = append(why, ev.Subtype)
				} else if ev.Subtype != "" && ev.Subtype != "error" {
					why = append(why, "("+ev.Subtype+")")
				}
				resErr = fmt.Errorf("claude: %s", strings.Join(why, " · "))
				if ev.IsError {
					// its result is the error ("API Error: 401 …"), not an
					// answer: kept as text it would read as the agent's reply
					res.Text = ""
				}
			}
		}
	}
	if serr := sc.Err(); serr != nil && resErr == nil {
		// the scan stopped on a read/buffer error (e.g. a JSON line over the
		// 16MB cap), not a clean EOF: without this, a result that never
		// arrived reads as a silent success instead of a failure
		resErr = fmt.Errorf("claude: đọc output lỗi: %w", serr)
	}
	// the CLI may still be writing (e.g. the rest of the oversized line): drain
	// it so that write doesn't block forever and deadlock cmd.Wait() below
	io.Copy(io.Discard, out)
	werr := cmd.Wait()
	res.Usage.DurationMS = time.Since(start).Milliseconds()
	if err := quiet.err("Claude Code"); err != nil {
		return res, err
	}
	// A signed-out CLI fails before any API call: say what to do instead of
	// leaving the chat with the CLI's own wording. A session the service
	// rejects (signed out elsewhere) still reads as signed in locally.
	if (resErr != nil || werr != nil) && ctx.Err() == nil && res.Usage.InputTokens == 0 &&
		(claudeAuthFailed(resErr) || !claudeSignedIn(bin, cmd.Env)) {
		return res, fmt.Errorf("%w: Claude Code trên máy chưa đăng nhập hoặc phiên đã hết hạn. Vào Kết nối AI, bấm Kiểm tra rồi Đăng nhập", llm.ErrNeedsLogin)
	}
	switch {
	case resErr != nil:
		return res, resErr
	case ctx.Err() != nil:
		return res, ctx.Err()
	case werr != nil && !gotFinal:
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = werr.Error()
		}
		return res, fmt.Errorf("claude: %s", truncate(msg, 500))
	}
	return res, nil
}

// claudeAuthFailed: the service turned the CLI's account down ("Failed to
// authenticate. API Error: 401 …").
func claudeAuthFailed(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "failed to authenticate") || strings.Contains(s, "authentication_error") ||
		strings.Contains(s, "api error: 401") || strings.Contains(s, "/login")
}

// claudeSignedIn reports whether the CLI has an account; a version too old for
// `auth status` (no JSON answer) counts as signed in, so nothing regresses.
func claudeSignedIn(bin string, env []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status", "--json")
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return true
	}
	var st struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if json.Unmarshal([]byte(firstJSONObject(string(out))), &st) != nil {
		return true
	}
	return st.LoggedIn
}

// cliEnv runs a connection with an account of its own in its config folder
// (ADR-178); nil keeps the office's environment.
func cliEnv(p storage.Provider) []string {
	if env := p.CLIEnv(); env != nil {
		return append(os.Environ(), env...)
	}
	return nil
}

// firstJSONObject pulls the JSON object out of output that may carry warnings.
func firstJSONObject(s string) string {
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}

// toolSummary turns a tool input into a short label for the chat ("Đọc src/app.vue").
func toolSummary(name string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := in[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch strings.ToLower(strings.TrimPrefix(name, "mcp__"+mcpserver.ServerName+"__")) {
	case "ops_overview":
		return "Xem tổng quan vận hành"
	case "process_logs":
		return "Đọc log tiến trình " + str("name")
	case "container_logs":
		return "Đọc log container " + str("service")
	case "monitor_detail":
		return "Xem giám sát " + str("name")
	case "git_status":
		return "Xem git status"
	case "git_diff":
		return "Xem git diff"
	case "git_log":
		return "Xem git log"
	case "run_command":
		return "Chạy lệnh " + str("command")
	case "propose_action":
		return "Đề xuất " + strings.ReplaceAll(str("action"), "_", " ") + " " + str("target")
	case "read", "read_file":
		return "Đọc " + str("file_path", "path")
	case "glob":
		return "Tìm file " + str("pattern")
	case "grep", "search", "search_text", "grep_search":
		return "Tìm \"" + str("pattern") + "\""
	case "list_dir", "ls", "list_directory":
		p := str("path")
		if p == "" {
			p = "."
		}
		return "Xem thư mục " + p
	case "write_file", "replace":
		return "Sửa " + str("file_path")
	case "task", "agent": // a subagent of Claude's own; its steps are shown under it
		return "Giao subagent " + str("subagent_type") + ": " + str("description")
	case "bash", "command_execution", "run_shell_command":
		return "Chạy " + str("command")
	}
	return name
}

// transcript folds earlier turns into one prompt for runtimes without sessions.
func transcript(history []HistoryItem, prompt string) string {
	if len(history) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString("The conversation so far:\n")
	for _, h := range history {
		who := "The person"
		if h.Role == "assistant" {
			who = "You"
			if h.Author != "" { // an agent that answered before a switch
				who = h.Author
			}
		}
		fmt.Fprintf(&b, "\n[%s]\n%s\n", who, truncate(h.Content, 6000))
	}
	b.WriteString("\n---\nThe person's new message:\n")
	b.WriteString(prompt)
	return b.String()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Back up to a rune boundary: a cut mid-rune leaves invalid UTF-8
	// (Vietnamese text), which some CLIs refuse to read.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// officeAllow is the allow rules for office's MCP servers, one per server
// in the whole-name form mcp__<server>__*: since Claude Code 2.1.299 a bare
// mcp__<server> no longer reaches every tool of a server.
func officeAllow(o *OfficeAccess) string {
	s := " mcp__" + mcpserver.ServerName + "__*"
	if o.Other {
		s += " mcp__" + officetools.OtherServer + "__*"
	}
	for _, n := range o.Gateway {
		s += " mcp__" + n + "__*"
	}
	return s
}

func writeMCPConfig(o *OfficeAccess) (string, error) {
	auth := map[string]string{"Authorization": "Bearer " + o.Token}
	servers := map[string]any{mcpserver.ServerName: map[string]any{"type": "http", "url": o.MCPURL, "headers": auth}}
	if o.Other {
		servers[officetools.OtherServer] = map[string]any{"type": "http", "url": o.MCPURL + mcpserver.OtherPath, "headers": auth}
	}
	// which AI calls, for the call log (ADR-093)
	gw := map[string]string{"Authorization": auth["Authorization"], ClientHeader: "claude"}
	for _, n := range o.Gateway { // same token: the gateway adds each server's own secrets
		servers[n] = map[string]any{"type": "http", "url": o.MCPURL + "/s/" + n, "headers": gw}
	}
	raw, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "office-mcp-*.json") // created 0600
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// compactClaude has Claude Code compact a session (its /compact), as it does
// itself when a chat fills up: the session goes on, shorter. It returns the
// tokens the session holds after.
func compactClaude(ctx context.Context, bin string, env []string, dir, model, session string) (int, error) {
	if bin == "" {
		bin = "claude"
	}
	args := []string{"-p", "/compact", "--output-format", "stream-json", "--verbose", "--permission-mode", "dontAsk",
		"--setting-sources", "user,project,local", "--tools", "", "--strict-mcp-config", "--resume", session}
	if model != "" {
		args = append(args, "--model", model)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	defer proctrack.Track(ctx, cmd.Process.Pid)()
	post, compacted, fail := 0, false, ""
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type     string `json:"type"`
			Subtype  string `json:"subtype"`
			IsError  bool   `json:"is_error"`
			Result   string `json:"result"`
			Metadata struct {
				PostTokens int `json:"post_tokens"`
			} `json:"compact_metadata"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "system" && ev.Subtype == "compact_boundary":
			post, compacted = ev.Metadata.PostTokens, true
		case ev.Type == "result" && (ev.IsError || ev.Subtype != "success"):
			fail = firstNonEmpty(ev.Result, ev.Subtype)
		}
	}
	werr := cmd.Wait()
	switch {
	case fail != "":
		return 0, fmt.Errorf("claude: %s", fail)
	case !compacted && werr != nil:
		return 0, fmt.Errorf("claude: %v %s", werr, strings.TrimSpace(stderr.String()))
	case !compacted:
		return 0, errors.New("claude: không tóm gọn được phiên")
	}
	return post, nil
}

// windowOf is how many tokens a model holds: Haiku before 5 200K, a "[1m]"
// model 1M, else as its last run said (0: 200K, the safe guess).
func windowOf(model string, seen int) int {
	switch {
	case strings.Contains(model, "haiku") && !strings.Contains(model, "haiku-5"):
		return 200_000
	case strings.Contains(model, "[1m]"):
		return 1_000_000
	case seen > 0:
		return seen
	}
	return 200_000
}

// subagentLabel names a subagent Claude Code starts on its own (the Task /
// Agent tool): its type and what it was asked ("" = not such a call).
func subagentLabel(name string, input json.RawMessage) string {
	if name != "Task" && name != "Agent" {
		return ""
	}
	var in struct {
		Description string `json:"description"`
		Type        string `json:"subagent_type"`
	}
	_ = json.Unmarshal(input, &in)
	return truncate(strings.TrimSpace(cmp.Or(in.Type, "subagent")+" · "+in.Description), 60)
}
