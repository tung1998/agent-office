package officetools

import (
	"cmp"
	"context"
	"fmt"
	"strings"
)

// What a project is working on right now (ADR-277), as Cursor's shared board
// shows it: the chats with edits not merged yet (files, who) and the open
// quests on the Burn board with who follows them. Read-only, also for an
// office the project is shared with (other_office call): teammates in two
// offices see what the other side is changing before they touch it.

func inProgressTool() Tool {
	return Tool{Name: "work_in_progress", Description: "What is being worked on in the project right now, to keep off a teammate's work: other chats with edits not merged yet (files, who, link) and open quests on the Burn board (status, who follows them). Call it before changing shared code; on a project of another office, call it through other_office call.",
		Schema: obj(map[string]any{}), ReadOnly: true}
}

// SetInProgress gives work_in_progress the chats' unmerged edits (the chat
// engine): lines about the project's chats other than conversationID.
func (t *Toolbox) SetInProgress(fn func(ctx context.Context, projectID, conversationID string) string) {
	t.inProgress = fn
}

const maxProgressQuests = 20

func (t *Toolbox) workInProgress(ctx context.Context, sc Scope) (string, error) {
	var sb strings.Builder
	sb.WriteString("## Chats with edits not merged yet\n")
	chats := ""
	if t.inProgress != nil {
		chats = t.inProgress(ctx, sc.ProjectID, sc.ConversationID)
	}
	sb.WriteString(cmp.Or(chats, "(none)") + "\n\n## Open quests on the Burn board\n")
	n := 0
	if b, err := t.store.Burn().Session(ctx, sc.ProjectID); err == nil {
		items, _ := t.store.Burn().Items(ctx, b.ID)
		for _, it := range items {
			if it.Kind != "quest" || it.Status == "done" || it.Status == "failed" || it.Status == "skipped" {
				continue
			}
			if n++; n > maxProgressQuests {
				break
			}
			line := fmt.Sprintf("- [%s] %s", it.Status, oneLineCut(it.Title, 100))
			if it.Owner != "" {
				line += " — follows: " + it.Owner
			}
			if len(it.Files) > 0 {
				line += " — files: " + strings.Join(it.Files[:min(len(it.Files), 8)], ", ")
			}
			sb.WriteString(line + "\n")
		}
	}
	if n == 0 {
		sb.WriteString("(none)\n")
	}
	return sb.String(), nil
}

func oneLineCut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
