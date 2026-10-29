package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/attach"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/proctrack"
	"github.com/tung1998/agent-office/internal/storage"
)

// geminiRunner drives Gemini CLI headless (prompt on stdin, stream-json out)
// and goes on in the chat's session with --resume, like the CLI itself.
type geminiRunner struct{}

// geminiTokenEnv carries the run's token; the settings file only names it.
const geminiTokenEnv = "OFFICE_MCP_TOKEN"

// geminiMCP is the office MCP server and the gateway's servers in Gemini's
// settings shape (streamable HTTP, trusted: office decides what may run).
func geminiMCP(req RunRequest) map[string]any {
	if req.Office == nil || req.NoTools || req.Office.MCPURL == "" {
		return nil
	}
	out := map[string]any{}
	add := func(name, url string, client bool) {
		h := map[string]string{"Authorization": "Bearer $" + geminiTokenEnv}
		if client {
			h[ClientHeader] = "gemini"
		}
		out[name] = map[string]any{"httpUrl": url, "headers": h, "trust": true}
	}
	add(mcpserver.ServerName, req.Office.MCPURL, false)
	if req.Office.Other {
		add(officetools.OtherServer, req.Office.MCPURL+mcpserver.OtherPath, false)
	}
	for _, n := range directGateway(req.Office) {
		add(n, req.Office.MCPURL+"/s/"+n, true)
	}
	return out
}

// jsonComments strips // line comments, which Gemini allows in settings.json.
var jsonComments = regexp.MustCompile(`(?m)^\s*//.*$`)

// geminiHome builds a throwaway GEMINI_CLI_HOME whose settings.json is the
// person's plus office's MCP servers; everything else (sign-in, extensions,
// GEMINI.md) links to the real ~/.gemini. Gemini only reads extra settings
// from root-owned files or the workspace, and the workspace is the diff.
func geminiHome(real string, servers map[string]any, userMCP bool) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "office-gemini-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) } // removes the links, not their targets
	dir := filepath.Join(tmp, ".gemini")
	if err := os.Mkdir(dir, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	// sessions and the project registry live here: link them even before
	// Gemini's first run, or they would go with the throwaway home
	for _, d := range []string{"tmp", "history"} {
		_ = os.MkdirAll(filepath.Join(real, d), 0o700)
	}
	entries, _ := os.ReadDir(real)
	for _, e := range entries {
		if e.Name() != "settings.json" {
			_ = os.Symlink(filepath.Join(real, e.Name()), filepath.Join(dir, e.Name()))
		}
	}
	settings := map[string]any{}
	if raw, err := os.ReadFile(filepath.Join(real, "settings.json")); err == nil {
		if json.Unmarshal(raw, &settings) != nil {
			_ = json.Unmarshal(jsonComments.ReplaceAll(raw, nil), &settings)
		}
	}
	mine, _ := settings["mcpServers"].(map[string]any)
	if mine == nil || !userMCP {
		mine = map[string]any{}
	}
	for k, v := range servers {
		mine[k] = v
	}
	settings["mcpServers"] = mine
	raw, err := json.Marshal(settings)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o600)
	}
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return tmp, cleanup, nil
}

