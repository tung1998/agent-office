// Package features: packs of features a project turns off to hide what it
// does not use (git, Vận hành) from its
// dashboard: menus, tabs, widgets, incident badges. Only the UI: agents keep
// their tools and permissions, data stays and nothing running is stopped. A
// project with no setting has every feature on, but those of a pack off by
// default (support, ADR-171): a project turns those on, and they gate their
// own data, tools and bot messages too.
package features

import (
	"context"
	"slices"

	"github.com/tung1998/agent-office/internal/storage"
)

// The features of the develop pack.
const (
	Git = "git" // the git bar and branch menu
	Ops = "ops" // Vận hành: processes, containers (and their monitors), env
	// SelfCheck: office's self-check calls the coordinator when work looks
	// stuck (ADR-230); off, the project is not checked
	SelfCheck = "selfcheck"
)

// Support is the support pack's one feature: tickets, sources, Inbox (ADR-171).
const Support = "support"

// Pack groups features turned on and off together. A feature belongs to one
// pack and depends on no other feature (Effective walks packs, then their
// features): either would need Effective to change.
type Pack struct {
	ID       string   `json:"id"`
	Features []string `json:"features"`
	// DefaultOff: off for a project that has not turned it on (a project
	// with no setting stays as it was before the pack existed).
	DefaultOff bool `json:"default_off,omitempty"`
}

// Packs lists every pack. Everything else (chat, agents, automations, Burn,
// memory, reviewing changes) is core: always there.
var Packs = []Pack{
	{ID: "develop", Features: []string{Git, Ops, SelfCheck}},
	{ID: "support", Features: []string{Support}, DefaultOff: true},
}

// needFolder: features with nothing to work on without a project folder.
var needFolder = []string{Git}

// Why a feature is off.
const (
	ReasonProject  = "project"   // the project turned it off
	ReasonNoFolder = "no_folder" // the project has no folder
)

// Config is a project's setting: the packs it turned off and single
// features off within the packs left on.
type Config struct {
	Packs map[string]bool `json:"packs"` // a pack missing = on (off: a DefaultOff pack)
	Off   []string        `json:"off"`
}

// All lists every feature.
func All() []string {
	var out []string
	for _, p := range Packs {
		out = append(out, p.Features...)
	}
	return out
}

// Effective works out the features on, and why each other one is off.
func Effective(c Config, hasPath bool) (on []string, off map[string]string) {
	on, off = []string{}, map[string]string{}
	for _, p := range Packs {
		packOn, ok := c.Packs[p.ID]
		if !ok {
			packOn = !p.DefaultOff
		}
		for _, f := range p.Features {
			switch {
			case !hasPath && slices.Contains(needFolder, f):
				off[f] = ReasonNoFolder
			case !packOn, slices.Contains(c.Off, f):
				off[f] = ReasonProject
			default:
				on = append(on, f)
			}
		}
	}
	return on, off
}

// Key is where a project's setting is stored.
func Key(projectID string) string { return "features:" + projectID }

// LoadConfig reads a project's setting (empty, everything on, when none is
// saved or it does not read).
func LoadConfig(ctx context.Context, st storage.Store, projectID string) Config {
	var c Config
	if ok, err := st.Settings().Get(ctx, Key(projectID), &c); !ok || err != nil {
		return Config{}
	}
	return c
}

// Save stores a project's setting, keeping only known packs and features.
func Save(ctx context.Context, st storage.Store, projectID string, c Config) error {
	clean := Config{Packs: map[string]bool{}, Off: []string{}}
	for _, p := range Packs {
		if v, ok := c.Packs[p.ID]; ok {
			clean.Packs[p.ID] = v
		}
	}
	for _, f := range All() {
		if slices.Contains(c.Off, f) {
			clean.Off = append(clean.Off, f)
		}
	}
	return st.Settings().Set(ctx, Key(projectID), clean)
}

// For is the features on in a project.
func For(ctx context.Context, st storage.Store, projectID string) []string {
	hasPath := false
	if r, err := st.Repos().Get(ctx, projectID); err == nil {
		hasPath = r.Path != ""
	}
	on, _ := Effective(LoadConfig(ctx, st, projectID), hasPath)
	return on
}

// Has reports a feature on (nil: not worked out, everything on but the
// features of a pack off by default).
func Has(on []string, f string) bool {
	if on == nil {
		return !defaultOff(f)
	}
	return slices.Contains(on, f)
}

// defaultOff: f belongs to a pack off by default.
func defaultOff(f string) bool {
	for _, p := range Packs {
		if p.DefaultOff && slices.Contains(p.Features, f) {
			return true
		}
	}
	return false
}

// On reports whether a project has feature f on (reads its setting).
func On(ctx context.Context, st storage.Store, projectID, f string) bool {
	return slices.Contains(For(ctx, st, projectID), f)
}
