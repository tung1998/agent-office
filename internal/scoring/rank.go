package scoring

import (
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// HotUtil: a connection that used this much of any window is ranked lower.
const HotUtil = 0.80

// Config is what a job ran on.
type Config struct {
	AgentID    string `json:"agent_id"`
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	Effort     string `json:"effort"`
}

// Cell is a configuration's numbers in one group of work (kind ×
// difficulty): real jobs blended with the prior as PriorWeight jobs.
type Cell struct {
	Config
	Kind       string  `json:"kind"`
	Difficulty string  `json:"difficulty"`
	N          int     `json:"n"` // real jobs with a Q
	Q          float64 `json:"q"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMS int64   `json:"duration_ms"`
	FromPrior  bool    `json:"from_prior"` // mostly public numbers (N < PriorWeight)
	Estimate   bool    `json:"estimate"`   // the prior itself is an estimate
}

type cellKey struct {
	Config
	kind, diff string
}

// Aggregate groups scored jobs by work group and configuration and blends
// each with its prior. Jobs without a Q (failed, nothing applies) are left out.
func Aggregate(scores []storage.JobScore, tierOf func(model string) string) []Cell {
	type acc struct {
		n       int
		q, cost float64
		ms      int64
	}
	sums := map[cellKey]*acc{}
	var order []cellKey
	for _, s := range scores {
		if s.Q == nil {
			continue
		}
		k := cellKey{Config{s.AgentID, s.ProviderID, s.Model, s.Effort}, s.TaskKind, s.Difficulty}
		a := sums[k]
		if a == nil {
			a = &acc{}
			sums[k] = a
			order = append(order, k)
		}
		a.n++
		a.q += float64(*s.Q)
		a.cost += s.CostUSD
		a.ms += s.DurationMS
	}
	out := make([]Cell, 0, len(order))
	for _, k := range order {
		a := sums[k]
		tier := ""
		if tierOf != nil {
			tier = tierOf(k.Model)
		}
		out = append(out, blend(k.Config, k.kind, k.diff, a.n, a.q, a.cost, a.ms, PriorIn(k.Model, k.Effort, tier, k.diff)))
	}
	return out
}

// PriorCell is a configuration with no real jobs yet in a group.
func PriorCell(c Config, kind, difficulty, tier string) Cell {
	return blend(c, kind, difficulty, 0, 0, 0, 0, PriorIn(c.Model, c.Effort, tier, difficulty))
}

func blend(c Config, kind, diff string, n int, sumQ, sumCost float64, sumMS int64, p Prior) Cell {
	w := float64(n) + PriorWeight
	return Cell{Config: c, Kind: kind, Difficulty: diff, N: n,
		Q:          (sumQ + p.Q*PriorWeight) / w,
		CostUSD:    (sumCost + p.CostUSD*PriorWeight) / w,
		DurationMS: int64((float64(sumMS) + float64(p.DurationMS)*PriorWeight) / w),
		FromPrior:  float64(n) < PriorWeight, Estimate: p.Estimate}
}

func group(cells []Cell, kind, difficulty string) []Cell {
	var g []Cell
	for _, c := range cells {
		if c.Kind == kind && c.Difficulty == difficulty {
			g = append(g, c)
		}
	}
	return g
}

// Best is a group's optimal configuration: the cheapest with Q ≥ Threshold
// (within 10% of it, the fastest); with none good enough, the highest Q.
func Best(cells []Cell, kind, difficulty string) (Cell, bool) {
	g := group(cells, kind, difficulty)
	if len(g) == 0 {
		return Cell{}, false
	}
	var good []Cell
	for _, c := range g {
		if c.Q >= Threshold {
			good = append(good, c)
		}
	}
	if len(good) == 0 {
		return slices.MaxFunc(g, func(a, b Cell) int { return cmpF(a.Q, b.Q) }), true
	}
	cheapest := slices.MinFunc(good, func(a, b Cell) int { return cmpF(a.CostUSD, b.CostUSD) })
	var near []Cell
	for _, c := range good {
		if c.CostUSD <= cheapest.CostUSD*1.1 {
			near = append(near, c)
		}
	}
	return slices.MinFunc(near, func(a, b Cell) int { return cmpF(float64(a.DurationMS), float64(b.DurationMS)) }), true
}

func cmpF(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Quota is a connection's room right now.
type Quota struct {
	Over         bool          `json:"over"`     // over its limit or the person's cap, or broken
	MaxUtil      float64       `json:"max_util"` // the fullest window, 0..1
	ResetsIn     time.Duration `json:"resets_in"`
	Subscription bool          `json:"subscription"` // paid flat (a CLI login): its margin costs ~nothing
}

// QuotaOf reads a connection's room from its windows' utilisation.
func QuotaOf(p storage.Provider, util map[string]float64, over bool, resetsAt, now time.Time) Quota {
	q := Quota{Over: over || p.Status == "error", Subscription: strings.HasSuffix(string(p.Kind), "_cli")}
	for _, u := range util {
		q.MaxUtil = max(q.MaxUtil, u)
	}
	if resetsAt.After(now) {
		q.ResetsIn = resetsAt.Sub(now)
	}
	return q
}

// Rank orders a group's configurations for a suggestion: never effort max,
// none over its quota; hot connections (≥ HotUtil) after the others, those
// under Threshold after those over it, then by marginal cost (a subscription
// with room ~ a tenth of its price) and time.
func Rank(cells []Cell, kind, difficulty string, quota map[string]Quota) []Cell {
	type ranked struct {
		Cell
		hot, weak bool
		cost      float64
	}
	var rs []ranked
	for _, c := range group(cells, kind, difficulty) {
		q := quota[c.ProviderID]
		if c.Effort == "max" || q.Over {
			continue
		}
		r := ranked{Cell: c, hot: q.MaxUtil >= HotUtil, weak: c.Q < Threshold, cost: c.CostUSD}
		if q.Subscription && !r.hot {
			r.cost *= 0.1
		}
		rs = append(rs, r)
	}
	slices.SortStableFunc(rs, func(a, b ranked) int {
		if a.hot != b.hot {
			return b2c(a.hot)
		}
		if a.weak != b.weak {
			return b2c(a.weak)
		}
		if c := cmpF(a.cost, b.cost); c != 0 {
			return c
		}
		return cmpF(float64(a.DurationMS), float64(b.DurationMS))
	})
	out := make([]Cell, len(rs))
	for i, r := range rs {
		out[i] = r.Cell
	}
	return out
}

func b2c(first bool) int {
	if first {
		return 1
	}
	return -1
}

// Vendor is who runs a connection's models, for telling fallbacks apart.
func Vendor(p storage.Provider) string {
	switch p.Kind {
	case storage.ProviderAnthropic, storage.ProviderClaudeCLI:
		return "anthropic"
	case storage.ProviderOpenAI, storage.ProviderCodexCLI:
		return "openai"
	case storage.ProviderGeminiCLI, storage.ProviderAntigravityCLI:
		return "google"
	case storage.ProviderDSH:
		return "deepseek"
	}
	if p.Preset != "" {
		return p.Preset
	}
	if p.BaseURL != "" {
		return p.BaseURL
	}
	return string(p.Kind) + ":" + p.ID
}

// FallbackOrder suggests an agent's fallback connections: another vendor
// first (it also covers the vendor being down), then the same vendor on
// another account (covers running out only); broken, disabled, over-quota
// and same-account ones are left out. Within each, the emptiest first.
func FallbackOrder(primary storage.Provider, cands []storage.Provider, quota map[string]Quota, vendorOf func(storage.Provider) string) (order, reasons []string) {
	if vendorOf == nil {
		vendorOf = Vendor
	}
	pv := vendorOf(primary)
	type cand struct {
		p    storage.Provider
		same bool
	}
	var cs []cand
	for _, p := range cands {
		q := quota[p.ID]
		if p.ID == primary.ID || !p.Enabled || p.Status == "error" || q.Over {
			continue
		}
		same := vendorOf(p) == pv
		if same && p.Kind == primary.Kind && p.CLIHome == primary.CLIHome {
			continue // the same account: runs out together
		}
		cs = append(cs, cand{p, same})
	}
	slices.SortStableFunc(cs, func(a, b cand) int {
		if a.same != b.same {
			return b2c(a.same)
		}
		return cmpF(quota[a.p.ID].MaxUtil, quota[b.p.ID].MaxUtil)
	})
	for _, c := range cs {
		order = append(order, c.p.ID)
		if c.same {
			reasons = append(reasons, "same_vendor_other_account")
		} else {
			reasons = append(reasons, "other_vendor")
		}
	}
	return order, reasons
}
