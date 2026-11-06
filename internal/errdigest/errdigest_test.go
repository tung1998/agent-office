package errdigest

import (
	"testing"
	"time"
)

// The same fault seen twice, with only its ids, paths, times or big numbers
// changed, is one signature (ADR-253).
func TestSignatureStable(t *testing.T) {
	pairs := [][2]string{
		{"codex: Error: spawn /Users/a/.nvm/versions/node/v25.9.0/lib/node_modules/@openai/codex/vendor/codex ENOENT\n    at ChildProcess._handle.onexit (node:internal/child_process:285:19)",
			"codex: Error: spawn /Users/b/.volta/tools/codex/vendor/codex ENOENT\n    at x"},
		{"codex: You’ve hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/settings/usage to purchase more credits or try again at 9:18 PM.",
			"codex: You’ve hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/settings/usage to purchase more credits or try again at 5:00 AM."},
		{"job job_01m6p83pqz5a1m2kx7v8w9y0ab bị kẹt sau 45s", "job job_01m6p9zzzz5a1m2kx7v8w9y0cd bị kẹt sau 2m"},
		{"dial tcp 127.0.0.1:27041: connect: connection refused", "dial tcp 127.0.0.1:61999: connect: connection refused"},
		{"merge 3f9a1c2d4e failed at 2026-11-05T10:01:02Z", "merge 88aa77bb66 failed at 2026-11-04T23:59:59+07:00"},
		{"request 1b4e28ba-2fa1-11d2-883f-0016d3cca427 rejected", "request 6fa459ea-ee8a-3ca4-894e-db77e160355e rejected"},
		{"chờ quá 30 phút", "chờ quá 15 phút"},
	}
	for _, p := range pairs {
		a, b := Signature(p[0]), Signature(p[1])
		if a != b {
			t.Errorf("not one signature:\n %q\n %q", a, b)
		}
	}
}

// Different faults stay apart: an exit code or an HTTP status is not masked,
// nor is a word that only looks like hex.
func TestSignatureKeepsWhatTellsApart(t *testing.T) {
	for _, p := range [][2]string{
		{"claude: exit status 1", "claude: exit status 2"},
		{"HTTP 429 from api", "HTTP 503 from api"},
		{"script thoát với mã 1", "các kết nối AI của agent đều đang tắt"},
	} {
		if Signature(p[0]) == Signature(p[1]) {
			t.Errorf("%q and %q gave one signature %q", p[0], p[1], Signature(p[0]))
		}
	}
	if s := Signature("the record was effaced"); s != "the record was effaced" {
		t.Errorf("a hex-looking word was masked: %q", s)
	}
	if s := Signature("\n\n  first line  \nsecond"); s != "first line" {
		t.Errorf("first line = %q", s)
	}
	long := ""
	for range 300 {
		long += "x"
	}
	if r := []rune(Signature(long)); len(r) != MaxSignature {
		t.Errorf("signature not cut: %d runes", len(r))
	}
}

func TestCategory(t *testing.T) {
	cases := []struct{ code, msg, want string }{
		{"agent_error", "codex: Error: spawn /Users/me/.nvm/x/codex ENOENT", CatCLIBroken},
		{"agent_error", "claude: CLI không chạy được (cài lại: npm install -g @anthropic-ai/claude-code, rồi bấm Kiểm tra ở Kết nối AI)", CatCLIBroken},
		{"agent_error", "gemini: Error: Cannot find module '/opt/gemini/dist/index.js'", CatCLIBroken},
		{"agent_error", "chưa đăng nhập: Claude Code trên máy chưa đăng nhập hoặc phiên đã hết hạn. Vào Kết nối AI, bấm Kiểm tra rồi Đăng nhập", CatNeedsLogin},
		{"agent_error", "chưa đăng nhập: phiên Codex đã hết hạn, hãy đăng nhập lại", CatNeedsLogin},
		{"agent_error", "codex: You’ve hit your usage limit. Upgrade to Pro", CatQuota},
		{"", "openai: 429 insufficient_quota", CatQuota},
		{"rate_limit", "anything", CatRateLimit},
		{"agent_error", "anthropic: 429 Too Many Requests", CatRateLimit},
		{"api_transient", "anything", CatTransient},
		{"agent_error", "claude: exit status 1 · lỗi API cuối: 529 overloaded_error, lần 10/10", CatTransient},
		{"agent_error", "claude: API Error: 529 {\"type\":\"overloaded_error\"}", CatTransient},
		{"budget", "", CatBudget},
		{"out_of_tokens", "", CatBudget},
		{"busy_timeout", "", CatTimeout},
		{"agent_error", "context deadline exceeded", CatTimeout},
		{"agent_error", "mcp server sentry: spawn npx ENOENT", CatMCP},
		{"agent_error", "dial tcp 127.0.0.1:2704: connect: connection refused", CatNetwork},
		{"agent_error", "upstream 503 Service Unavailable", CatNetwork},
		{"agent_error", "worktree .office/worktrees/x is gone", CatWorktree},
		{"agent_error", "⚠️ Quy trình Kiểm tra sau deploy không xong: đầu ra không hợp lệ trong outputs: summary (thiếu)", CatWorkflow},
		{"agent_error", "⚠️ Quy trình Nâng cấp không xong: codex: You’ve hit your usage limit", CatQuota},
		{"script_error", "script thoát với mã 1", CatScript},
		{"", "script thoát với mã 2", CatScript},
		{"restart", "office khởi động lại khi job đang chạy", CatRestart},
		{"", "office khởi động lại khi job đang chạy", CatRestart},
		{"cancelled", "", CatCancelled},
		{"agent_missing", "", CatConfig},
		{"agent_error", "claude: exit status 1", CatOther},
	}
	for _, c := range cases {
		if got := Category(c.code, c.msg); got != c.want {
			t.Errorf("Category(%q, %q) = %s, want %s", c.code, c.msg, got, c.want)
		}
	}
	for _, c := range AllCategories {
		if c == "" {
			t.Fatal("empty category")
		}
	}
}

