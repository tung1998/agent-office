package llm

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// run executes bin with args, feeding stdin; stderr is kept for error messages.
func run(ctx context.Context, bin string, stdin string, args ...string) (string, error) {
	return runEnv(ctx, bin, stdin, nil, args...)
}

// runEnv is run with extra environment variables.
func runEnv(ctx context.Context, bin string, stdin string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(strings.ToValidUTF8(stdin, "�"))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return "", fmt.Errorf("không tìm thấy lệnh %q trong PATH (%w)", bin, ErrCLIBroken)
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) && ctx.Err() == nil {
		return "", StartError(bin, err) // it never started: a file that is gone, not executable
	}
	if err != nil {
		if CLIBroken(stderr.String()) { // npm's wrapper ran but the native binary is gone
			return stdout.String(), &cliError{msg: CLIBrokenError(bin, stderr.String()).Error(), stderr: stderr.String(), cause: ErrCLIBroken}
		}
		return stdout.String(), &cliError{msg: fmt.Sprintf("%s: %v: %s", bin, err, cliFailure(stdout.String(), stderr.String())), stderr: stderr.String()}
	}
	return stdout.String(), nil
}

// cliError keeps a failed run's whole stderr for CLIs whose reason is not
// at the end (Gemini prints it first, then a stack trace); cause is
// ErrCLIBroken when the CLI never started.
type cliError struct {
	msg, stderr string
	cause       error
}

func (e *cliError) Error() string { return e.msg }
func (e *cliError) Unwrap() error { return e.cause }

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
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return "…" + s[i:]
}

