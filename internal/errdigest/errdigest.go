// Package errdigest groups failures by cause (ADR-253): a failed job and a
// failed model call carry only an error string, so the same fault seen many
// times (a broken CLI, a spent quota) reads as many different lines. A
// signature drops what changes between two occurrences (ids, paths, times,
// big numbers) so they fall together; a category names the kind of fault.
package errdigest

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxSignature is the length a signature is cut to (runes).
const MaxSignature = 160

var masks = []struct {
	re   *regexp.Regexp
	put  string
	keep func(string) bool // leave a match as it is (a hex-looking word with no digit)
}{
	{regexp.MustCompile(`(?i)\b(?:https?|wss?|file)://[^\s"'<>)]+`), "<url>", nil},
	{regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "<uuid>", nil},
	{regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`), "<ip>", nil},
	// office ids: job_01m6…, act_01m6…, cnv_…
	{regexp.MustCompile(`\b[a-z]{2,8}_[0-9a-z]{20,}\b`), "<id>", nil},
	// a bare ULID
	{regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`), "<id>", nil},
	// an absolute or home path: at least one more slash after the first
	{regexp.MustCompile(`(?:~|\.{1,2})?/[^\s:'"(),]*?/[^\s:'"(),]*`), "<path>", nil},
	{regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?`), "<time>", nil},
	{regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}(?::\d{2})?(?:\s?[ap]\.?m\.?)?`), "<time>", nil},
	{regexp.MustCompile(`(?i)\b\d+(?:\.\d+)?\s*(?:(?:ms|µs|ns|s|m|h|seconds?|minutes?|hours?|days?|secs?|mins?)\b|giây|phút|giờ|ngày)`), "<dur>", nil},
	// commit SHAs and other hex runs
	{regexp.MustCompile(`\b[0-9a-f]{7,40}\b`), "<hex>", func(w string) bool { return !strings.ContainsAny(w, "0123456789") }},
	// ports, pids, sizes: four digits and more; small numbers (an exit code,
	// an HTTP status) stay, they tell faults apart
	{regexp.MustCompile(`\b\d+\.\d+\b|\b\d{4,}\b`), "<n>", nil},
	{regexp.MustCompile(`\s+`), " ", nil},
}

// Signature is err with what changes between two occurrences of the same
// fault replaced by a mark (<id>, <path>, <time>, <n>…): its first line that
// says something, cut to MaxSignature runes.
func Signature(err string) string {
	line := ""
	for l := range strings.SplitSeq(err, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	for _, m := range masks {
		line = m.re.ReplaceAllStringFunc(line, func(w string) string {
			if m.keep != nil && m.keep(w) {
				return w
			}
			return m.put
		})
	}
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) > MaxSignature {
		line = string([]rune(line)[:MaxSignature-1]) + "…"
	}
	return line
}

// Categories, in the order the dashboard shows their filter.
const (
	CatCLIBroken  = "cli_broken"
	CatNeedsLogin = "needs_login"
	CatQuota      = "quota"
	CatRateLimit  = "rate_limit"
	CatBudget     = "budget"
	CatTimeout    = "timeout"
	CatMCP        = "mcp"
	CatNetwork    = "network"
	CatWorktree   = "worktree"
	CatWorkflow   = "workflow"
	CatScript     = "script"
	CatRestart    = "restart"
	CatCancelled  = "cancelled"
	CatConfig     = "config"
	CatOther      = "other"
)

// AllCategories lists every category.
var AllCategories = []string{CatCLIBroken, CatNeedsLogin, CatQuota, CatRateLimit, CatBudget, CatTimeout, CatMCP,
	CatNetwork, CatWorktree, CatWorkflow, CatScript, CatRestart, CatCancelled, CatConfig, CatOther}

// byCode: a job's error code that already says the kind.
var byCode = map[string]string{
	"cancelled": CatCancelled, "restart": CatRestart, "budget": CatBudget, "out_of_tokens": CatBudget,
	"rate_limit": CatRateLimit, "busy_timeout": CatTimeout, "agent_missing": CatConfig, "disabled": CatConfig,
	"script_error": CatScript,
}

