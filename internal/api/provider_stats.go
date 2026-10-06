package api

import (
	"net/http"
	"sort"
	"strconv"
	"time"
)

type providerDay struct {
	Day     string  `json:"day"` // YYYY-MM-DD, office timezone
	Calls   int     `json:"calls"`
	CostUSD float64 `json:"cost_usd"`
}

type providerStat struct {
	ProviderID   string        `json:"provider_id"`
	Calls        int           `json:"calls"`
	Errors       int           `json:"errors"`
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	CostUSD      float64       `json:"cost_usd"`
	AvgMS        int64         `json:"avg_ms"`
	LastUsedAt   *time.Time    `json:"last_used_at"`
	TopModel     string        `json:"top_model"`
	Days         []providerDay `json:"days"` // oldest first, every day of the window
}

// providerStats summarises model calls per connection over ?days (default 7):
// calls, errors, tokens, cost, latency, last use and a per-day series.
func (s *server) providerStats(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 7
	}
	loc := time.Local
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	runs, err := s.cfg.Store.Runs().Since(r.Context(), start)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	dayKeys := make([]string, days)
	for i := range dayKeys {
		dayKeys[i] = start.AddDate(0, 0, i).Format("2006-01-02")
	}
	type acc struct {
		providerStat
		ms     int64
		msN    int64
		models map[string]int
		byDay  map[string]*providerDay
	}
	stats := map[string]*acc{}
	today := struct {
		Calls   int     `json:"calls"`
		Errors  int     `json:"errors"`
		Tokens  int     `json:"tokens"`
		CostUSD float64 `json:"cost_usd"`
	}{}
	todayKey := now.Format("2006-01-02")
	for _, x := range runs {
		if x.ProviderID == "" || x.Kind == "provider_test" {
			continue
		}
		a := stats[x.ProviderID]
		if a == nil {
			a = &acc{providerStat: providerStat{ProviderID: x.ProviderID}, models: map[string]int{}, byDay: map[string]*providerDay{}}
			stats[x.ProviderID] = a
		}
		a.Calls++
		a.InputTokens += x.InputTokens
		a.OutputTokens += x.OutputTokens
		if x.Status != "ok" {
			a.Errors++
		}
		if x.CostUSD != nil {
			a.CostUSD += *x.CostUSD
		}
		if x.DurationMS > 0 {
			a.ms += x.DurationMS
			a.msN++
		}
		if x.Model != "" {
			a.models[x.Model]++
		}
		if a.LastUsedAt == nil || x.CreatedAt.After(*a.LastUsedAt) {
			t := x.CreatedAt
			a.LastUsedAt = &t
		}
		day := x.CreatedAt.In(loc).Format("2006-01-02")
		d := a.byDay[day]
		if d == nil {
			d = &providerDay{Day: day}
			a.byDay[day] = d
		}
		d.Calls++
		if x.CostUSD != nil {
			d.CostUSD += *x.CostUSD
		}
		if day == todayKey {
			today.Calls++
			today.Tokens += x.InputTokens + x.OutputTokens
			if x.Status != "ok" {
				today.Errors++
			}
			if x.CostUSD != nil {
				today.CostUSD += *x.CostUSD
			}
		}
	}
	out := []providerStat{}
	for _, a := range stats {
		if a.msN > 0 {
			a.AvgMS = a.ms / a.msN
		}
		best := 0
		for m, n := range a.models {
			if n > best || (n == best && m < a.TopModel) {
				a.TopModel, best = m, n
			}
		}
		for _, k := range dayKeys {
			if d := a.byDay[k]; d != nil {
				a.Days = append(a.Days, *d)
			} else {
				a.Days = append(a.Days, providerDay{Day: k})
			}
		}
		out = append(out, a.providerStat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Calls > out[j].Calls })
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "providers": out, "today": today})
}
