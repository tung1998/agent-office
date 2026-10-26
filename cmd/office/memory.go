package main

import (
	"cmp"
	"context"
	"strings"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
)

// compactNotes rewrites an agent's notes shorter with a fast model, no tools.
func compactNotes(st storage.Store, engine *chat.Engine) memory.Compactor {
	return func(ctx context.Context, projectID, agentID string, items []storage.Memory) ([]string, error) {
		project, err := st.Repos().Get(ctx, projectID)
		if err != nil {
			return nil, err
		}
		agent, err := st.Agents().Get(ctx, agentID)
		if err != nil {
			return nil, err
		}
		notes := make([]string, 0, len(items))
		for _, m := range items {
			notes = append(notes, strings.ReplaceAll(m.Text, "\n", " "))
		}
		prompt := prompts.Render("memory/compact", map[string]any{"Notes": notes, "Topic": items[0].Topic}) + "\n"
		res, err := engine.Invoke(chat.WithNoTools(chat.WithModelTier(ctx, storage.TierFast)), project, agent, prompt, nil, "memory_compact", nil)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, l := range strings.Split(res.Text, "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "- ") {
				out = append(out, strings.TrimPrefix(l, "- "))
			}
		}
		return out, nil
	}
}

// reviewNotes keeps an agent's notes up to date after a turn (ADR-215): a
// fast model, no tools, reads the turn beside the notes and answers with
// ADD/EDIT/DROP lines, and LINK for a link to keep (ADR-218).
func reviewNotes(st storage.Store, engine *chat.Engine) memory.Reviewer {
	return func(ctx context.Context, projectID, agentID string, items []storage.Memory, asked, answer string) ([]memory.Op, error) {
		project, err := st.Repos().Get(ctx, projectID)
		if err != nil {
			return nil, err
		}
		agent, err := st.Agents().Get(ctx, agentID)
		if err != nil {
			return nil, err
		}
		notes := make([]string, 0, len(items))
		for _, m := range items {
			notes = append(notes, m.ID+" | "+cmp.Or(m.Topic, "-")+" | "+strings.ReplaceAll(m.Text, "\n", " "))
		}
		prompt := prompts.Render("memory/review", map[string]any{"Notes": notes, "Asked": cut(asked, 3000), "Answer": cut(answer, 4000)}) + "\n"
		res, err := engine.Invoke(chat.WithNoTools(chat.WithModelTier(ctx, storage.TierFast)), project, agent, prompt, nil, "memory_review", nil)
		if err != nil {
			return nil, err
		}
		return parseReview(res.Text), nil
	}
}

// parseReview reads the ADD/EDIT/DROP/LINK lines of a review; anything else is ignored.
func parseReview(text string) []memory.Op {
	var out []memory.Op
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- "))
		verb, rest, _ := strings.Cut(l, " ")
		parts := strings.Split(rest, "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		dash := func(s string) string {
			if s == "-" {
				return ""
			}
			return s
		}
		switch strings.ToUpper(verb) {
		case "ADD":
			if len(parts) >= 3 {
				out = append(out, memory.Op{Op: "add", Topic: dash(parts[0]), Summary: dash(parts[1]), Text: strings.Join(parts[2:], " | ")})
			}
		case "EDIT":
			if len(parts) >= 2 {
				out = append(out, memory.Op{Op: "edit", ID: parts[0], Text: strings.Join(parts[1:], " | ")})
			}
		case "LINK": // ADR-218: kind | title | url | note
			if len(parts) >= 3 && parts[2] != "" {
				o := memory.Op{Op: "link", Topic: dash(parts[0]), Summary: dash(parts[1]), Text: parts[2]}
				if len(parts) > 3 {
					o.ID = dash(strings.Join(parts[3:], " | "))
				}
				out = append(out, o)
			}
		case "DROP":
			if parts[0] != "" {
				out = append(out, memory.Op{Op: "drop", ID: strings.Fields(parts[0])[0]})
			}
		}
	}
	return out
}

// cut keeps the first n runes of s.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