var (
	geminiUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_\-.:]`)
)

// geminiMCPName turns Gemini's "mcp_<server>_<tool>" (names sanitized) back
// into office's "mcp__<server>__<tool>".
func geminiMCPName(name string, servers []string) string {
	// longest first: "a_b" must win over "a" for "mcp_a_b_tool"
	sort.Slice(servers, func(i, j int) bool { return len(servers[i]) > len(servers[j]) })
	for _, s := range servers {
		if p := "mcp_" + geminiUnsafe.ReplaceAllString(s, "_") + "_"; strings.HasPrefix(name, p) {
			return "mcp__" + s + "__" + strings.TrimPrefix(name, p)
		}
	}
	return name
}

// geminiPrompt inlines text files and points at images with @path, which
// Gemini reads itself; it returns the folders it must be allowed to read.
func geminiPrompt(prompt string, files []attach.File) (string, []string) {
	prompt, images := codexPrompt(prompt, files)
	var dirs []string
	for _, p := range images {
		prompt += "\n@" + p
		if d := filepath.Dir(p); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return prompt, dirs
}

func (r geminiRunner) Run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	res, err := r.run(ctx, req, emit)
	// a session Gemini no longer has: start over with the transcript
	if err != nil && req.SessionID != "" && res.Text == "" {
		req.SessionID = ""
		return r.run(ctx, req, emit)
	}
	return res, err
}

func (geminiRunner) run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	bin := firstNonEmpty(req.Bin, "gemini")
	// Headless Gemini drops tools that would need a confirmation: default
	// keeps it to reading, auto_edit adds file edits, yolo runs anything.
	mode := "default"
	switch {
	case req.FullAccess:
		mode = "yolo"
	case req.Write:
		mode = "auto_edit"
	}
	args := []string{"--output-format", "stream-json", "--approval-mode", mode}
	if req.Model != "" {
		args = append(args, "-m", req.Model)
	}
	if req.SessionID != "" {
		args = append(args, "--resume", req.SessionID)
	}
	prompt, imageDirs := geminiPrompt(req.Prompt, req.Attachments)
	dirs := append(slices.Clone(req.ExtraDirs), imageDirs...)
	if len(dirs) > 5 { // Gemini's limit
		dirs = dirs[:5]
	}
	if len(dirs) > 0 {
		args = append(args, "--include-directories", strings.Join(dirs, ","))
	}
	servers := geminiMCP(req)
	var names []string
	for n := range servers {
		names = append(names, n)
	}
	if !req.UserMCP {
		// only office's servers, also none from extensions; an unknown name
		// keeps the list from meaning "all"
		args = append(args, "--allowed-mcp-server-names", strings.Join(append(names, "office-none"), ","))
	}
	// the workdir is office's to trust; yolo would otherwise start a Docker sandbox
	// the connection's own account first (ADR-178): the run's home below builds on it
	env := append(append(os.Environ(), req.Provider.CLIEnv()...), "GEMINI_CLI_TRUST_WORKSPACE=true")
	if mode == "yolo" {
		env = append(env, "GEMINI_SANDBOX=false")
	}
	if len(servers) > 0 {
		home, cleanup, err := geminiHome(llm.GeminiDir(req.Provider.CLIEnv()), servers, req.UserMCP)
		if err != nil {
			return RunResult{}, fmt.Errorf("gemini: không tạo được cấu hình MCP: %w", err)
		}
		defer cleanup()
		// the run's token reaches Gemini through its environment, not a file
		env = append(env, "GEMINI_CLI_HOME="+home, geminiTokenEnv+"="+req.Office.Token)
	}
	start := time.Now()
	runCtx, quiet, stop := watchSilence(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, bin, args...)
	ownGroup(cmd)
	cmd.Dir = req.WorkDir
	cmd.Env = env
	// stdin that is not a terminal makes Gemini headless without -p. A
	// resumed session already has the instructions and the earlier turns.
	if req.SessionID == "" {
		prompt = req.System + "\n\n" + transcript(req.History, prompt)
	} else if brief := gatewayBrief(req.Office); !req.NoTools && brief != "" {
		prompt = strings.TrimSpace(brief) + "\n\n" + prompt
	}
	cmd.Stdin = strings.NewReader(strings.ToValidUTF8(prompt, "�")) // a stray cut byte must not make the CLI refuse the whole turn
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("không chạy được %s: %w", bin, err)
	}
	defer proctrack.Track(ctx, cmd.Process.Pid)()
	res := RunResult{}
	res.Usage.Model = req.Model
	var answer strings.Builder
	var failure string
	sc := bufio.NewScanner(quiet.out(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type       string          `json:"type"`
			SessionID  string          `json:"session_id"`
			Model      string          `json:"model"`
			Role       string          `json:"role"`
			Content    string          `json:"content"`
			ToolName   string          `json:"tool_name"`
			Parameters json.RawMessage `json:"parameters"`
			Status     string          `json:"status"`
			Severity   string          `json:"severity"`
			Message    string          `json:"message"`
			Error      struct {
				Message string `json:"message"`
			} `json:"error"`
			Stats struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"stats"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "init":
			res.SessionID = ev.SessionID
			if ev.Model != "" {
				res.Usage.Model = ev.Model
			}
		case "message":
			if ev.Role == "assistant" && ev.Content != "" {
				answer.WriteString(ev.Content)
				emit(Event{Type: "text", Text: ev.Content})
			}
		case "tool_use":
			name := geminiMCPName(ev.ToolName, names)
			tc := storage.ToolCall{Name: name, Summary: toolSummary(name, ev.Parameters)}
			res.Tools = append(res.Tools, tc)
			emit(Event{Type: "tool", Tool: &tc})
		case "error":
			if ev.Severity == "error" {
				failure = ev.Message
			}
		case "result":
			res.Usage.InputTokens, res.Usage.OutputTokens = ev.Stats.InputTokens, ev.Stats.OutputTokens
			if ev.Status == "error" && ev.Error.Message != "" {
				failure = ev.Error.Message
			}
		}
	}
	var scanErr error
	if serr := sc.Err(); serr != nil {
		scanErr = fmt.Errorf("gemini: đọc output lỗi: %w", serr)
	}
	io.Copy(io.Discard, out)
	werr := cmd.Wait()
	res.Usage.DurationMS = time.Since(start).Milliseconds()
	res.Text = strings.TrimSpace(answer.String())
	if err := quiet.err("Gemini CLI"); err != nil {
		return res, err
	}
	if scanErr != nil {
		return res, scanErr
	}
	if res.Text == "" && (werr != nil || failure != "") {
		msg := failure
		if msg == "" {
			msg = llm.GeminiError(stderr.String())
		}
		if msg == "" {
			msg = werr.Error()
		}
		return res, fmt.Errorf("gemini: %s", truncate(msg, 500))
	}
	return res, nil
}