// byText: the words of the office's own errors (internal/llm: ErrCLIBroken,
// ErrNeedsLogin…) and of the CLIs and APIs it runs, first match wins.
var byText = []struct {
	cat   string
	words []string
}{
	// an MCP server that will not start says ENOENT too: it is not the CLI
	{CatMCP, []string{"mcp"}},
	{CatCLIBroken, []string{"cli không chạy được", "không tìm thấy lệnh", "command not found", "cannot find module", " enoent", "executable file not found"}},
	{CatNeedsLogin, []string{"chưa đăng nhập", "đăng nhập lại", "not logged in", "please log in", "please login", "unauthorized", "invalid api key", "invalid_api_key", "no api key", "authentication"}},
	{CatQuota, []string{"usage limit", "quota", "hết lượt", "credit balance", "purchase more credits", "insufficient_quota", "billing"}},
	{CatRateLimit, []string{"rate limit", "rate_limit", "ratelimit", "too many requests", " 429", "overloaded"}},
	{CatBudget, []string{"ngân sách", "budget"}},
	{CatTimeout, []string{"timed out", "timeout", "deadline exceeded", "hết thời gian", "hết giờ", "quá thời gian"}},
	{CatNetwork, []string{"connection refused", "econnrefused", "econnreset", "connection reset", "no such host", "network is unreachable", "tls handshake", " 502", " 503", " 504", "bad gateway", "service unavailable"}},
	{CatWorktree, []string{"worktree"}},
	{CatRestart, []string{"khởi động lại"}},
	{CatCancelled, []string{"đã hủy", "đã huỷ", "context canceled", "cancelled", "canceled"}},
	{CatScript, []string{"script thoát", "script exited"}},
}

// Category is the kind of fault behind a job's error code and message.
func Category(code, msg string) string {
	if c, ok := byCode[code]; ok {
		return c
	}
	low := " " + strings.ToLower(msg)
	// a workflow that did not finish: its own words lead; what broke inside
	// it is read from the rest
	wf := strings.Contains(low, "quy trình") && strings.Contains(low, "không xong")
	for _, b := range byText {
		for _, w := range b.words {
			if strings.Contains(low, w) {
				return b.cat
			}
		}
	}
	if wf {
		return CatWorkflow
	}
	return CatOther
}

// Event is one failure: a failed job, or a failed model call.
type Event struct {
	Error     string
	Code      string // a job's error code
	JobID     string
	ProjectID string
	AgentID   string
	Provider  string // the AI connection, for a model call
	Call      bool   // a model call (not a job)
	At        time.Time
}

// MaxSamples is how many jobs a group links to.
const MaxSamples = 3

// Group is the failures of one cause.
type Group struct {
	Signature  string    `json:"signature"`
	Category   string    `json:"category"`
	Count      int       `json:"count"`
	Jobs       int       `json:"jobs"`  // failed jobs
	Calls      int       `json:"calls"` // failed model calls
	AgentIDs   []string  `json:"agent_ids"`
	Providers  []string  `json:"providers"`
	ProjectIDs []string  `json:"project_ids"`
	First      time.Time `json:"first"`
	Last       time.Time `json:"last"`
	Sample     string    `json:"sample"`  // the latest whole error
	JobIDs     []string  `json:"job_ids"` // the latest jobs, at most MaxSamples
}

// maxSample cuts a sample error (a stack trace can be long).
const maxSample = 2000

// Gather groups events by category and signature, the biggest group first
// (then the latest). A model call whose job also failed with the same
// signature is that job's failure, counted once.
func Gather(events []Event) []Group {
	events = slices.Clone(events)
	slices.SortStableFunc(events, func(a, b Event) int { return a.At.Compare(b.At) }) // oldest first: samples keep the latest
	jobSig := map[string]string{}
	for _, e := range events {
		if !e.Call && e.JobID != "" {
			jobSig[e.JobID] = Signature(e.Error)
		}
	}
	idx := map[string]*Group{}
	var out []*Group
	for _, e := range events {
		sig := Signature(e.Error)
		if e.Call && e.JobID != "" && jobSig[e.JobID] == sig {
			continue
		}
		cat := Category(e.Code, e.Error)
		key := cat + "\x00" + sig
		g := idx[key]
		if g == nil {
			// lists never null: a page reads their length
			g = &Group{Signature: sig, Category: cat, First: e.At, Last: e.At,
				AgentIDs: []string{}, Providers: []string{}, ProjectIDs: []string{}, JobIDs: []string{}}
			idx[key] = g
			out = append(out, g)
		}
		g.Count++
		if e.Call {
			g.Calls++
		} else {
			g.Jobs++
		}
		g.AgentIDs = addOnce(g.AgentIDs, e.AgentID)
		g.Providers = addOnce(g.Providers, e.Provider)
		g.ProjectIDs = addOnce(g.ProjectIDs, e.ProjectID)
		if e.At.Before(g.First) {
			g.First = e.At
		}
		if !e.At.Before(g.Last) {
			g.Last = e.At
			g.Sample = cut(e.Error, maxSample)
		}
		if e.JobID != "" && !slices.Contains(g.JobIDs, e.JobID) {
			g.JobIDs = append(g.JobIDs, e.JobID)
			if len(g.JobIDs) > MaxSamples {
				g.JobIDs = g.JobIDs[1:]
			}
		}
	}
	res := make([]Group, 0, len(out))
	for _, g := range out {
		res = append(res, *g)
	}
	slices.SortStableFunc(res, func(a, b Group) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return b.Last.Compare(a.Last)
	})
	return res
}

func addOnce(list []string, v string) []string {
	if v == "" || slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
