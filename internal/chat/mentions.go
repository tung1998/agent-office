package chat

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/tung1998/agent-office/internal/storage"
)

var codeSpans = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~|`[^`\n]*`")

// Mentions finds the agents tagged in text ("@Name" or "@key", any case), in
// the order they appear, once each; the longest name wins ("@Dev Lead" over
// "@Dev"), and tags inside code or in the middle of a word (emails) do not count.
func Mentions(text string, agents []storage.Agent) []storage.Agent {
	text = codeSpans.ReplaceAllStringFunc(text, func(s string) string { return strings.Repeat(" ", len(s)) })
	lower := []rune(strings.ToLower(text))
	type cand struct {
		name  []rune
		agent storage.Agent
	}
	var cands []cand
	for _, a := range agents {
		for _, n := range []string{a.Name, a.Key} {
			if n = strings.TrimSpace(strings.ToLower(n)); n != "" {
				cands = append(cands, cand{[]rune(n), a})
			}
		}
	}
	wordy := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	var out []storage.Agent
	seen := map[string]bool{}
	for i, r := range lower {
		if r != '@' || (i > 0 && wordy(lower[i-1])) {
			continue
		}
		var best *cand
		for j := range cands {
			c := &cands[j]
			end := i + 1 + len(c.name)
			if end > len(lower) || string(lower[i+1:end]) != string(c.name) {
				continue
			}
			if end < len(lower) && (wordy(lower[end]) || lower[end] == '-') {
				continue // "@Dev" inside "@Developer"
			}
			if best == nil || len(c.name) > len(best.name) {
				best = c
			}
		}
		if best != nil && !seen[best.agent.ID] {
			seen[best.agent.ID] = true
			out = append(out, best.agent)
		}
	}
	return out
}
