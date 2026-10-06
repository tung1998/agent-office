package channels

import (
	"context"
	"strings"

	"github.com/tung1998/agent-office/internal/gitops"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// DefaultHeader is the line on top of a bot's answer when its settings say none.
const DefaultHeader = "{agent} · {project} · {branch}"

// HeaderLine fills a header template ("" = the default, "-" = none): the
// parts it has, joined by " · " (a script's answer names no agent). Discord
// shows it as small grey text (-#).
func HeaderLine(tpl, kind, agent, project, branch string) string {
	tpl = strings.TrimSpace(tpl)
	if tpl == "-" {
		return ""
	}
	if tpl == "" {
		tpl = DefaultHeader
	}
	line := strings.NewReplacer("{agent}", agent, "{project}", project, "{repo}", project, "{branch}", branch).Replace(tpl)
	var parts []string
	for _, p := range strings.Split(line, "·") {
		if p = strings.TrimSpace(p); p != "" && p != "()" {
			parts = append(parts, p)
		}
	}
	line = strings.Join(parts, " · ")
	if line == "" {
		return ""
	}
	if kind == "discord" {
		return "-# " + line
	}
	return line
}

// headed puts the channel's header on an answer. The agent is the one who
// wrote it: a follow-up signed "Name:\n…" by another agent of the project is
// named in the header instead.
func (m *Manager) headed(ctx context.Context, ch storage.Channel, p trigger.ChannelPayload, origin storage.Job, text string) string {
	if strings.TrimSpace(ch.Header) == "-" || text == "" {
		return text
	}
	agent := ""
	if name, rest, ok := strings.Cut(text, ":\n"); ok && !strings.ContainsAny(name, "\n") && len(name) <= 60 {
		if agents, err := m.engine.Agents(ctx, ch.ProjectID); err == nil {
			for _, a := range agents {
				if a.Name == name {
					agent, text = name, rest
				}
			}
		}
	}
	if agent == "" && p.ConversationID != "" {
		if c, err := m.store.Chat().GetConversation(ctx, p.ConversationID); err == nil {
			agent = c.AgentName
		}
	}
	if agent == "" && origin.AgentID != "" {
		if a, err := m.store.Agents().Get(ctx, origin.AgentID); err == nil {
			agent = a.Name
		}
	}
	project, branch := "", ""
	if r, err := m.store.Repos().Get(ctx, ch.ProjectID); err == nil {
		project = r.Name
		branch = gitops.CurrentBranch(ctx, r.Path)
	}
	if line := HeaderLine(ch.Header, ch.Kind, agent, project, branch); line != "" {
		return line + "\n" + text
	}
	return text
}
