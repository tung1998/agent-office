package scoring

import "strings"

// PriorWeight: a prior counts as this many jobs until real ones outweigh it.
const PriorWeight = 5.0

// Prior is what public numbers say of a model at an effort, on office's own
// scale (Q 0..100, $ per job, ms per job). Estimate: not from a source but
// from the tier and the effort curve.
type Prior struct {
	Model, Effort string
	Q             float64
	CostUSD       float64
	DurationMS    int64
	Source        string
	Estimate      bool
}

const (
	srcOpusEffort = "https://aiagentstore.ai/ai-models/reasoning-effort/claude-opus-5-5 (2026-11)"
	srcPricing    = "https://www.sentra.app/articles/claude-api-pricing (2026-10), https://kingy.ai/blog/claude-sonnet-5-5-specs-benchmarks-pricing/"
	srcGemini     = "https://www.vellum.ai/blog/gemini-3-8-flash-benchmarks-explained (SWE-Bench Pro 61.6, DeepSWE 73.7)"
	srcGPT        = "https://www.eesel.ai/blog/gemini-3-8-flash (GPT-5.6 Sol DeepSWE 72.7; Terra price unverified)"
)

// effortCurve is Opus 5.5's $/job by effort (medium = 1) and what more
// thinking adds to Q on office's scale (public scores rise ~65 → 70.5 from
// high to max, with overthinking reported at max).
var effortCurve = map[string]struct {
	cost float64
	dq   float64
	ms   int64
}{
	"low":    {0.61 / 0.97, -7, 60_000},
	"medium": {1, 0, 120_000},
	"high":   {1.25 / 0.97, 2, 180_000},
	"xhigh":  {2.16 / 0.97, 4, 300_000},
	"max":    {3.57 / 0.97, 4, 600_000},
}

// base is a model family at medium effort: Q, $ per job (Opus 5.5 at medium
// = $0.97, the others by their token price against Opus' $4/$20).
var base = []struct {
	prefix string
	tier   string
	q      float64
	cost   float64
	src    string
}{
	{"claude-opus-5-5", "strong", 85, 0.97, srcOpusEffort},
	{"claude-sonnet-5-5", "balanced", 80, 0.97 * 0.5, srcPricing},
	{"claude-haiku-5-5", "fast", 70, 0.97 * 0.025, srcPricing},
	{"gemini-3.8-flash", "balanced", 80, 0.97 * 0.1875, srcGemini},
	{"gpt-5.6", "balanced", 82, 0.97 * 0.6, srcGPT},
}

var tierBase = map[string]struct{ q, cost float64 }{
	"strong":   {85, 0.97},
	"balanced": {80, 0.5},
	"fast":     {70, 0.05},
}

func curve(effort string) (cost, dq float64, ms int64) {
	c, ok := effortCurve[effort]
	if !ok {
		c = effortCurve["medium"]
	}
	return c.cost, c.dq, c.ms
}

// Priors are the public numbers office starts from, one per model family
// and effort.
func Priors() []Prior {
	var out []Prior
	for _, b := range base {
		for _, e := range []string{"low", "medium", "high", "xhigh", "max"} {
			c, dq, ms := curve(e)
			out = append(out, Prior{Model: b.prefix, Effort: e, Q: b.q + dq, CostUSD: round2(b.cost * c), DurationMS: ms, Source: b.src})
		}
	}
	return out
}

// PriorFor is the prior of a model at an effort ("" = medium): from the
// public numbers when its family has some, else estimated from its tier.
func PriorFor(model, effort, tier string) Prior {
	m := strings.ToLower(model)
	c, dq, ms := curve(effort)
	for _, b := range base {
		if strings.HasPrefix(m, b.prefix) {
			return Prior{Model: model, Effort: effort, Q: b.q + dq, CostUSD: round2(b.cost * c), DurationMS: ms, Source: b.src}
		}
	}
	t, ok := tierBase[tier]
	if !ok {
		t = tierBase["balanced"]
	}
	return Prior{Model: model, Effort: effort, Q: t.q + dq, CostUSD: round2(t.cost * c), DurationMS: ms, Estimate: true}
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// PriorIn is the prior for one group of work: light work suits the smaller
// models better than public (hard) benchmarks show, heavy work less so.
func PriorIn(model, effort, tier, difficulty string) Prior {
	p := PriorFor(model, effort, tier)
	t := tier
	for _, b := range base {
		if strings.HasPrefix(strings.ToLower(model), b.prefix) {
			t = b.tier
		}
	}
	adj := map[string]map[string]float64{
		Light: {"strong": 0, "balanced": 5, "fast": 12},
		Heavy: {"strong": 0, "balanced": -5, "fast": -20},
	}[difficulty][t]
	if difficulty == Heavy {
		switch effort {
		case "low":
			adj -= 5
		case "xhigh":
			adj += 2
		}
	}
	p.Q = min(100, p.Q+adj)
	return p
}
