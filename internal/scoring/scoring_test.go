package scoring

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestClassify(t *testing.T) {
	long := strings.Repeat("chữ ", 400)
	for _, c := range []struct {
		text       string
		src        Source
		kind, diff string
	}{
		{"chạy báo cáo", Source{Purpose: "automation"}, "automation", "medium"},
		{"x", Source{Workflow: true}, "workflow", "medium"},
		{"x", Source{Burn: true}, "burn", "medium"},
		{"Soát lại PR này giúp mình", Source{}, "review", "medium"},
		{"review the diff", Source{}, "review", "medium"},
		{"Sửa lỗi đăng nhập bị treo", Source{}, "bugfix", "medium"},
		{"fix the crash on save", Source{}, "bugfix", "medium"},
		{"Thêm tính năng xuất CSV", Source{}, "feature", "medium"},
		{"triển khai", Source{}, "feature", "medium"},
		{"implement dark mode", Source{}, "feature", "medium"},
		{"ADR-090 là gì?", Source{}, "ask", "light"},
		{"ADR-090 là gì?", Source{Files: 1}, "ask", "medium"},
		{"Refactor toàn bộ storage", Source{}, "feature", "heavy"},
		{long, Source{}, "ask", "heavy"},
		{"Sửa lỗi", Source{Files: 6}, "bugfix", "heavy"},
	} {
		k, d := Classify(c.text, c.src)
		if k != c.kind || d != c.diff {
			t.Errorf("Classify(%.30q, %+v) = %s/%s, want %s/%s", c.text, c.src, k, d, c.kind, c.diff)
		}
	}
}

func TestCorrective(t *testing.T) {
	for text, want := range map[string]bool{
		"sai rồi": true, "Chưa đúng, sửa lại đi": true, "không phải vậy": true, "that's wrong": true, "làm lại": true,
		"cảm ơn": false, "tiếp đi": false, "ok triển khai": false, "": false,
	} {
		if got := Corrective(text); got != want {
			t.Errorf("Corrective(%q) = %v", text, got)
		}
	}
}

func bp(b bool) *bool { return &b }

func TestQuality(t *testing.T) {
	q := func(p storage.ScoreParts) int {
		v := Quality(p)
		if v == nil {
			return -1
		}
		return *v
	}
	all := storage.ScoreParts{Merged: bp(true), NoRework: bp(true), NoCorrection: bp(true), ChecksGreen: bp(true)}
	if got := q(all); got != 100 {
		t.Fatalf("all = %d", got)
	}
	all.NoCorrection = bp(false)
	if got := q(all); got != 80 {
		t.Fatalf("corrected = %d", got)
	}
	if got := q(storage.ScoreParts{NoRework: bp(true), NoCorrection: bp(false)}); got != 50 {
		t.Fatalf("partial = %d", got)
	}
	if got := q(storage.ScoreParts{}); got != -1 {
		t.Fatalf("none = %d", got)
	}
}

func TestPriorsNoMax(t *testing.T) {
	if len(Priors()) == 0 {
		t.Fatal("no priors")
	}
	for _, p := range Priors() {
		if p.Source == "" || p.Q <= 0 {
			t.Fatalf("prior without source/Q: %+v", p)
		}
	}
	if p := PriorFor("claude-opus-5-5", "medium", "strong"); p.CostUSD != 0.97 {
		t.Fatalf("exact prior = %+v", p)
	}
	if p := PriorFor("unknown-model", "", "fast"); p.Q != 70 || !p.Estimate {
		t.Fatalf("estimate prior = %+v", p)
	}
}

func TestAggregateBlendsPrior(t *testing.T) {
	q := 100
	s := storage.JobScore{AgentID: "a", ProviderID: "p", Model: "claude-sonnet-5-5", Effort: "medium", TaskKind: "bugfix", Difficulty: "medium", Q: &q, CostUSD: 0.4, DurationMS: 1000}
	nilQ := s
	nilQ.Q = nil
	cells := Aggregate([]storage.JobScore{s, nilQ}, func(string) string { return "balanced" })
	if len(cells) != 1 {
		t.Fatalf("cells = %+v", cells)
	}
	c := cells[0]
	pr := PriorFor("claude-sonnet-5-5", "medium", "balanced")
	want := (100 + pr.Q*PriorWeight) / (1 + PriorWeight)
	if c.N != 1 || math.Abs(c.Q-want) > 0.01 || !c.FromPrior {
		t.Fatalf("cell = %+v want Q %.2f", c, want)
	}
}

func cell(model, effort string, q, cost float64, ms int64) Cell {
	return Cell{Config: Config{AgentID: "a-" + model, ProviderID: "p-" + model, Model: model, Effort: effort}, Kind: "bugfix", Difficulty: "medium", N: 10, Q: q, CostUSD: cost, DurationMS: ms}
}

func TestBestThresholdAndTie(t *testing.T) {
	cells := []Cell{cell("a", "medium", 90, 1.00, 5000), cell("b", "medium", 85, 0.95, 2000), cell("c", "low", 70, 0.10, 100)}
	if b, ok := Best(cells, "bugfix", "medium"); !ok || b.Model != "b" {
		t.Fatalf("tie → faster: %+v", b)
	}
	cells[1].CostUSD = 0.5
	if b, _ := Best(cells, "bugfix", "medium"); b.Model != "b" {
		t.Fatalf("cheaper: %+v", b)
	}
	low := []Cell{cell("a", "medium", 60, 1, 1), cell("b", "medium", 75, 2, 1)}
	if b, _ := Best(low, "bugfix", "medium"); b.Model != "b" {
		t.Fatalf("none ≥80 → highest Q: %+v", b)
	}
	if _, ok := Best(cells, "ask", "light"); ok {
		t.Fatal("empty group")
	}
}