// claudeLoggedIn reads `claude auth status --json`. ok is false only when the
// CLI answers and says it is signed out, so a version too old for the
// subcommand never turns a working setup into a sign-in prompt.
func claudeLoggedIn(ctx context.Context, bin string, env []string) (ok bool, account string) {
	out, err := runEnv(ctx, bin, "", env, "auth", "status", "--json")
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

type claudeCLI struct {
	bin string
	env []string // the connection's own account (ADR-178)
}

func (c *claudeCLI) Check(ctx context.Context) (CheckResult, error) {
	out, err := run(ctx, c.bin, "", "--version")
	if err != nil {
		return CheckResult{}, err
	}
	v := strings.TrimSpace(out)
	// The binary answering says nothing about the account: an expired session
	// only shows up when a prompt is sent, which used to read as "connected".
	if ok, account := claudeLoggedIn(ctx, c.bin, c.env); !ok {
		return CheckResult{}, fmt.Errorf("%s · %w: hãy bấm Đăng nhập để ký lại phiên Claude Code%s", v, ErrNeedsLogin, orPrefix(" (tài khoản cũ: ", account, ")"))
	}
	return CheckResult{Version: v, Models: claudeModels(ctx, c.bin), Detail: v}, nil
}

func orPrefix(prefix, s, suffix string) string {
	if s == "" {
		return ""
	}
	return prefix + s + suffix
}

// claudeAliases is what Claude Code always accepts for --model; each alias
// follows the newest model of its family, so the list never goes stale.
var claudeAliases = []string{"opus", "sonnet", "haiku"}

// claudeModels: Claude Code cannot list models, so offer the aliases its
// `--help` names for --model (new families such as 'fable' show up there),
// plus the aliases it always accepts.
func claudeModels(ctx context.Context, bin string) []string {
	help, _ := run(ctx, bin, "", "--help")
	return mergeModels(helpAliases(help, "--model"), claudeAliases)
}

// helpAliases reads the 'quoted' examples in the help of one flag.
func helpAliases(help, flag string) []string {
	i := strings.Index(help, flag+" ")
	if i < 0 {
		return nil
	}
	block := help[i+len(flag):]
	// the flag's text ends where the next flag starts
	if j := regexp.MustCompile(`\n\s*-`).FindStringIndex(block); j != nil {
		block = block[:j[0]]
	}
	var out []string
	for _, m := range quotedAlias.FindAllStringSubmatch(block, -1) {
		out = append(out, m[1])
	}
	return out
}

var quotedAlias = regexp.MustCompile(`'([a-z][a-z0-9.\-]*)'`)

// mergeModels joins lists in order, without duplicates.
func mergeModels(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, m := range l {
			if m != "" && !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	return out
}

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
	out, err := runEnv(ctx, c.bin, req.Prompt, c.env, args...)
	if err != nil {
		// A signed-out CLI fails before any API call (zero tokens, no cost);
		// say so, so the dashboard can offer to sign in.
		if ok, _ := claudeLoggedIn(ctx, c.bin, c.env); !ok {
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
	if err := json.Unmarshal([]byte(claudeResultJSON(out)), &res); err != nil {
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

type codexCLI struct {
	bin string
	env []string // the connection's own account (ADR-178)
}

func (c *codexCLI) Check(ctx context.Context) (CheckResult, error) {
	out, err := run(ctx, c.bin, "", "--version")
	if err != nil {
		return CheckResult{}, err
	}
	v := strings.TrimSpace(out)
	home := CodexHomeIn(c.env)
	if st, _ := runEnv(ctx, c.bin, "", c.env, "login", "status"); strings.Contains(strings.ToLower(st), "not logged in") {
		return CheckResult{}, fmt.Errorf("%s · %w: hãy bấm Đăng nhập để ký lại phiên Codex", v, ErrNeedsLogin)
	}
	expired, detail := CodexSessionExpired(home, time.Now())
	if !expired {
		expired, detail = CodexRejected(home)
	}
	if expired {
		return CheckResult{}, fmt.Errorf("%s · %w: %s, hãy bấm Đăng nhập để ký lại phiên Codex", v, ErrNeedsLogin, detail)
	}
	return CheckResult{Version: v, Models: codexModels(ctx, c.bin, c.env), Detail: v}, nil
}

// codexModels lists the models Codex offers in its picker, best first
// (`codex debug models` prints its catalog); nil when it cannot tell.
func codexModels(ctx context.Context, bin string, env []string) []string {
	out, err := runEnv(ctx, bin, "", env, "debug", "models")
	if err != nil {
		return nil
	}
	type model struct {
		Slug       string `json:"slug"`
		Visibility string `json:"visibility"`
		Priority   int    `json:"priority"`
	}
	var cat struct {
		Models []model `json:"models"`
	}
	if json.Unmarshal([]byte(out), &cat) != nil {
		return nil
	}
	slices.SortStableFunc(cat.Models, func(a, b model) int { return cmp.Compare(a.Priority, b.Priority) })
	var ms []string
	for _, m := range cat.Models {
		if m.Slug != "" && m.Visibility == "list" {
			ms = append(ms, m.Slug)
		}
	}
	return ms
}

// codexStaleAfter: a ChatGPT session whose access token expired and was not
// refreshed for this long is treated as signed out (its refresh token is
// revoked long before; codex refreshes an active session every few days).
const codexStaleAfter = 30 * 24 * time.Hour

// CodexSessionExpired reads Codex's credentials file. `codex login status`
// only checks the file exists, so a session idle for months still reads
// "Logged in" while every request fails with "token could not be refreshed".
// No file (credentials in the keyring) or an API key is not expired.
func CodexSessionExpired(home string, now time.Time) (bool, string) {
	raw, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return false, ""
	}
	var a struct {
		APIKey string `json:"OPENAI_API_KEY"`
		Tokens *struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
		LastRefresh time.Time `json:"last_refresh"`
	}
	if json.Unmarshal(raw, &a) != nil || a.APIKey != "" || a.Tokens == nil {
		return false, ""
	}
	exp := jwtExpiry(a.Tokens.AccessToken)
	if !exp.IsZero() && now.After(exp) && now.Sub(a.LastRefresh) > codexStaleAfter {
		return true, "phiên ChatGPT đã hết hạn từ " + exp.Format("2006-01-02")
	}
	return false, ""
}

// codexRejectedFile marks that a real call had the ChatGPT session refused.
// The token can still look valid (revoked by the server, e.g. a sign-in
// elsewhere), so `codex login status` keeps saying "Logged in". A file, so
// the mark outlives an office restart.
func codexRejectedFile(home string) string { return filepath.Join(home, ".office-rejected") }

// MarkCodexRejected records that the server refused the Codex session kept in home.
func MarkCodexRejected(home string) { _ = os.WriteFile(codexRejectedFile(home), nil, 0o600) }

// CodexRejected: a real call was refused and auth.json has not changed since
// (a new sign-in rewrites it).
func CodexRejected(home string) (bool, string) {
	mark, err := os.Stat(codexRejectedFile(home))
	if err != nil {
		return false, ""
	}
	auth, err := os.Stat(filepath.Join(home, "auth.json"))
	if err != nil || auth.ModTime().After(mark.ModTime()) {
		return false, ""
	}
	return true, "máy chủ từ chối phiên ChatGPT lúc " + mark.ModTime().Format("15:04 02/01")
}

// CodexHome is where Codex keeps its config, credentials and sessions.
func CodexHome() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// CodexHomeIn is Codex's folder for a run with env (the CODEX_HOME of a
// connection with an account of its own, ADR-178), else the machine's.
func CodexHomeIn(env []string) string {
	if v := envValue(env, "CODEX_HOME"); v != "" {
		return v
	}
	return CodexHome()
}

// envValue is the value env gives key (the last one, as exec does), "" if none.
func envValue(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], key+"="); ok {
			return v
		}
	}
	return ""
}

// jwtExpiry reads a JWT's exp claim without verifying it (zero if unreadable).
func jwtExpiry(tok string) time.Time {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}
	}
	var c struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(c.Exp, 0)
}

