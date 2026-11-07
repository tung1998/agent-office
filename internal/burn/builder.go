package burn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// The builder template (ADR-139) builds a project from an idea, the way
// long-running agent harnesses do: the first scan finds no features.json and
// records one piece, the start (spec, feature list, map, progress log, a
// scaffold whose build/test pass); later scans record the next features not
// passing yet, in order; a worker does one feature, runs its acceptance and
// marks it passing. The repo's files are the memory between turns, and the
// Burn's checks run every passing feature's acceptance again, so a feature
// broken later sends its piece back.

// featuresFile is the feature list at the project's root.
const featuresFile = "features.json"

// maxAcceptance caps the acceptance commands the checks run.
const maxAcceptance = 50

// feature is one entry of features.json.
type feature struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Acceptance string `json:"acceptance"` // a shell command: exit 0 = it works
	Passes     bool   `json:"passes"`
}

// readFeatures reads dir's feature list: a list, or {"features": [...]};
// none of the file yet: none, no error. The file is there but neither shape
// parses: an error, since that is a sign something broke (ADR-139's
// re-check of every passing feature must not silently skip).
func readFeatures(dir string) ([]feature, error) {
	raw, err := os.ReadFile(filepath.Join(dir, featuresFile))
	if err != nil {
		return nil, nil
	}
	var list []feature
	if json.Unmarshal(raw, &list) == nil {
		return list, nil
	}
	var wrapped struct {
		Features []feature `json:"features"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return wrapped.Features, nil
	} else {
		return nil, fmt.Errorf("%s: %w", featuresFile, err)
	}
}

// shellQuote wraps s in single quotes for sh -c, escaping any inside it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Feature is one entry of a builder Burn's features.json, for callers
// outside this package (the project page, to show its progress).
type Feature struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Passes bool   `json:"passes"`
}

// Features reads dir's feature list for display (ADR-139): none when dir
// is "" (no run worktree and no project folder) or features.json is not
// there yet (the start piece has not run).
func Features(dir string) []Feature {
	if dir == "" {
		return nil
	}
	var out []Feature
	list, _ := readFeatures(dir) // display only: a parse problem shows as no features, the checks (acceptance) are what must not stay silent
	for _, f := range list {
		out = append(out, Feature{f.ID, f.Title, f.Passes})
	}
	return out
}

// acceptance are the acceptance commands of the features passing in dir,
// each once, in the list's order; err set when features.json is there but
// did not parse (its re-check is the builder Burn's whole safety point, so
// that must not be mistaken for "no features").
func acceptance(dir string) ([]string, error) {
	list, err := readFeatures(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range list {
		c := strings.TrimSpace(f.Acceptance)
		if !f.Passes || c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
		if len(out) == maxAcceptance {
			break
		}
	}
	return out, nil
}

// builderCheck is how a builder piece is done and checked (burn/work.md).
const builderCheck = "this Burn builds the project feature by feature (features.json, PROGRESS.md at the root). " +
	"Do ONLY the one feature this piece names (or the start, if it says so); read PROGRESS.md and features.json first. " +
	"When it works, run its acceptance command yourself, then set its \"passes\" to true (never for another feature, never without running it), " +
	"add what you did and what is next to PROGRESS.md, and put what you learned (a pitfall, a command) in your burn_done summary. " +
	"Office runs the acceptance of every passing feature again: one you broke sends the piece back"

// parallel is how many pieces b does at a time: a builder one, as each
// feature runs on those before it and all write features.json, PROGRESS.md.
func parallel(b storage.BurnSession) int {
	if templateOf(b) == "builder" {
		return 1
	}
	return max(b.MaxParallel, 1)
}

// builderBusy: a builder Burn with a piece not finished, so no scan: the
// next features are read from features.json once that piece is merged.
func builderBusy(b storage.BurnSession, items []storage.BurnItem) bool {
	if templateOf(b) != "builder" {
		return false
	}
	for _, it := range items {
		if !hunting(it) && openStatus[it.Status] {
			return true
		}
	}
	return false
}