func TestRankQuota(t *testing.T) {
	cells := []Cell{cell("over", "medium", 95, 0.1, 1), cell("hot", "medium", 90, 0.2, 1), cell("cool", "medium", 85, 0.6, 1), cell("maxed", "max", 99, 0.01, 1)}
	quota := map[string]Quota{"p-over": {Over: true}, "p-hot": {MaxUtil: 0.85}, "p-cool": {MaxUtil: 0.5}}
	got := Rank(cells, "bugfix", "medium", quota)
	if len(got) != 2 || got[0].Model != "cool" || got[1].Model != "hot" {
		t.Fatalf("rank = %+v", got)
	}
	// a subscription with room costs ~nothing at the margin
	sub := []Cell{cell("api", "medium", 90, 0.3, 1), cell("cli", "medium", 90, 0.9, 1)}
	got = Rank(sub, "bugfix", "medium", map[string]Quota{"p-cli": {Subscription: true, MaxUtil: 0.2}})
	if got[0].Model != "cli" {
		t.Fatalf("subscription first: %+v", got)
	}
}

func TestFallbackOrder(t *testing.T) {
	prov := func(id string, k storage.ProviderKind, home, status string) storage.Provider {
		return storage.Provider{ID: id, Kind: k, CLIHome: home, Status: status, Enabled: true}
	}
	primary := prov("claude", storage.ProviderClaudeCLI, "", "ok")
	cands := []storage.Provider{primary,
		prov("claude2", storage.ProviderClaudeCLI, "/acc2", "ok"),
		prov("codex", storage.ProviderCodexCLI, "", "ok"),
		prov("gemini", storage.ProviderGeminiCLI, "", "error"),
		prov("openai", storage.ProviderOpenAI, "", "ok"),
	}
	quota := map[string]Quota{"openai": {Over: true}, "codex": {MaxUtil: 0.1}}
	got, _ := FallbackOrder(primary, cands, quota, nil)
	if strings.Join(got, ",") != "codex,claude2" {
		t.Fatalf("order = %v", got)
	}
}

func TestQuotaFromLimits(t *testing.T) {
	now := time.Now()
	q := QuotaOf(storage.Provider{Kind: storage.ProviderClaudeCLI}, map[string]float64{"five_hour": 0.3, "seven_day": 0.82}, false, now.Add(time.Hour), now)
	if !q.Subscription || q.MaxUtil != 0.82 || q.Over {
		t.Fatalf("quota = %+v", q)
	}
}

func TestSuggestByDifficultyAndQuota(t *testing.T) {
	claude := storage.Provider{ID: "cc", Kind: storage.ProviderClaudeCLI, Status: "ok", Enabled: true,
		TierModels: map[string]string{"strong": "claude-opus-5-5", "balanced": "claude-sonnet-5-5", "fast": "claude-haiku-5-5"}}
	opts := []Option{{Agent: storage.Agent{ID: "a1", Name: "Lead"}, Provider: claude, Models: []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}}}
	tierOf := Tiers([]storage.Provider{claude})
	light, ok := Suggest(nil, opts, KindAsk, Light, nil, tierOf)
	if !ok || light.Model != "claude-haiku-5-5" || light.Effort != "medium" || light.AgentName != "Lead" {
		t.Fatalf("light = %+v", light)
	}
	heavy, _ := Suggest(nil, opts, KindFeature, Heavy, nil, tierOf)
	if heavy.Model != "claude-opus-5-5" || heavy.Effort == "max" {
		t.Fatalf("heavy = %+v", heavy)
	}
	if _, ok := Suggest(nil, opts, KindAsk, Light, map[string]Quota{"cc": {Over: true}}, tierOf); ok {
		t.Fatal("suggested a connection over its quota")
	}
	// real jobs outweigh the prior: Haiku kept failing light asks here
	q := 0
	var scores []storage.JobScore
	for range 20 {
		scores = append(scores, storage.JobScore{AgentID: "a1", ProviderID: "cc", Model: "claude-haiku-5-5", Effort: "medium", TaskKind: KindAsk, Difficulty: Light, Q: &q, CostUSD: 0.02, DurationMS: 10})
	}
	if s, _ := Suggest(scores, opts, KindAsk, Light, nil, tierOf); s.Model == "claude-haiku-5-5" && s.Effort == "medium" {
		t.Fatalf("real data ignored: %+v", s)
	}
	// an agent no longer in the project is not suggested
	if s, _ := Suggest(scores, nil, KindAsk, Light, nil, tierOf); s.Model != "" {
		t.Fatalf("gone agent suggested: %+v", s)
	}
}

// A configuration the agent cannot run on any more is never suggested
// (review finding 3): a job that ran on a fallback connection.
func TestSuggestOnlyRunnableConfigs(t *testing.T) {
	claude := storage.Provider{ID: "cc", Kind: storage.ProviderClaudeCLI, Status: "ok", Enabled: true, TierModels: map[string]string{"strong": "claude-opus-5-5"}}
	opts := []Option{{Agent: storage.Agent{ID: "a1", Name: "Lead"}, Provider: claude, Models: []string{"claude-opus-5-5"}}}
	q := 100
	var scores []storage.JobScore
	for range 20 {
		scores = append(scores, storage.JobScore{AgentID: "a1", ProviderID: "codex", Model: "gpt-5.6", Effort: "low", TaskKind: KindAsk, Difficulty: Light, Q: &q, CostUSD: 0.0001, DurationMS: 1})
	}
	s, ok := Suggest(scores, opts, KindAsk, Light, nil, Tiers([]storage.Provider{claude}))
	if !ok || s.ProviderID != "cc" || s.Model != "claude-opus-5-5" {
		t.Fatalf("suggested a fallback's config: %+v", s)
	}
}