// CodexFailure is codexFailure for the chat runner.
func CodexFailure(out string) string { return codexFailure(out) }

// CodexAuthFailed: msg says the ChatGPT session is dead (sign in again).
func CodexAuthFailed(msg string) bool { return codexAuthFailed.MatchString(msg) }

// codexFailure is the reason in a failed `codex exec --json` run: the
// turn.failed (or last error) event, which comes after the thread/turn
// started events a plain prefix cut would show instead.
func codexFailure(out string) string {
	msg := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "turn.failed" && ev.Error.Message != "":
			return ev.Error.Message
		case ev.Type == "error" && ev.Message != "":
			msg = ev.Message
		}
	}
	return msg
}

// codexAuthFailed: codex's own wording when the ChatGPT session is dead.
var codexAuthFailed = regexp.MustCompile(`(?i)could not be refreshed|token_expired|sign(ing)? in again|log out and sign in|unauthorized \(401\)|status 401|401 unauthorized|invalidated oauth token`)

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
	out, err := runEnv(ctx, c.bin, prompt, c.env, args...)
	if err != nil {
		var ce *cliError
		if errors.As(err, &ce) && codexAuthFailed.MatchString(ce.stderr) {
			MarkCodexRejected(CodexHomeIn(c.env))
			return Result{}, fmt.Errorf("%w: phiên Codex đã hết hạn, hãy đăng nhập lại", ErrNeedsLogin)
		}
		if msg := codexFailure(out); msg != "" {
			if codexAuthFailed.MatchString(msg) {
				MarkCodexRejected(CodexHomeIn(c.env))
				return Result{}, fmt.Errorf("%w: phiên Codex đã hết hạn, hãy đăng nhập lại", ErrNeedsLogin)
			}
			return Result{}, fmt.Errorf("codex: %s", msg)
		}
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
	if err := sc.Err(); err != nil {
		return Result{}, fmt.Errorf("codex: đọc output lỗi: %w", err)
	}
	if res.Text == "" {
		return Result{}, errors.New("codex: không có câu trả lời trong output")
	}
	return res, nil
}

