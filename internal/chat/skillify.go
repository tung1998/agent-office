package chat

import (
	"context"
	"strings"

	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
)

// SkillifyCommand is "/skillify [note]" (ADR-281): the chat's agent turns what
// the chat did into a skill of the project, from the steps office recorded
// (tool calls that worked, what the person asked). It answers with a ```skill
// block, which the dashboard opens in the skill editor for the person to
// review and save: nothing is written on its own. A project skill of the same
// name wins (it is called as any skill).
const SkillifyCommand = "skillify"

const (
	skillifyMaxSteps = 80
	skillifyMaxAsks  = 20
)

// skillifyPrompt is the prompt of a /skillify, from the chat's messages so
// far (the /skillify itself left out) and the person's note.
func skillifyPrompt(msgs []storage.Message, note string) string {
	var steps, asks []string
	seen := map[string]bool{}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			if name, _, ok := automation.ParseSkillCall(strings.TrimSpace(m.Content)); ok && name == SkillifyCommand {
				continue
			}
			if l := firstLine(m.Content); l != "" {
				asks = append(asks, truncate(l, 200))
			}
		case "assistant":
			for _, t := range m.Tools {
				if t.Error {
					continue
				}
				s := strings.TrimSpace(t.Name + ": " + oneLine(t.Summary))
				if s = truncate(s, 240); !seen[s] {
					seen[s] = true
					steps = append(steps, s)
				}
			}
		}
	}
	steps = lastN(steps, skillifyMaxSteps)
	asks = lastN(asks, skillifyMaxAsks)
	return prompts.Render("chat/skillify", map[string]any{"Steps": steps, "Asks": asks, "Request": strings.TrimSpace(note)})
}

// skillify reports whether text is a /skillify office answers, and its note:
// in a person's chat (not a bot's, an automation's or an editor's), when the
// project has no skill of that name.
func (e *Engine) skillify(_ context.Context, conv storage.Conversation, projectPath, text string) (string, bool) {
	name, note, ok := automation.ParseSkillCall(strings.TrimSpace(text))
	if !ok || name != SkillifyCommand || conv.Purpose != "" {
		return "", false
	}
	if _, _, _, err := automation.LoadSkill(userHome(), projectPath, SkillifyCommand); err == nil {
		return "", false
	}
	return note, true
}

// lastN keeps the last n: the latest steps are the ones that worked.
func lastN(list []string, n int) []string {
	if len(list) > n {
		return list[len(list)-n:]
	}
	return list
}
