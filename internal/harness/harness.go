// Package harness gathers the evidence for a project's harness review
// (ADR-181), the way Better Harness (github.com/QoderAI/better-harness)
// reviews the loop around coding agents, but from what the office saw: the
// project's agent assets (instructions, skills, tests, CI, hooks) and, over
// a window, how diffs ended, which commands failed or were refused, whether
// a check ran before a diff was merged, where the person corrected an agent,
// what the person asked again and again (ADR-228), Burn's reviews and the
// agents' notes. No model is called: the bundle is
// what the review's agents read.
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/tung1998/agent-office/internal/scan"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/verdict"
	"github.com/tung1998/agent-office/internal/verify"
)

// Evidence is the bundle, compact enough for an agent's prompt.
type Evidence struct {
	ProjectID   string      `json:"project_id"`
	Project     string      `json:"project"`
	Path        string      `json:"path"`
	Days        int         `json:"days"`
	Since       time.Time   `json:"since"`
	Assets      Assets      `json:"assets"`
	Diffs       Diffs       `json:"diffs"`
	Commands    Commands    `json:"commands"`
	Turns       Turns       `json:"turns"`
	Validation  Validation  `json:"validation"`
	Corrections Corrections `json:"corrections"`
	Repeats     []Repeat    `json:"repeats"` // requests the person made again and again
	Burn        Burn        `json:"burn"`
	Learning    Learning    `json:"learning"`
}

// Assets are what steers and checks the agents before and after they act.
type Assets struct {
	Instructions []string `json:"instructions"` // CLAUDE.md, AGENTS.md, rules…
	Skills       []string `json:"skills"`
	Subagents    []string `json:"subagents"`
	Tests        []string `json:"tests"` // test frameworks found
	CI           []string `json:"ci"`
	Hooks        []string `json:"hooks"`
	Processes    []string `json:"processes"` // the project's commands in office (name: command)
	// Agents are the project's office agents: their own instructions steer
	// them as CLAUDE.md does (name (role): instruction size)
	Agents      []string `json:"agents"`
	Description string   `json:"description"` // the project's description in office
	// Automations already run for the project (name: source → action), so a
	// repeated request they cover is not proposed again
	Automations []string `json:"automations"`
}

// Diffs are how the agents' code changes ended.
type Diffs struct {
	Total            int     `json:"total"`
	Merged           int     `json:"merged"`
	Pending          int     `json:"pending"`
	RejectedByPerson int     `json:"rejected_by_person"`
	Superseded       int     `json:"superseded"`      // replaced by a newer diff of the same chat
	FailedToMerge    int     `json:"failed_to_merge"` // conflicts, project changed meanwhile
	Rejected         []Item  `json:"rejected"`        // the person's refusals, newest first
	MergeFailures    []Group `json:"merge_failures"`
}

// Commands are the commands agents proposed (run_command cards) and git actions.
type Commands struct {
	ByStatus map[string]int `json:"by_status"` // kind/status → count
	Refused  []Group        `json:"refused"`   // refused by the person or the rules
	Failed   []Group        `json:"failed"`
}

// Turns are the agents' chat turns and tasks.
type Turns struct {
	ByStatus  map[string]int `json:"by_status"`
	TopErrors []Group        `json:"top_errors"`
}

// Validation: did a check (test, build, typecheck, lint) run in the chat
// before each merged diff.
type Validation struct {
	MergedDiffs   int      `json:"merged_diffs"`
	WithCheck     int      `json:"with_check"`
	WithoutCheck  []string `json:"without_check_chats"` // chat ids, a sample
	CheckCommands []Group  `json:"check_commands"`
	// office's own check before merging a chat's diff (ADR-222): diffs it
	// passed (counted in WithCheck too) and diffs it held back
	GatePassed int `json:"gate_passed"`
	GateHeld   int `json:"gate_held"`
}

// Corrections are the person's messages that look like correcting an agent.
type Corrections struct {
	Messages int    `json:"person_messages"`
	Count    int    `json:"count"`
	Samples  []Item `json:"samples"` // newest first
}

