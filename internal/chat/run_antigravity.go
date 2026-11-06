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
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/cliuse"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/proctrack"
	"github.com/tung1998/agent-office/internal/storage"
)

// antigravityRunner drives Antigravity CLI (`agy`) in print mode: the prompt
// on stdin, stream-json out, and the chat's conversation resumed with
// --conversation, like the CLI itself (ADR-106).
type antigravityRunner struct{}

// agyEvent is one stream-json line: {"event": "<name>", "<name>": {...}}.
type agyEvent struct {
	Event string `json:"event"`
	Init  struct {
		Model string `json:"model"`
	} `json:"init"`
	StepUpdate struct {
		ConversationID string          `json:"conversation_id"`
		StepIndex      int             `json:"step_index"`
		State          string          `json:"state"`
		StepType       string          `json:"step_type"`
		TextDelta      string          `json:"text_delta"`
		ToolName       string          `json:"tool_name"`
		ToolInfo       json.RawMessage `json:"tool_info"`
		Error          string          `json:"error"`
	} `json:"step_update"`
	Result struct {
		ConversationID string `json:"conversation_id"`
		Status         string `json:"status"`
		Response       string `json:"response"`
		Error          string `json:"error"`
		Usage          struct {
			InputTokens     int `json:"input_tokens"`
			OutputTokens    int `json:"output_tokens"`
			ThinkingTokens  int `json:"thinking_tokens"`
			CacheReadTokens int `json:"cache_read_tokens"`
		} `json:"usage"`
	} `json:"result"`
}

func (r antigravityRunner) Run(ctx context.Context, req RunRequest, emit func(Event)) (RunResult, error) {
	res, stepErr, err := r.run(ctx, req, emit)
	// a conversation agy no longer has: start over with the transcript; not
	// when the first attempt already acted (a tool call), since restarting
	// would repeat that action for real, not just its display
	if err != nil && req.SessionID != "" && res.Text == "" && stepErr == "" && len(res.Tools) == 0 && ctx.Err() == nil &&
		!errors.Is(err, ErrSilent) && !errors.Is(err, llm.ErrNeedsLogin) {
		req.SessionID = ""
		res, stepErr, err = r.run(ctx, req, emit)
	}
	// agy ends a turn without an answer when a step fails (a command the
	// mode denies): asked once more in its conversation to answer with what
	// it has, else the turn fails (not an empty answer taken as done)
	if res.Text == "" && res.SessionID != "" && ctx.Err() == nil && (err == nil || stepErr != "") {
		more := req
		more.SessionID, more.Attachments = res.SessionID, nil
		more.Prompt = "You have not answered yet. Write your final answer to the request above with what you have; do not retry the step that just failed."
		if !req.FullAccess {
			more.Prompt += " " + agyNoShell
		}
		if stepErr != "" {
			more.Prompt = "The last step failed: " + truncate(stepErr, 300) + "\n" + more.Prompt
		}
		again, againErr, err2 := r.run(ctx, more, emit)
		again.Tools = append(res.Tools, again.Tools...)
		again.Usage.InputTokens += res.Usage.InputTokens
		again.Usage.OutputTokens += res.Usage.OutputTokens
		again.Usage.DurationMS += res.Usage.DurationMS
		res, err = again, err2
		stepErr = cmp.Or(againErr, stepErr)
	}
	if err == nil && res.Text == "" {
		msg := "agy kết thúc lượt mà không trả lời"
		if stepErr != "" {
			msg += ": " + truncate(stepErr, 300)
		}
		err = errors.New(msg)
	}
	return res, err
}