// ---- Gemini ----

type geminiCLI struct {
	bin string
	env []string // the connection's own account (ADR-178)
}

// geminiAliases are Gemini CLI's model aliases: they follow its newest models.
var geminiAliases = []string{"auto", "pro", "flash", "flash-lite"}

// geminiModels: Gemini CLI cannot list models. Its aliases always work; with
// an API key the account's real models come from the Gemini API.
func geminiModels(ctx context.Context, dir string) []string {
	return mergeModels(geminiAliases, geminiAPIModels(ctx, geminiAPIBase, geminiAPIKey(dir)))
}

var geminiAPIBase = "https://generativelanguage.googleapis.com"

// geminiAPIModels lists the chat models an API key can use, newest first
// (nil without a key or when the API does not answer).
func geminiAPIModels(ctx context.Context, base, key string) []string {
	if key == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out struct {
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	hc := &http.Client{}
	if err := doJSON(ctx, hc, http.MethodGet, base+"/v1beta/models?pageSize=1000", map[string]string{"x-goog-api-key": key}, nil, &out); err != nil {
		return nil
	}
	var ms []string
	for _, m := range out.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		if strings.HasPrefix(id, "gemini-") && slices.Contains(m.Methods, "generateContent") && !notChat.MatchString(id) {
			ms = append(ms, id)
		}
	}
	slices.Sort(ms)
	slices.Reverse(ms)
	return ms
}

// geminiAPIKey is the key Gemini CLI would use: the environment, then
// ~/.gemini/.env.
func geminiAPIKey(dir string) string {
	for _, k := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if m := geminiEnvValue.FindSubmatch(env); m != nil {
		return strings.Trim(string(m[3]), `"'`)
	}
	return ""
}

var geminiEnvValue = regexp.MustCompile(`(?m)^\s*(export\s+)?(GEMINI_API_KEY|GOOGLE_API_KEY)\s*=\s*(\S+)`)

// GeminiDir is Gemini CLI's config folder for a run with env (the
// GEMINI_CLI_HOME of a connection with an account of its own, ADR-178),
// else the machine's.
func GeminiDir(env []string) string {
	home := envValue(env, "GEMINI_CLI_HOME")
	if home == "" {
		home = os.Getenv("GEMINI_CLI_HOME")
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".gemini")
}

// GeminiSignedIn reports whether Gemini CLI has credentials: a Google sign-in
// cached in ~/.gemini, or an API key / Vertex / Code Assist setup in the
// environment or ~/.gemini/.env.
func GeminiSignedIn(dir string) bool {
	ok, _ := GeminiAccount(dir)
	return ok
}

// GeminiAccount is GeminiSignedIn plus the Google account Gemini CLI signed
// in with ("" for a key from the environment or an unknown account).
func GeminiAccount(dir string) (bool, string) {
	for _, k := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_USE_VERTEXAI", "GOOGLE_GENAI_USE_GCA", "GOOGLE_CLOUD_ACCESS_TOKEN"} {
		if os.Getenv(k) != "" {
			return true, ""
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "oauth_creds.json")); err == nil {
		var acc struct {
			Active string `json:"active"`
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "google_accounts.json"))
		_ = json.Unmarshal(raw, &acc)
		return true, acc.Active
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	return geminiEnvKey.Match(env), ""
}

var geminiEnvKey = regexp.MustCompile(`(?m)^\s*(export\s+)?(GEMINI_API_KEY|GOOGLE_API_KEY)\s*=\s*\S`)