// Repeat is one request the person made in several chats: a candidate for
// an automation (same thing on a schedule or an event) or a skill (same
// procedure on demand).
type Repeat struct {
	Count  int      `json:"count"`
	Chats  int      `json:"chats"`
	Sample string   `json:"sample"` // the newest wording
	Ids    []string `json:"chat_ids"`
	Last   string   `json:"last"` // date
}

// Burn is how Burn's work ended and what it learned.
type Burn struct {
	ByStatus       map[string]int `json:"by_status"`
	ReviewDisagree int            `json:"review_disagree"` // the reviewer refused the issue, plan or result
	Failed         []string       `json:"failed"`
	Lessons        string         `json:"lessons"`
}

// Learning is what carries over to the next task.
type Learning struct {
	Memories       int      `json:"memories"`
	NewMemories    int      `json:"new_memories"`
	Topics         []string `json:"topics"`
	RepeatedErrors []Group  `json:"repeated_errors"` // the same failure 3+ times
	// AgentSkills are the skills each agent keeps at hand (ADR-185) and how
	// they were used: "agent: skill (source, used N×, last date | never)".
	AgentSkills []string `json:"agent_skills"`
}

// Item is one piece of evidence and where it happened.
type Item struct {
	Chat string    `json:"chat,omitempty"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Group is one kind of thing and how often it happened.
type Group struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

const (
	maxSamples = 15
	maxGroups  = 10
)

// Collect builds the evidence for a project over the last days days.
func Collect(ctx context.Context, store storage.Store, p storage.Repo, days int) (Evidence, error) {
	days = min(max(days, 1), 90)
	since := time.Now().UTC().AddDate(0, 0, -days)
	ev := Evidence{ProjectID: p.ID, Project: p.Name, Path: p.Path, Days: days, Since: since}
	if p.Path != "" {
		ev.Assets = assets(p.Path)
	}
	ev.Assets.Description = cut(p.Description, 400)
	var skills []string
	if agents, err := store.Agents().List(ctx, p.ID); err == nil {
		for _, a := range agents {
			if !a.Disabled {
				ev.Assets.Agents = append(ev.Assets.Agents, fmt.Sprintf("%s (%s): %d ký tự chỉ dẫn", a.Name, cut(a.Role, 60), len([]rune(a.Instructions))))
				skills = append(skills, agentSkills(ctx, store, a)...)
			}
		}
	}
	if autos, err := store.Automations().List(ctx, p.ID); err == nil {
		for _, a := range autos {
			if a.Enabled {
				ev.Assets.Automations = append(ev.Assets.Automations, fmt.Sprintf("%s: %s → %s", cut(a.Name, 80), a.Source, a.Action))
			}
		}
	}
	if procs, err := store.Processes().List(ctx, p.ID); err == nil {
		for _, pr := range procs {
			ev.Assets.Processes = append(ev.Assets.Processes, pr.Name+": "+cut(pr.Command, 120))
		}
	}
	f, err := store.Harness().Facts(ctx, p.ID, since)
	if err != nil {
		return ev, err
	}
	ev.Diffs = diffs(f.Patches)
	ev.Commands = commands(f.Actions)
	ev.Turns = turns(f.Jobs)
	ev.Validation = validation(f)
	ev.Corrections = corrections(f.UserMessages)
	ev.Repeats = repeats(f.UserMessages)
	ev.Burn = burn(f)
	ev.Learning = Learning{Memories: f.Memories, NewMemories: f.NewMemories, Topics: f.MemoryTopics, RepeatedErrors: repeated(f), AgentSkills: skills}
	return ev, nil
}

// agentSkills are an agent's skills at hand and their use, for the review
// to see which earn their place.
func agentSkills(ctx context.Context, store storage.Store, a storage.Agent) []string {
	list, err := store.AgentSkills().List(ctx, a.ID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		used := "never used, added " + s.CreatedAt.Format("2006-01-02")
		if s.Uses > 0 {
			used = fmt.Sprintf("used %d×, last %s", s.Uses, s.LastUsedAt.Format("2006-01-02"))
		}
		out = append(out, fmt.Sprintf("%s: %s (%s, %s)", a.Name, s.Name, s.Source, used))
	}
	return out
}

// ---- assets ----

func assets(root string) Assets {
	var a Assets
	if s, err := scan.Scan(root); err == nil {
		for _, d := range s.AgentDocs {
			switch d.Kind {
			case "skill":
				a.Skills = append(a.Skills, d.Path)
			case "subagent":
				a.Subagents = append(a.Subagents, d.Path)
			default:
				a.Instructions = append(a.Instructions, d.Path)
			}
		}
		a.Tests = s.Tests
	}
	for _, g := range []string{".github/workflows/*", ".gitlab-ci.yml", "bitbucket-pipelines.yml", ".circleci/config.yml", "Jenkinsfile", ".drone.yml"} {
		a.CI = append(a.CI, globRel(root, g)...)
	}
	for _, g := range []string{".husky/*", ".pre-commit-config.yaml", "lefthook.yml", ".lefthook.yml", ".githooks/*"} {
		a.Hooks = append(a.Hooks, globRel(root, g)...)
	}
	for _, f := range []string{".claude/settings.json", ".claude/settings.local.json", ".codex/hooks.json"} {
		if b, err := os.ReadFile(filepath.Join(root, f)); err == nil && strings.Contains(string(b), `"hooks"`) {
			a.Hooks = append(a.Hooks, f)
		}
	}
	return a
}

func globRel(root, pattern string) []string {
	m, _ := filepath.Glob(filepath.Join(root, pattern))
	out := make([]string, 0, len(m))
	for _, p := range m {
		if rel, err := filepath.Rel(root, p); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

// ---- diffs, commands, turns ----

func diffs(ps []storage.HarnessPatch) Diffs {
	var d Diffs
	fails := counter{}
	for i := len(ps) - 1; i >= 0; i-- {
		p := ps[i]
		d.Total++
		switch {
		case p.Status == "applied":
			d.Merged++
		case p.Status == "pending" || p.Status == "queued":
			d.Pending++
		case p.Status == "rejected" && strings.HasPrefix(p.DecidedBy, "human:"):
			d.RejectedByPerson++
			if len(d.Rejected) < maxSamples {
				d.Rejected = append(d.Rejected, Item{Chat: p.ConversationID, Text: cut(p.Detail, 200), At: p.CreatedAt})
			}
		case p.Status == "rejected":
			d.Superseded++
		default:
			d.FailedToMerge++
			fails.add(errorKey(p.Detail))
		}
	}
	d.MergeFailures = fails.top(maxGroups, 1)
	return d
}

func commands(as []storage.HarnessAction) Commands {
	c := Commands{ByStatus: map[string]int{}}
	refused, failed := counter{}, counter{}
	for _, a := range as {
		c.ByStatus[a.Kind+"/"+a.Status]++
		switch a.Status {
		case "rejected":
			refused.add(a.Kind + ": " + cut(a.Target, 80))
		case "failed":
			failed.add(a.Kind + ": " + cut(a.Target, 80))
		}
	}
	c.Refused, c.Failed = refused.top(maxGroups, 1), failed.top(maxGroups, 1)
	return c
}

func turns(js []storage.HarnessJob) Turns {
	t := Turns{ByStatus: map[string]int{}}
	errs := counter{}
	for _, j := range js {
		t.ByStatus[j.Kind+"/"+j.Status]++
		if j.Status == "failed" {
			errs.add(firstNonEmpty(j.ErrorCode, errorKey(j.Error)))
		}
	}
	t.TopErrors = errs.top(maxGroups, 1)
	return t
}

// ---- validation ----

// checkRe finds a build/test/lint/typecheck command in what an agent ran.
var checkRe = regexp.MustCompile(`(?i)\b(go (test|vet|build)|(pnpm|npm|yarn|bun)( run)?( --[^ ]+)*( [^ ]+)? (test|typecheck|type-check|lint|build|check)|npx (vitest|jest|tsc|eslint|playwright)|vitest|jest|pytest|tsc( |$)|cargo (test|build|check|clippy)|make (test|check|build|lint)|eslint|ruff|mypy|mvn (test|verify)|gradlew? (test|build|check)|phpunit|artisan test|rspec|dotnet (test|build)|check-i18n|gofmt -l)`)

type check struct {
	conv string
	at   time.Time
	cmd  string
}

func validation(f storage.HarnessFacts) Validation {
	var checks []check
	for _, t := range f.Tools {
		var calls []struct{ Name, Summary string }
		if json.Unmarshal([]byte(t.Tools), &calls) != nil {
			continue
		}
		for _, c := range calls {
			if m := checkRe.FindString(c.Summary); m != "" {
				checks = append(checks, check{t.ConversationID, t.CreatedAt, strings.ToLower(m)})
			}
		}
	}
	for _, a := range f.Actions {
		if a.Kind == "run_command" && a.Status == "done" {
			if m := checkRe.FindString(a.Target); m != "" {
				checks = append(checks, check{a.ConversationID, a.CreatedAt, strings.ToLower(m)})
			}
		}
	}
	byConv := map[string][]check{}
	cmds := counter{}
	for _, c := range checks {
		byConv[c.conv] = append(byConv[c.conv], c)
		cmds.add(c.cmd)
	}
	var v Validation
	last := map[string]time.Time{} // a chat's previous merged diff: a check before it does not count again
	missing := map[string]bool{}
	for _, p := range f.Patches {
		if strings.HasPrefix(p.Detail, verify.Held) {
			v.GateHeld++
		}
		if p.Status != "applied" || p.ConversationID == "" {
			continue
		}
		v.MergedDiffs++
		ok := strings.HasPrefix(p.Detail, verify.Passed)
		if ok {
			v.GatePassed++
		}
		for _, c := range byConv[p.ConversationID] {
			if c.at.Before(p.CreatedAt) && !c.at.Before(last[p.ConversationID]) {
				ok = true
				break
			}
		}
		last[p.ConversationID] = p.CreatedAt
		if ok {
			v.WithCheck++
		} else if !missing[p.ConversationID] && len(v.WithoutCheck) < maxSamples {
			missing[p.ConversationID] = true
			v.WithoutCheck = append(v.WithoutCheck, p.ConversationID)
		}
	}
	v.CheckCommands = cmds.top(maxGroups, 1)
	return v
}

// ---- the person's corrections ----

var correctionRe = regexp.MustCompile(`(?i)(\bsai\b|chưa đúng|không đúng|không phải|làm lại|sửa lại|vẫn (lỗi|chưa|không|bị)|bị lỗi|quên|lại bị|chưa chạy|chưa test|hoàn tác|revert|tại sao|sao lại|\bwrong\b|not what|\bagain\b|still (fail|broken|not)|didn't|doesn't work|undo)`)

// officeWritten: a message office posts in the person's name (decided
// cards, relayed answers, Burn and workflow prompts), not what they typed.
func officeWritten(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "[") || strings.HasPrefix(s, "↩")
}

func corrections(ms []storage.HarnessMessage) Corrections {
	c := Corrections{}
	for _, m := range ms {
		if !officeWritten(m.Content) {
			c.Messages++
		}
	}
	for i := len(ms) - 1; i >= 0; i-- {
		if officeWritten(ms[i].Content) || !correctionRe.MatchString(ms[i].Content) {
			continue
		}
		c.Count++
		if len(c.Samples) < maxSamples+5 {
			c.Samples = append(c.Samples, Item{Chat: ms[i].ConversationID, Text: cut(oneLine(ms[i].Content), 200), At: ms[i].CreatedAt})
		}
	}
	return c
}

// ---- repeated requests ----

// Repeated requests are grouped by their words, accents and case aside: two
// messages are the same request when they share most words (Jaccard).
const (
	repeatSimilar   = 0.5
	repeatMinWords  = 4    // shorter ones ("ok", "tiếp tục") say nothing of the task
	repeatMinCount  = 3    // seen this often…
	repeatMinChats  = 2    // …in this many chats, so not one chat's back-and-forth
	repeatMaxScan   = 3000 // newest messages compared, to keep it linear enough
	repeatSampleLen = 200
)

var (
	wordRe    = regexp.MustCompile(`[\pL\pN]{2,}`)
	mentionRe = regexp.MustCompile(`@\S+`) // "@Thực thi làm đi" is a nudge, not a task
)

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(fold(mentionRe.ReplaceAllString(s, ""))), -1) {
		out[w] = true
	}
	return out
}

func fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, strings.ReplaceAll(strings.ReplaceAll(s, "đ", "d"), "Đ", "D"))
	if err != nil {
		return s
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return float64(n) / float64(len(a)+len(b)-n)
}

func repeats(ms []storage.HarnessMessage) []Repeat {
	type group struct {
		words map[string]bool
		msgs  []storage.HarnessMessage // newest first
	}
	var groups []*group
	scanned := 0
	for i := len(ms) - 1; i >= 0 && scanned < repeatMaxScan; i-- {
		m := ms[i]
		if officeWritten(m.Content) {
			continue
		}
		w := words(m.Content)
		if len(w) < repeatMinWords {
			continue
		}
		scanned++
		var best *group
		bestScore := repeatSimilar
		for _, g := range groups {
			if s := jaccard(w, g.words); s >= bestScore {
				best, bestScore = g, s
			}
		}
		if best == nil {
			groups = append(groups, &group{words: w, msgs: []storage.HarnessMessage{m}})
		} else {
			best.msgs = append(best.msgs, m)
		}
	}
	out := []Repeat{}
	for _, g := range groups {
		chats := map[string]bool{}
		var ids []string
		for _, m := range g.msgs {
			if !chats[m.ConversationID] {
				chats[m.ConversationID] = true
				if len(ids) < 3 {
					ids = append(ids, m.ConversationID)
				}
			}
		}
		if len(g.msgs) < repeatMinCount || len(chats) < repeatMinChats {
			continue
		}
		out = append(out, Repeat{Count: len(g.msgs), Chats: len(chats), Sample: cut(oneLine(g.msgs[0].Content), repeatSampleLen), Ids: ids, Last: g.msgs[0].CreatedAt.Format("2006-01-02")})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Chats != out[j].Chats {
			return out[i].Chats > out[j].Chats
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > maxGroups {
		out = out[:maxGroups]
	}
	return out
}

// ---- Burn and learning ----

func burn(f storage.HarnessFacts) Burn {
	b := Burn{ByStatus: map[string]int{}, Lessons: cut(f.BurnLessons, 1500)}
	for _, it := range f.Burn {
		b.ByStatus[it.Status]++
		if it.ReviewNote != "" && verdict.Of(it.ReviewNote) == "no" {
			b.ReviewDisagree++
		}
		if it.Status == "failed" && len(b.Failed) < maxSamples {
			b.Failed = append(b.Failed, cut(it.Title, 120))
		}
	}
	return b
}

func repeated(f storage.HarnessFacts) []Group {
	c := counter{}
	for _, j := range f.Jobs {
		if j.Status == "failed" {
			c.add("turn: " + firstNonEmpty(j.ErrorCode, errorKey(j.Error)))
		}
	}
	for _, a := range f.Actions {
		if a.Status == "failed" {
			c.add(a.Kind + ": " + errorKey(a.Detail))
		}
	}
	return c.top(maxGroups, 3)
}

// ---- helpers ----

type counter map[string]int

func (c counter) add(k string) {
	if k = strings.TrimSpace(k); k != "" {
		c[k]++
	}
}

// top: the most frequent keys seen at least atLeast times.
func (c counter) top(n, atLeast int) []Group {
	out := []Group{}
	for k, v := range c {
		if v >= atLeast {
			out = append(out, Group{k, v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key < out[j].Key
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

var digitsRe = regexp.MustCompile(`\d+`)

// errorKey groups failures by their first line, numbers aside.
func errorKey(msg string) string {
	msg = strings.TrimSpace(strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0])
	return cut(digitsRe.ReplaceAllString(msg, "N"), 120)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
