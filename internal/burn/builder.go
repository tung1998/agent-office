package burn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// agentsFile is the project's map for agents: a builder Burn's start piece
// writes it, with the commands office reads from it (below).
const agentsFile = "AGENTS.md"

// Headings of AGENTS.md office reads commands from: the checks a piece must
// pass when the Burn has none of its own, and how to run the app (the
// builder Burn's preview).
var (
	checksHeads = []string{"checks", "kiểm chứng"}
	runHeads    = []string{"run", "chạy"}
)

// maxAgentsCommands caps the commands read from one section.
const maxAgentsCommands = 20

// agentsCommands reads the commands under AGENTS.md's heading named one of
// heads (any level, case-insensitive): the lines of the section's first
// fenced block, else its list items written as `code`; # comments skipped.
// None: no file, no such section, or nothing that reads as a command.
func agentsCommands(dir string, heads ...string) []string {
	if dir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, agentsFile))
	if err != nil {
		return nil
	}
	var fenced, listed []string
	in, level, fence, fencesSeen := false, 0, false, 0
	for line := range strings.SplitSeq(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if !fence {
			if n := headingLevel(t); n > 0 {
				if in && n <= level {
					break // the next section
				}
				if !in && slices.Contains(heads, strings.ToLower(strings.TrimSpace(strings.TrimLeft(t, "#")))) {
					in, level = true, n
				}
				continue
			}
		}
		if !in {
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
			if !fence {
				fencesSeen++
			}
			continue
		}
		switch {
		case fence && fencesSeen == 0:
			if t != "" && !strings.HasPrefix(t, "#") {
				fenced = append(fenced, t)
			}
		case !fence:
			if item, ok := strings.CutPrefix(t, "- "); ok {
				if c, ok := codeSpan(item); ok {
					listed = append(listed, c)
				}
			}
		}
	}
	out := fenced
	if len(out) == 0 {
		out = listed
	}
	if len(out) > maxAgentsCommands {
		out = out[:maxAgentsCommands]
	}
	return out
}

// headingLevel is how many # start a Markdown heading (0: not one).
func headingLevel(t string) int {
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n == len(t) || t[n] != ' ' {
		return 0
	}
	return n
}

// codeSpan is the command of a list item written `like this` (it may say
// more after it: "`npm test`: unit tests").
func codeSpan(item string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(item), "`")
	if !ok {
		return "", false
	}
	c, _, ok := strings.Cut(rest, "`")
	c = strings.TrimSpace(c)
	return c, ok && c != ""
}

// Preview starts a project's app again on command, in its Burn run (the
// ops manager's preview process).
type Preview func(ctx context.Context, projectID, command string) error

// SetPreview sets how a builder run's app is started again.
func (s *Service) SetPreview(p Preview) { s.preview = p }

// previewRun: a builder Burn's piece merged, its app starts again on the
// run's latest code, with the command under AGENTS.md's "## Run" (ADR-266).
// No command yet (the start piece has not written it) or no preview set:
// nothing. Failing to start is said in the Burn's chat, never a piece's fault.
func (s *Service) previewRun(ctx context.Context, b storage.BurnSession) {
	if s.preview == nil || templateOf(b) != "builder" {
		return
	}
	cmds := agentsCommands(s.RunTree(b), runHeads...)
	if len(cmds) == 0 {
		return
	}
	if err := s.preview(context.WithoutCancel(ctx), b.ProjectID, cmds[0]); err != nil {
		s.say(ctx, b, "Chưa chạy lại được preview (`"+cmds[0]+"`): "+oneLine(err.Error(), 200))
	}
}

// RunDir is where a project's current Burn run is ("" when it has none):
// the folder a process at ops.RunCwd runs in.
func (s *Service) RunDir(ctx context.Context, projectID string) string {
	b, err := s.store.Burn().Session(ctx, projectID)
	if err != nil {
		return ""
	}
	return s.RunTree(b)
}
