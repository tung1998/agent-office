package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// run executes bin with args, feeding stdin; stderr is kept for error messages.
func run(ctx context.Context, bin string, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("không tìm thấy lệnh %q trong PATH", bin)
	}
	if err != nil {
		return stdout.String(), fmt.Errorf("%s: %v: %s", bin, err, cliFailure(stdout.String(), stderr.String()))
	}
	return stdout.String(), nil
}

// cliFailure turns a failed run's output into one readable line. CLIs print
// their result as JSON whose reason (result/subtype/errors) sits at the END of
// a long object, so a plain prefix cut would throw away the only useful part.
func cliFailure(stdout, stderr string) string {
	var res struct {
		Result  string   `json:"result"`
		Subtype string   `json:"subtype"`
		Errors  []string `json:"errors"`
		Error   string   `json:"error"`
	}
	if j := lastJSONObject(stdout); j != "" && json.Unmarshal([]byte(j), &res) == nil {
		parts := []string{}
		for _, p := range append([]string{res.Result, res.Error}, res.Errors...) {
			if p = strings.TrimSpace(p); p != "" && !slices.Contains(parts, p) {
				parts = append(parts, p)
			}
		}
		if len(parts) == 0 && res.Subtype != "" {
			parts = append(parts, res.Subtype)
		}
		if len(parts) > 0 {
			return clip(strings.Join(parts, " · "), 400)
		}
	}
	if msg := strings.TrimSpace(stderr); msg != "" {
		return clip(msg, 400)
	}
	return clip(strings.TrimSpace(stdout), 400)
}

// clip keeps the tail of a long line: the reason a CLI failed is usually there.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// claudeLoggedIn reads `claude auth status --json`. ok is false only when the
// CLI answers and says it is signed out, so a version too old for the
// subcommand never turns a working setup into a sign-in prompt.
func claudeLoggedIn(ctx context.Context, bin string) (ok bool, account string) {
	out, err := run(ctx, bin, "", "auth", "status", "--json")
	if err != nil && strings.TrimSpace(out) == "" {
		return true, ""
	}
	var st struct {
		LoggedIn bool   `json:"loggedIn"`
		Email    string `json:"email"`
		OrgName  string `json:"orgName"`
	}
	if json.Unmarshal([]byte(lastJSONObject(out)), &st) != nil {
		return true, ""
	}
	account = st.Email
	if st.OrgName != "" && account != "" {
		account += " · " + st.OrgName
	}
	return st.LoggedIn, account
}

// ---- Claude Code ----

type claudeCLI struct{ bin string }

func (c *claudeCLI) Check(ctx context.Context) (CheckResult, error) {
	out, err := run(ctx, c.bin, "", "--version")
	if err != nil {
		return CheckResult{}, err
	}
	v := strings.TrimSpace(out)
	// The binary answering says nothing about the account: an expired session
	// only shows up when a prompt is sent, which used to read as "connected".
	if ok, account := claudeLoggedIn(ctx, c.bin); !ok {
		return CheckResult{}, fmt.Errorf("%s · %w: hãy bấm Đăng nhập để ký lại phiên Claude Code%s", v, ErrNeedsLogin, orPrefix(" (tài khoản cũ: ", account, ")"))
	}
	return CheckResult{Version: v, Models: DefaultModelsClaude, Detail: v}, nil
}

func orPrefix(prefix, s, suffix string) string {
	if s == "" {
		return ""
	}
	return prefix + s + suffix
}

// DefaultModelsClaude are offered for CLI providers, which cannot list models.
var DefaultModelsClaude = []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5", "claude-haiku-4-5"}

func (c *claudeCLI) Complete(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	args := []string{"-p", "--output-format", "json", "--max-turns", "1"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.System != "" {
		args = append(args, "--append-system-prompt", req.System)
	}
	// The prompt goes on stdin: argv is visible in ps and has a size limit.
	out, err := run(ctx, c.bin, req.Prompt, args...)
	if err != nil {
		// A signed-out CLI fails before any API call (zero tokens, no cost);
		// say so, so the dashboard can offer to sign in.
		if ok, _ := claudeLoggedIn(ctx, c.bin); !ok {
			return Result{}, fmt.Errorf("%w: phiên Claude Code đã hết hạn, hãy đăng nhập lại", ErrNeedsLogin)
		}
		return Result{}, err
	}
	var res struct {
		Result       string  `json:"result"`
		IsError      bool    `json:"is_error"`
		TotalCostUSD float64 `json:"total_cost_usd"`
		Usage        struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(lastJSONObject(out)), &res); err != nil {
		return Result{}, fmt.Errorf("claude: không đọc được output JSON: %w", err)
	}
	if res.IsError {
		return Result{}, fmt.Errorf("claude: %s", res.Result)
	}
	// Claude Code reports cached prompt tokens separately; count the whole prompt.
	in := res.Usage.InputTokens + res.Usage.CacheReadTokens + res.Usage.CacheCreationTokens
	return Result{Text: res.Result, Model: req.Model, InputTokens: in, OutputTokens: res.Usage.OutputTokens,
		CostUSD: res.TotalCostUSD, DurationMS: time.Since(start).Milliseconds()}, nil
}

// ---- Codex ----

type codexCLI struct{ bin string }

func (c *codexCLI) Check(ctx context.Context) (CheckResult, error) {
	out, err := run(ctx, c.bin, "", "--version")
	if err != nil {
		return CheckResult{}, err
	}
	v := strings.TrimSpace(out)
	if st, _ := run(ctx, c.bin, "", "login", "status"); strings.Contains(strings.ToLower(st), "not logged in") {
		return CheckResult{}, fmt.Errorf("%s · %w: hãy bấm Đăng nhập để ký lại phiên Codex", v, ErrNeedsLogin)
	}
	return CheckResult{Version: v, Detail: v}, nil
}

func (c *codexCLI) Complete(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	args := []string{"exec", "--json", "--skip-git-repo-check", "--sandbox", "read-only"}
	if req.Model != "" {
		args = append(args, "-m", req.Model)
	}
	prompt := req.Prompt
	if req.System != "" {
		prompt = req.System + "\n\n" + req.Prompt
	}
	args = append(args, "-") // read the prompt from stdin
	out, err := run(ctx, c.bin, prompt, args...)
	if err != nil {
		return Result{}, err
	}
	res := Result{Model: req.Model, DurationMS: time.Since(start).Milliseconds()}
	// JSONL events; the event shape has changed across codex versions, so read
	// both the item.* and msg.* styles and keep the last agent message.
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Msg struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"msg"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Item.Type == "agent_message" && ev.Item.Text != "":
			res.Text = ev.Item.Text
		case ev.Msg.Type == "agent_message" && ev.Msg.Message != "":
			res.Text = ev.Msg.Message
		}
		if ev.Usage.InputTokens > 0 || ev.Usage.OutputTokens > 0 {
			res.InputTokens, res.OutputTokens = ev.Usage.InputTokens, ev.Usage.OutputTokens
		}
	}
	if res.Text == "" {
		return Result{}, errors.New("codex: không có câu trả lời trong output")
	}
	return res, nil
}

// lastJSONObject returns the last line that looks like a JSON object, since
// CLIs may print warnings before the result.
func lastJSONObject(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "{") {
			return l
		}
	}
	return strings.TrimSpace(out)
}