func (c *geminiCLI) Check(ctx context.Context) (CheckResult, error) {
	out, err := run(ctx, c.bin, "", "--version")
	if err != nil {
		return CheckResult{}, err
	}
	v := strings.TrimSpace(out)
	if !GeminiSignedIn(GeminiDir(c.env)) {
		return CheckResult{}, fmt.Errorf("%s · %w: hãy bấm Đăng nhập (tài khoản Google) hoặc đặt GEMINI_API_KEY", v, ErrNeedsLogin)
	}
	return CheckResult{Version: v, Models: geminiModels(ctx, GeminiDir(c.env)), Detail: v}, nil
}

func (c *geminiCLI) Complete(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	// headless because stdin is not a terminal; default approval keeps it to reading
	args := []string{"--output-format", "json", "--approval-mode", "default"}
	if req.Model != "" {
		args = append(args, "-m", req.Model)
	}
	prompt := req.Prompt
	if req.System != "" {
		prompt = req.System + "\n\n" + req.Prompt
	}
	out, err := runEnv(ctx, c.bin, prompt, append([]string{"GEMINI_CLI_TRUST_WORKSPACE=true"}, c.env...), args...)
	var res struct {
		Response string `json:"response"`
		Error    *struct {
			Message string `json:"message"`
		} `json:"error"`
		Stats struct {
			Models map[string]struct {
				Tokens struct {
					Prompt     int `json:"prompt"`
					Candidates int `json:"candidates"`
				} `json:"tokens"`
			} `json:"models"`
		} `json:"stats"`
	}
	jerr := json.Unmarshal([]byte(lastJSONObject(out)), &res)
	if err != nil || jerr != nil || res.Error != nil {
		var ce *cliError
		if errors.As(err, &ce) && res.Error == nil {
			if msg := GeminiError(ce.stderr); msg != "" {
				return Result{}, fmt.Errorf("gemini: %s", msg)
			}
		}
		if !GeminiSignedIn(GeminiDir(c.env)) {
			return Result{}, fmt.Errorf("%w: Gemini CLI chưa đăng nhập", ErrNeedsLogin)
		}
		if res.Error != nil && res.Error.Message != "" {
			return Result{}, fmt.Errorf("gemini: %s", clip(res.Error.Message, 400))
		}
		if err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("gemini: không đọc được output JSON: %w", jerr)
	}
	r := Result{Text: strings.TrimSpace(res.Response), Model: req.Model, DurationMS: time.Since(start).Milliseconds()}
	for m, s := range res.Stats.Models {
		r.InputTokens += s.Tokens.Prompt
		r.OutputTokens += s.Tokens.Candidates
		if r.Model == "" {
			r.Model = m
		}
	}
	if r.Text == "" {
		return Result{}, errors.New("gemini: không có câu trả lời trong output")
	}
	return r, nil
}

var (
	ansiColor  = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	geminiFail = regexp.MustCompile(`(?:Error[^:]*|[A-Za-z]+Error): (.+)$`)
)

// GeminiError reads why Gemini CLI failed from its stderr: the first error
// line without its "Error authenticating: XError:" prefixes and the stack
// trace after it, with a hint for the refusals people can act on.
func GeminiError(stderr string) string {
	var lines []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(ansiColor.ReplaceAllString(l, ""))
		if l != "" && !strings.HasPrefix(l, "at ") && !slices.Contains(lines, l) {
			lines = append(lines, l)
		}
	}
	msg := ""
	for _, l := range lines {
		if m := geminiFail.FindStringSubmatch(l); m != nil {
			msg = m[1]
			for { // "Error authenticating: IneligibleTierError: reason"
				n := geminiFail.FindStringSubmatch(msg)
				if n == nil {
					break
				}
				msg = n[1]
			}
			break
		}
	}
	if msg == "" {
		msg = strings.Join(lines, "\n")
	}
	switch {
	case strings.Contains(stderr, "UNSUPPORTED_CLIENT") || strings.Contains(stderr, "IneligibleTierError"):
		msg += " — Google không còn cho Gemini CLI dùng tài khoản Google cá nhân (gói miễn phí): dùng API key (GEMINI_API_KEY từ aistudio.google.com/apikey) hoặc gói Code Assist có dự án Google Cloud (GOOGLE_CLOUD_PROJECT)."
	case strings.Contains(stderr, "ProjectIdRequiredError"):
		msg += " — tài khoản này cần dự án Google Cloud: đặt GOOGLE_CLOUD_PROJECT cho office."
	}
	return clip(msg, 600)
}

