package chat

import (
	"context"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/textrank"
)

// Auto-route (ADR-277): with the project policy's AutoRoute on, a person's
// message in a project chat that tags no agent goes to the project's agent
// whose name, role, description and skills fit it clearly better than the
// chat's own agent. No AI call: a word match, rare words weighing more.

type autoRouteKey struct{}

// WithAutoRoute marks ctx as carrying a message the person typed in a chat
// without picking an agent (the dashboard's send, a queued message): only
// such a message may be auto-routed, never a relay, a bot, an automation,
// a delegation or a message office writes itself.
func WithAutoRoute(ctx context.Context) context.Context {
	return context.WithValue(ctx, autoRouteKey{}, true)
}

func autoRouteOf(ctx context.Context) bool {
	on, _ := ctx.Value(autoRouteKey{}).(bool)
	return on
}

// routeMinScore and routeMargin: how much better another agent must fit
// before it takes the message from the chat's agent (one rare word is
// about 1.1 among two agents).
const (
	routeMinScore = 1.0
	routeMargin   = 1.0
)

// routeStop are common words (folded) that say nothing about whose job a
// message is.
var routeStop = map[string]bool{}

func init() {
	for _, w := range strings.Fields("and for with this that from what when how can you your please help make about into " +
		"cho cua nay khong duoc nhung voi the toi ban minh lam giup hay mot cac nhe nha thi roi can sao gi la va co de da dang se " +
		"nao nua ma neu khi vao ra len xem lai di tu trong ngoai tren duoi them sua viec cai con anh chi em") {
		routeStop[textrank.Fold(w)] = true
	}
}

// routeWords are the words of s worth matching: normalized (accents off,
// abbreviations expanded), split on what is not a letter or digit, at
// least 2 letters, not a common word.
func routeWords(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(textrank.Normalize(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(w) < 2 || routeStop[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// routeProfile is what an agent is matched on.
type routeProfile struct {
	agent storage.Agent
	text  string // name, role, description, skills (name + why)
}

// pickAgent is the agent the message fits clearly best, or the chat's own
// (ok false) when none does: the best must score routeMinScore, beat the
// chat's agent by routeMargin and twice over, and not tie with another.
// Paused agents are never picked.
func pickAgent(text, currentID string, profiles []routeProfile) (storage.Agent, bool) {
	words := routeWords(text)
	if len(words) == 0 || len(profiles) < 2 {
		return storage.Agent{}, false
	}
	docs := make([]map[string]bool, len(profiles))
	for i, p := range profiles {
		docs[i] = map[string]bool{}
		for _, w := range routeWords(p.text) {
			docs[i][w] = true
		}
	}
	scores := routeScores(words, docs)
	cur, best, second := 0.0, -1, 0.0
	for i, p := range profiles {
		if p.agent.ID == currentID {
			cur = scores[i]
			continue
		}
		if p.agent.Disabled {
			continue
		}
		switch {
		case best < 0 || scores[i] > scores[best]:
			if best >= 0 {
				second = scores[best]
			}
			best = i
		case scores[i] > second:
			second = scores[i]
		}
	}
	if best < 0 {
		return storage.Agent{}, false
	}
	s := scores[best]
	if s < routeMinScore || s < cur+routeMargin || s < 2*cur || s-second < 1e-9 {
		return storage.Agent{}, false
	}
	return profiles[best].agent, true
}

// routeScores: each doc's sum of the weights of the message words it has,
// a word fewer agents have weighing more (idf).
func routeScores(words []string, docs []map[string]bool) []float64 {
	scores := make([]float64, len(docs))
	for _, w := range words {
		df := 0
		for _, d := range docs {
			if d[w] {
				df++
			}
		}
		if df == 0 {
			continue
		}
		idf := math.Log(float64(len(docs)+1) / float64(df))
		for i, d := range docs {
			if d[w] {
				scores[i] += idf
			}
		}
	}
	return scores
}

// autoRoute is the agent that answers an untagged message in place of the
// chat's agent, when the project turned AutoRoute on and one fits clearly
// better (ok false: the chat's agent answers).
func (e *Engine) autoRoute(ctx context.Context, conv storage.Conversation, current storage.Agent, agents []storage.Agent, text string) (storage.Agent, bool) {
	if !autoRouteOf(ctx) || len(agents) < 2 || conv.Purpose != "" || conv.TaskID != "" || current.Disabled || e.isAssistant(ctx, conv.ProjectID) {
		return storage.Agent{}, false
	}
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, "#") { // a skill or workflow call: the chat's agent takes it
		return storage.Agent{}, false
	}
	if !perm.LoadPolicy(ctx, e.store, conv.ProjectID).AutoRoute {
		return storage.Agent{}, false
	}
	profiles := make([]routeProfile, 0, len(agents))
	for _, a := range agents {
		if a.Disabled && a.ID != current.ID {
			continue
		}
		parts := []string{a.Name, a.Key, a.Role, a.Description}
		if skills, err := e.store.AgentSkills().List(ctx, a.ID); err == nil {
			for _, s := range skills {
				parts = append(parts, s.Name, s.Why)
			}
		}
		profiles = append(profiles, routeProfile{agent: a, text: strings.Join(parts, "\n")})
	}
	return pickAgent(text, current.ID, profiles)
}