// run is one agy call; stepErr is the last step that failed (it may end the turn).
func (antigravityRunner) run(ctx context.Context, req RunRequest, emit func(Event)) (res RunResult, stepErr string, _ error) {
	bin := firstNonEmpty(req.Bin, "agy")
	args := []string{"--output-format", "stream-json", "--disable-slash-commands"}
	switch {
	case req.FullAccess:
		args = append(args, "--dangerously-skip-permissions")
	case req.Write:
		args = append(args, "--mode", "accept-edits") // edits in its folder; commands stay soft-denied
	default:
		args = append(args, "--mode", "plan") // reads only
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	// the model names its level, or the default model takes up to high (ADR-106)
	if e := storage.FitEffort(storage.ProviderAntigravityCLI, req.Model, req.Effort); e != "" {
		args = append(args, "--effort", e)
	}
	if req.SessionID != "" {
		args = append(args, "--conversation", req.SessionID)
	}
	prompt, imageDirs := agyPrompt(req)
	for _, d := range append(slices.Clone(req.ExtraDirs), imageDirs...) {
		if d = strings.TrimSpace(d); d != "" {
			args = append(args, "--add-dir", d)
		}
	}
	start := time.Now()
	runCtx, quiet, stop := watchSilence(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, bin, args...)
	ownGroup(cmd)
	cmd.Dir = req.WorkDir
	cmd.Stdin = strings.NewReader(strings.ToValidUTF8(prompt, "�")) // a stray cut byte must not make the CLI refuse the whole turn
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, "", err
	}
	inUse, err := cliuse.Begin(ctx, bin) // an update of this CLI holds new runs back (ADR-259)
	if err != nil {
		return RunResult{}, "", err
	}
	defer inUse()
	if err := cmd.Start(); err != nil {
		return RunResult{}, "", llm.StartErrorIn(bin, cmd.Dir, err)
	}
	defer proctrack.Track(ctx, cmd.Process.Pid)()
	res.Usage.Model = req.Model
	var answer strings.Builder
	var failure, final string
	seenTool := map[int]bool{}
	sc := bufio.NewScanner(quiet.out(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev agyEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Event {
		case "init":
			res.Usage.Model = cmp.Or(ev.Init.Model, res.Usage.Model)
		case "step_update":
			st := ev.StepUpdate
			res.SessionID = cmp.Or(st.ConversationID, res.SessionID)
			if st.State == "ERROR" {
				stepErr = cmp.Or(st.Error, agyStepFailure(st.StepType, st.ToolName, st.ToolInfo))
			}
			switch {
			case st.StepType == "agent_response" && st.TextDelta != "":
				answer.WriteString(st.TextDelta)
				emit(Event{Type: "text", Text: st.TextDelta})
			case st.ToolName != "" && !seenTool[st.StepIndex]:
				seenTool[st.StepIndex] = true
				tc := storage.ToolCall{Name: st.ToolName, Summary: toolSummary(st.ToolName, st.ToolInfo)}
				res.Tools = append(res.Tools, tc)
				emit(Event{Type: "tool", Tool: &tc})
			}
		case "result":
			rs := ev.Result
			res.SessionID = cmp.Or(rs.ConversationID, res.SessionID)
			u := rs.Usage
			res.Usage.InputTokens += u.InputTokens + u.CacheReadTokens
			res.Usage.OutputTokens += u.OutputTokens + u.ThinkingTokens
			final = rs.Response
			if rs.Status != "SUCCESS" {
				failure = cmp.Or(rs.Error, rs.Status)
			}
		}
	}
	var scanErr error
	if serr := sc.Err(); serr != nil {
		scanErr = fmt.Errorf("antigravity: đọc output lỗi: %w", serr)
	}
	io.Copy(io.Discard, out)
	werr := cmd.Wait()
	res.Usage.DurationMS = time.Since(start).Milliseconds()
	res.Text = strings.TrimSpace(answer.String())
	if res.Text == "" && final != "" { // no deltas streamed: the result has it all
		res.Text = strings.TrimSpace(final)
		emit(Event{Type: "text", Text: res.Text})
	}
	if err := quiet.err("Antigravity CLI"); err != nil {
		return res, stepErr, err
	}
	if scanErr != nil {
		return res, stepErr, scanErr
	}
	if res.Text == "" && (werr != nil || failure != "") {
		msg := cmp.Or(failure, strings.TrimSpace(stderr.String()))
		if msg == "" {
			msg = werr.Error()
		}
		if strings.Contains(msg, "authentication") {
			return res, stepErr, fmt.Errorf("%w: Antigravity CLI chưa đăng nhập (%s), bấm Đăng nhập ở Kết nối AI", llm.ErrNeedsLogin, truncate(msg, 200))
		}
		return res, stepErr, fmt.Errorf("agy: %s", truncate(msg, 500))
	}
	return res, stepErr, nil
}

// agyPrompt is the turn's text: instructions and the transcript for a new
// conversation, only the new message for a resumed one. agy reads text only,
// so images are named by path and their folders added to the workspace.
func agyPrompt(req RunRequest) (string, []string) {
	prompt, images := codexPrompt(req.Prompt, req.Attachments)
	var dirs []string
	for _, p := range images {
		prompt += "\n(Attached image: " + p + ")"
		if d := filepath.Dir(p); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	if req.SessionID == "" {
		system := req.System
		if !req.FullAccess {
			system += "\n\n" + agyNoShell
		}
		prompt = system + "\n\n" + transcript(req.History, prompt)
	}
	return prompt, dirs
}

// agyNoShell: without full access agy soft-denies every command in print
// mode (no one to approve it) and then ends the turn with no answer, so the
// model is told up front to read with its file tools only.
const agyNoShell = "Shell commands (run_command) are not available in this run and are always denied: do not call them. Read with view_file, grep_search, list_dir and find_by_name, then write your answer."

// agyStepFailure names a failed step when the step carries no error text:
// agy puts a tool's error under tool_info, e.g. `run_command "git status":
// permission check failed…` instead of a bare "tool lỗi".
func agyStepFailure(stepType, tool string, info json.RawMessage) string {
	if tool == "" {
		return stepType + " lỗi"
	}
	var ti struct {
		Parameters map[string]any `json:"parameters"`
		Error      struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(info, &ti)
	why := cmp.Or(strings.TrimSpace(ti.Error.Message), "lỗi")
	for _, k := range []string{"CommandLine", "AbsolutePath", "Query", "SearchPath"} {
		if v, ok := ti.Parameters[k].(string); ok && v != "" {
			return fmt.Sprintf("%s %q: %s", tool, truncate(v, 120), why)
		}
	}
	return tool + ": " + why
}
