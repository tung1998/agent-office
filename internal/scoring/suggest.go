package scoring

import (
	"slices"

	"github.com/tung1998/agent-office/internal/storage"
)

// Option is a configuration an agent of the project can run on: its own
// connection with one of its models.
type Option struct {
	Agent    storage.Agent
	Provider storage.Provider
	Models   []string // the agent's own model first, then its connection's tier models
}

// Tiers maps a connection's models to their tier ("" when unknown).
func Tiers(ps []storage.Provider) func(model string) string {
	m := map[string]string{}
	for _, p := range ps {
		for tier, model := range p.TierModels {
			m[model] = tier
		}
	}
	return func(model string) string { return m[model] }
}

// Table is the statistics of a project: every configuration it ran on, in
// every group of work, and the optimal one of each group.
type Table struct {
	Cells []Cell `json:"cells"`
	Best  []Cell `json:"best"`
}

// Stats builds the table from the scored jobs.
func Stats(scores []storage.JobScore, tierOf func(string) string) Table {
	t := Table{Cells: Aggregate(scores, tierOf), Best: []Cell{}}
	for _, k := range Kinds {
		for _, d := range Difficulties {
			if b, ok := Best(t.Cells, k, d); ok {
				t.Best = append(t.Best, b)
			}
		}
	}
	return t
}

// Suggestion is the configuration office suggests for a group of work, and
// why, or none.
type Suggestion struct {
	Cell
	AgentName string `json:"agent_name"`
}

// Suggest ranks every option of the project for a group of work (their real
// numbers, else their priors) and suggests the first one; none when every
// option is over its quota. Effort max is never suggested.
func Suggest(scores []storage.JobScore, opts []Option, kind, difficulty string, quota map[string]Quota, tierOf func(string) string) (Suggestion, bool) {
	cells := group(Aggregate(scores, tierOf), kind, difficulty)
	names := map[string]string{}
	have := map[Config]bool{}
	for _, c := range cells {
		have[c.Config] = true
	}
	for _, o := range opts {
		names[o.Agent.ID] = o.Agent.Name
		for _, m := range o.Models {
			for _, e := range storage.EffortsFor(o.Provider.Kind, m) {
				if e == "max" {
					continue
				}
				c := Config{AgentID: o.Agent.ID, ProviderID: o.Provider.ID, Model: m, Effort: e}
				if !have[c] {
					have[c] = true
					cells = append(cells, PriorCell(c, kind, difficulty, tierOf(m)))
				}
			}
		}
	}
	// only what the project's agents can still run on: their own connection
	// and its models (not a fallback's, nor one the agent left)
	runnable := map[[3]string]bool{}
	for _, o := range opts {
		for _, m := range o.Models {
			runnable[[3]string{o.Agent.ID, o.Provider.ID, m}] = true
		}
	}
	cells = slices.DeleteFunc(cells, func(c Cell) bool { return !runnable[[3]string{c.AgentID, c.ProviderID, c.Model}] })
	r := Rank(cells, kind, difficulty, quota)
	if len(r) == 0 {
		return Suggestion{}, false
	}
	return Suggestion{Cell: r[0], AgentName: names[r[0].AgentID]}, true
}