// ---- Antigravity ----

type antigravityCLI struct{ bin string }

// agyModel is a model slug in `agy models` output.
var agyModel = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)+$`)

// AntigravityModels lists the models of the signed-in account (`agy models`);
// a signed-out CLI gives ErrNeedsLogin.
func AntigravityModels(ctx context.Context, bin string, env []string) ([]string, error) {
	cmd := exec.CommandContext(ctx, bin, "models")
	if len(env) > 0 {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	text := string(out)
	if strings.Contains(strings.ToLower(text), "sign in") || strings.Contains(text, "authentication") {
		return nil, fmt.Errorf("%w: hãy bấm Đăng nhập (tài khoản Google) cho Antigravity CLI", ErrNeedsLogin)
	}
	if err != nil {
		return nil, fmt.Errorf("agy models: %v: %s", err, clip(strings.TrimSpace(text), 300))
	}
	var models []string
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(strings.Trim(l, " \t-*•>"))
		if len(f) > 0 && agyModel.MatchString(f[0]) && !slices.Contains(models, f[0]) {
			models = append(models, f[0])
		}
	}
	return models, nil
}

func (c *antigravityCLI) Check(ctx context.Context) (CheckResult, error) {
	out, err := run(ctx, c.bin, "", "--version")
	if err != nil {
		return CheckResult{}, err
	}
	v := strings.TrimSpace(out)
	models, err := AntigravityModels(ctx, c.bin, nil)
	if err != nil {
		return CheckResult{}, fmt.Errorf("%s · %w", v, err)
	}
	return CheckResult{Version: v, Models: models, Detail: v}, nil
}

// agyResult is Antigravity's json output (and the payload of its
// stream-json "result" event).
type agyResult struct {
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
}

func (c *antigravityCLI) Complete(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	// the prompt on stdin runs one turn non-interactively; plan mode only reads
	args := []string{"--output-format", "json", "--mode", "plan", "--disable-slash-commands"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	prompt := req.Prompt
	if req.System != "" {
		prompt = req.System + "\n\n" + req.Prompt
	}
	out, err := run(ctx, c.bin, prompt, args...)
	var res agyResult
	if jerr := json.Unmarshal([]byte(lastJSONObject(out)), &res); jerr != nil {
		if err != nil {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("agy: không đọc được output JSON: %w", jerr)
	}
	if res.Status != "SUCCESS" {
		if strings.Contains(res.Error, "authentication") {
			return Result{}, fmt.Errorf("%w: Antigravity CLI chưa đăng nhập", ErrNeedsLogin)
		}
		return Result{}, fmt.Errorf("agy: %s", clip(cmp.Or(res.Error, res.Status), 400))
	}
	return Result{Text: strings.TrimSpace(res.Response), Model: req.Model, DurationMS: time.Since(start).Milliseconds(),
		InputTokens: res.Usage.InputTokens + res.Usage.CacheReadTokens, OutputTokens: res.Usage.OutputTokens + res.Usage.ThinkingTokens}, nil
}

// claudeResultJSON is the result object of `claude -p --output-format json`.
// With verbose on (in the person's settings.json or env), Claude Code prints
// every message as one JSON array instead: take its last "result" element.
func claudeResultJSON(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(strings.TrimSpace(l), "[") {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal([]byte(strings.Join(lines[i:], "\n")), &items) != nil {
			continue
		}
		for j := len(items) - 1; j >= 0; j-- {
			var m struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(items[j], &m) == nil && m.Type == "result" {
				return string(items[j])
			}
		}
	}
	return lastJSONObject(out)
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
