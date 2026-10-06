package usage

import (
	"regexp"
	"strings"
)

// Price is USD per million tokens.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`  // 0: 10% of input
	CacheWrite float64 `json:"cache_write,omitempty"` // 0: 125% of input
}

// defaultPrices are Anthropic first-party list prices (claude-api reference,
// cached 2026-06-24). Admins can override or add models (e.g. GPT) in settings.
var defaultPrices = map[string]Price{
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 0.25},
	"claude-mythos-5-1": {Input: 10, Output: 50},
	"claude-fable-5":    {Input: 10, Output: 50},
	"claude-opus-5-5":   {Input: 4, Output: 20, CacheRead: 0.20},
	"claude-opus-5":     {Input: 5, Output: 25},
	"claude-opus-4-8":   {Input: 5, Output: 25},
	"claude-opus-4-7":   {Input: 5, Output: 25},
	"claude-opus-4-6":   {Input: 5, Output: 25},
	"claude-sonnet-5":   {Input: 2, Output: 10},
	"claude-sonnet-4-6": {Input: 3, Output: 15},
	"claude-haiku-4-5":  {Input: 1, Output: 5},
}

// DefaultPrices returns a copy of the built-in table (for the settings page).
func DefaultPrices() map[string]Price {
	out := make(map[string]Price, len(defaultPrices))
	for k, v := range defaultPrices {
		out[k] = v
	}
	return out
}

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// lookup finds the price of model: overrides first, then built-ins; date
// suffixes (claude-x-20260101) and provider prefixes (anthropic.claude-x) are ignored.
func lookup(model string, overrides map[string]Price) (Price, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	m = strings.TrimPrefix(m, "anthropic.")
	m = dateSuffix.ReplaceAllString(m, "")
	for _, table := range []map[string]Price{overrides, defaultPrices} {
		if p, ok := table[m]; ok {
			return p, true
		}
	}
	return Price{}, false
}

// Estimate returns the cost of a call, or false when the model has no price.
// inputTokens is the whole prompt; cached tokens are not split out here, so
// this is an upper bound for cached prompts.
func Estimate(model string, inputTokens, outputTokens int, overrides map[string]Price) (float64, bool) {
	p, ok := lookup(model, overrides)
	if !ok {
		return 0, false
	}
	return (float64(inputTokens)*p.Input + float64(outputTokens)*p.Output) / 1e6, true
}