func TestGather(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC)
	enoent := func(user string) string { return "codex: Error: spawn /Users/" + user + "/codex/bin/codex ENOENT" }
	ev := []Event{
		// newest first on purpose: Gather must not depend on the order
		{Error: enoent("c"), Call: true, AgentID: "agt_ai", Provider: "openAI", ProjectID: "p1", At: t0.Add(5 * time.Minute)},
		{Error: "claude: exit status 1", Code: "agent_error", JobID: "j4", AgentID: "agt_x", ProjectID: "p1", At: t0.Add(4 * time.Minute)},
		{Error: enoent("b"), Call: true, AgentID: "agt_ai", Provider: "openAI", JobID: "j9", ProjectID: "p1", At: t0.Add(3 * time.Minute)},
		{Error: enoent("a"), Code: "agent_error", JobID: "j1", AgentID: "agt_ai", ProjectID: "p1", At: t0.Add(2 * time.Minute)},
		// the model call of j1: the same fault, counted once
		{Error: enoent("a"), Call: true, JobID: "j1", AgentID: "agt_ai", Provider: "openAI", ProjectID: "p1", At: t0.Add(2 * time.Minute)},
		{Error: enoent("z"), Call: true, JobID: "j0", AgentID: "agt_ai", Provider: "openAI", ProjectID: "p2", At: t0},
	}
	gs := Gather(ev)
	if len(gs) != 2 {
		t.Fatalf("groups = %d, want 2: %+v", len(gs), gs)
	}
	g := gs[0]
	if g.Category != CatCLIBroken || g.Count != 4 || g.Jobs != 1 || g.Calls != 3 {
		t.Fatalf("first group = %+v", g)
	}
	if !g.First.Equal(t0) || !g.Last.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("first/last = %v %v", g.First, g.Last)
	}
	if g.Sample != enoent("c") {
		t.Fatalf("sample is not the latest: %q", g.Sample)
	}
	if len(g.JobIDs) != 3 || g.JobIDs[0] != "j0" || g.JobIDs[2] != "j9" {
		t.Fatalf("job ids = %v", g.JobIDs)
	}
	if len(g.ProjectIDs) != 2 || len(g.Providers) != 1 || len(g.AgentIDs) != 1 {
		t.Fatalf("lists = %+v", g)
	}
	if gs[1].Signature != "claude: exit status 1" || gs[1].Count != 1 {
		t.Fatalf("second group = %+v", gs[1])
	}
	// a group of model calls with no job: its lists are empty, never null
	only := Gather([]Event{{Error: "boom", Call: true, At: t0}})
	if only[0].JobIDs == nil || only[0].AgentIDs == nil || only[0].Providers == nil || only[0].ProjectIDs == nil {
		t.Fatalf("null lists: %+v", only[0])
	}
}

// The one definition of a passing failure every layer reads (ADR-260).
func TestTransientText(t *testing.T) {
	for msg, want := range map[string]bool{
		"claude: exit status 1 · lỗi API cuối: 529 overloaded_error, lần 10/10": true,
		"codex: unexpected status 502 Bad Gateway":                              true,
		"gemini: 503 Service Unavailable":                                       true,
		"read: connection reset by peer":                                        true,
		"codex: stream disconnected before completion":                          true,
		"claude: API Error: 500 Internal server error":                          true,
		"codex: You've hit your usage limit · 503":                              false, // quota first
		"anthropic: 429 Too Many Requests overloaded":                           false,
		"claude: not logged in · 503 Service Unavailable":                       false,
		"mcp server sentry: connection refused":                                 false,
		"codex: Error: spawn /x/codex ENOENT":                                   false,
		"claude: exit status 1":                                                 false,
		"go test: sau 500ms":                                                    false,
	} {
		if got := TransientText(msg); got != want {
			t.Errorf("TransientText(%q) = %v, want %v", msg, got, want)
		}
	}
}

// A group says whose automation's runs failed so, a workflow step it started
// included (ADR-263).
func TestGatherAutomations(t *testing.T) {
	now := time.Now()
	g := Gather([]Event{{Error: "boom", JobID: "j1", Automation: "aut_a", At: now}, {Error: "boom", JobID: "j2", At: now}, {Error: "boom", JobID: "j3", Automation: "aut_a", At: now}})
	if len(g) != 1 || len(g[0].AutomationIDs) != 1 || g[0].AutomationIDs[0] != "aut_a" {
		t.Fatalf("groups = %+v", g)
	}
}
