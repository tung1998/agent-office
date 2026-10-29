package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/proctrack"
	"github.com/tung1998/agent-office/internal/storage"
)

// codexRunner drives `codex exec` with the run's effective permissions and
// goes on in the chat's session with `codex exec resume`, like the CLI itself.
type codexRunner struct{}

// codexTokenEnv carries the run's token to Codex's MCP client.
const codexTokenEnv = "OFFICE_MCP_TOKEN"

// codexMCPArgs gives Codex the office MCP server and the gateway's servers
// (streamable HTTP, the run's token from codexTokenEnv; ADR-093), their tools
// approved: office decides what may run, and `codex exec` (approval policy
// never) refuses a tool that is not read-only, such as propose_action.
func codexMCPArgs(req RunRequest) []string {
	if req.Office == nil || req.NoTools || req.Office.MCPURL == "" {
		return nil
	}
	q := strconv.Quote
	out := []string{"-c", "experimental_use_rmcp_client=true"}
	add := func(name, url string, client bool) {
		key := "mcp_servers." + name
		out = append(out, "-c", key+".url="+q(url), "-c", key+".bearer_token_env_var="+q(codexTokenEnv),
			"-c", key+".default_tools_approval_mode="+q("approve"))
		if client {
			out = append(out, "-c", key+".http_headers={"+q(ClientHeader)+"="+q("codex")+"}")
		}
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

// codexEffortArgs asks Codex to think this hard; its highest is xhigh.
func codexEffortArgs(e string) []string {
	if e == "" {
		return nil
	}
	if e == "max" {
		e = "xhigh"
	}
	return []string{"-c", "model_reasoning_effort=" + strconv.Quote(e)}
}

func (r codexRunner) Run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	res, err := r.run(ctx, req, emit)
	// a session Codex no longer has: start over with the transcript (not a
	// Codex that never answered: it would hang just as long again)
	if err != nil && req.SessionID != "" && res.Text == "" && !errors.Is(err, ErrSilent) {
		req.SessionID = ""
		return r.run(ctx, req, emit)
	}
	return res, err
}

func codexSandbox(req RunRequest) string {
	if req.NoTools {
		return "read-only"
	}
	if req.FullAccess {
		return "danger-full-access"
	}
	if req.Write {
		return "workspace-write"
	}
	return "read-only"
}

func (codexRunner) run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	bin := firstNonEmpty(req.Bin, "codex")
	args := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", codexSandbox(req), "--cd", req.WorkDir}
	if req.Model != "" {
		args = append(args, "-m", req.Model)
	}
	args = append(args, codexEffortArgs(req.Effort)...)
	args = append(args, codexMCPArgs(req)...)
	prompt, images := codexPrompt(req.Prompt, req.Attachments)
	if req.SessionID != "" {
		// exec's options stay before the subcommand
		args = append(args, "resume", req.SessionID)
		if brief := gatewayBrief(req.Office); !req.NoTools && brief != "" {
			prompt = strings.TrimSpace(brief) + "\n\n" + prompt
		}
	} else {
		// a resumed session already has the instructions and earlier turns
		prompt = req.System + "\n\n" + transcript(req.History, prompt)
	}
	args = append(args, "-") // the prompt from stdin
	if len(images) > 0 {
		// last: --image takes several values and would swallow what follows
		args = append(args, "--image", strings.Join(images, ","))
	}
	start := time.Now()
	runCtx, quiet, stop := watchSilence(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, bin, args...)
	ownGroup(cmd)
	cmd.Dir = req.WorkDir
	cmd.Env = cliEnv(req.Provider)
	if req.Office != nil && !req.NoTools {
		// the run's token reaches Codex through its environment, not argv
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, codexTokenEnv+"="+req.Office.Token)
	}
	home := llm.CodexHomeIn(req.Provider.CLIEnv())
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
	var answer []string
	var failed []string // turn.failed / error lines: why a run that answered nothing failed
	sc := bufio.NewScanner(quiet.out(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Command string `json:"command"`
				Server  string `json:"server"`
				Tool    string `json:"tool"`
			} `json:"item"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Type == "turn.failed" || ev.Type == "error" {
			failed = append(failed, sc.Text())
		}
		switch {
		case ev.Type == "thread.started" && ev.ThreadID != "":
			res.SessionID = ev.ThreadID
		case ev.Type == "item.completed" && ev.Item.Type == "agent_message" && ev.Item.Text != "":
			if len(answer) > 0 {
				emit(Event{Type: "text", Text: "\n\n"})
			}
			answer = append(answer, ev.Item.Text)
			emit(Event{Type: "text", Text: ev.Item.Text})
		case ev.Type == "item.started" && ev.Item.Type == "command_execution":
			in, _ := json.Marshal(map[string]string{"command": ev.Item.Command})
			tc := storage.ToolCall{Name: "command_execution", Summary: toolSummary("command_execution", in)}
			res.Tools = append(res.Tools, tc)
			emit(Event{Type: "tool", Tool: &tc})
		case ev.Type == "item.started" && ev.Item.Type == "mcp_tool_call":
			tc := storage.ToolCall{Name: "mcp__" + ev.Item.Server + "__" + ev.Item.Tool}
			res.Tools = append(res.Tools, tc)
			emit(Event{Type: "tool", Tool: &tc})
		}
		if ev.Usage.InputTokens+ev.Usage.OutputTokens > 0 {
			res.Usage.InputTokens, res.Usage.OutputTokens = ev.Usage.InputTokens, ev.Usage.OutputTokens
		}
	}
	var scanErr error
	if serr := sc.Err(); serr != nil {
		scanErr = fmt.Errorf("codex: đọc output lỗi: %w", serr)
	}
	io.Copy(io.Discard, out)
	werr := cmd.Wait()
	res.Usage.DurationMS = time.Since(start).Milliseconds()
	res.Text = strings.TrimSpace(strings.Join(answer, "\n\n"))
	res.Limits = codexLimits(home, res.SessionID, time.Now())
	if err := quiet.err("Codex"); err != nil {
		return res, err
	}
	if scanErr != nil {
		return res, scanErr
	}
	if res.Text == "" {
		if msg := llm.CodexFailure(strings.Join(failed, "\n")); msg != "" {
			if llm.CodexAuthFailed(msg) || llm.CodexAuthFailed(stderr.String()) {
				llm.MarkCodexRejected(home)
				return res, fmt.Errorf("%w: phiên Codex bị từ chối (%s), hãy bấm Đăng nhập ở Kết nối AI để ký lại", llm.ErrNeedsLogin, truncate(msg, 200))
			}
			return res, fmt.Errorf("codex: %s", truncate(msg, 500))
		}
	}
	if werr != nil && res.Text == "" {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = werr.Error()
		}
		if llm.CodexAuthFailed(msg) {
			llm.MarkCodexRejected(home)
			return res, fmt.Errorf("%w: phiên Codex bị từ chối (%s), hãy bấm Đăng nhập ở Kết nối AI để ký lại", llm.ErrNeedsLogin, truncate(msg, 200))
		}
		return res, fmt.Errorf("codex: %s", truncate(msg, 500))
	}
	return res, nil
}

// codexLimits reads the usage windows Codex wrote to the session's rollout
// file (`codex exec --json` does not print them): the last token_count
// event's rate_limits, mapped onto Claude's window names.
func codexLimits(home, threadID string, now time.Time) *Limits {
	if threadID == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if len(files) == 0 {
		return nil
	}
	return codexRolloutLimits(files[len(files)-1], now)
}

// CodexLatestLimits is the usage Codex last saw on this machine (its newest
// session, office's or the person's own), so a connection shows its quota
// before office ever ran it.
func CodexLatestLimits(home string) *Limits {
	days, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*"))
	slices.Sort(days) // YYYY/MM/DD: the newest day last
	for i := len(days) - 1; i >= 0 && i >= len(days)-7; i-- {
		files, _ := filepath.Glob(filepath.Join(days[i], "rollout-*.jsonl"))
		var newest string
		var at time.Time
		for _, f := range files {
			if st, err := os.Stat(f); err == nil && st.ModTime().After(at) {
				newest, at = f, st.ModTime()
			}
		}
		if newest != "" {
			if l := codexRolloutLimits(newest, at); l != nil {
				return l
			}
		}
	}
	return nil
}

// codexRolloutLimits reads the last rate_limits in one rollout file.
func codexRolloutLimits(path string, now time.Time) *Limits {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	type window struct {
		UsedPercent   float64 `json:"used_percent"`
		WindowMinutes int     `json:"window_minutes"`
		ResetsAt      int64   `json:"resets_at"`
	}
	var last *struct {
		Primary, Secondary *window
		Reached            *string `json:"rate_limit_reached_type"`
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		if !bytes.Contains(sc.Bytes(), []byte(`"rate_limits"`)) {
			continue
		}
		var ev struct {
			Payload struct {
				RateLimits *struct {
					Primary, Secondary *window
					Reached            *string `json:"rate_limit_reached_type"`
				} `json:"rate_limits"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Payload.RateLimits != nil {
			last = ev.Payload.RateLimits
		}
	}
	if last == nil {
		return nil
	}
	lim := &Limits{Status: "allowed", Windows: map[string]LimitWindow{}, UpdatedAt: now.UTC()}
	for _, w := range []*window{last.Primary, last.Secondary} {
		if w == nil || w.WindowMinutes == 0 {
			continue
		}
		key := "w" + strconv.Itoa(w.WindowMinutes) + "m"
		switch w.WindowMinutes {
		case 300:
			key = "five_hour"
		case 7 * 24 * 60:
			key = "seven_day"
		}
		lim.Windows[key] = LimitWindow{Utilization: w.UsedPercent / 100, ResetsAt: time.Unix(w.ResetsAt, 0)}
	}
	if last.Reached != nil && *last.Reached != "" {
		lim.Status = "rejected"
	}
	return lim
}
