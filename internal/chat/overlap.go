package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/worktree"
)

// Other chats' edits not merged yet (ADR-277): each chat works in a worktree
// of its own, so two chats on one file only meet when the second diff fails
// to merge. A turn is told the files the project's other chats are changing
// (one short line), and in full the chats on files it changed too.
// work_in_progress gives the full list on demand.

const (
	maxSiblingChats = 8                  // chats described
	maxSiblingFiles = 10                 // files per chat described
	maxBriefFiles   = 15                 // files in the one-line brief
	siblingAge      = 3 * 24 * time.Hour // a chat untouched longer is left out
	siblingTimeout  = 3 * time.Second
	siblingWorkers  = 4
)

// siblingOf is the conversation a worktree name belongs to ("chat-<id>" or
// "chat-<id>--<agent>"), "" for a worktree that is not a chat's.
func siblingOf(name string) string {
	id, ok := strings.CutPrefix(name, "chat-")
	if !ok {
		return ""
	}
	id, _, _ = strings.Cut(id, "--")
	return id
}

type sibling struct {
	conv   storage.Conversation
	files  []string // not merged yet
	shared []string // also changed by the asking chat
}

// siblings are the project's other recent chats with edits not merged yet,
// those sharing files with mine (a worktree, "" = none) first. The store is
// read first (cheap), git only for the chats left, a few at a time.
func (e *Engine) siblings(ctx context.Context, projectID, conversationID, mine string) []sibling {
	if e.trees == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, siblingTimeout)
	defer cancel()
	type tree struct {
		conv storage.Conversation
		dir  string
	}
	var trees []tree
	convs := map[string]*storage.Conversation{}
	for _, name := range e.trees.Names(projectID) {
		id := siblingOf(name)
		if id == "" || id == conversationID {
			continue
		}
		c, ok := convs[id]
		if !ok {
			if got, err := e.store.Chat().GetConversation(ctx, id); err == nil && got.ProjectID == projectID && got.Cleaned == "" && time.Since(got.UpdatedAt) < siblingAge {
				c = &got
			}
			convs[id] = c
		}
		if c != nil {
			trees = append(trees, tree{*c, e.trees.Path(projectID, name)})
		}
	}
	if len(trees) == 0 {
		return nil
	}
	var own []string
	if mine != "" {
		own, _ = worktree.Touched(ctx, mine)
	}
	found := make([][]string, len(trees))
	var wg sync.WaitGroup
	sem := make(chan struct{}, siblingWorkers)
	for i, t := range trees {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() == nil {
				found[i], _ = worktree.Touched(ctx, t.dir)
			}
		}()
	}
	wg.Wait()
	byConv := map[string]int{}
	var out []sibling
	for i, t := range trees {
		if len(found[i]) == 0 {
			continue
		}
		j, ok := byConv[t.conv.ID]
		if !ok {
			j = len(out)
			byConv[t.conv.ID] = j
			out = append(out, sibling{conv: t.conv})
		}
		for _, f := range found[i] {
			if !slices.Contains(out[j].files, f) {
				out[j].files = append(out[j].files, f)
				if slices.Contains(own, f) {
					out[j].shared = append(out[j].shared, f)
				}
			}
		}
	}
	slices.SortStableFunc(out, func(a, b sibling) int { return min(len(b.shared), 1) - min(len(a.shared), 1) })
	return out
}

// overlaps describes the other chats' unmerged edits in full (work_in_progress);
// "" when there are none.
func (e *Engine) overlaps(ctx context.Context, projectID, conversationID, mine string) string {
	sibs := e.siblings(ctx, projectID, conversationID, mine)
	lines := make([]string, 0, min(len(sibs), maxSiblingChats))
	for _, s := range sibs[:min(len(sibs), maxSiblingChats)] {
		lines = append(lines, siblingLine(projectID, s))
	}
	return strings.Join(lines, "\n")
}

// overlapBrief is what a turn is told: the chats on files it changed too, in
// full; the other files being changed, in one line. "", "" when nothing.
func (e *Engine) overlapBrief(ctx context.Context, projectID, conversationID, mine string) (shared, files string) {
	var lines, rest []string
	for _, s := range e.siblings(ctx, projectID, conversationID, mine) {
		if len(s.shared) > 0 && len(lines) < maxSiblingChats {
			lines = append(lines, siblingLine(projectID, s))
			continue
		}
		for _, f := range s.files {
			if !slices.Contains(rest, f) {
				rest = append(rest, f)
			}
		}
	}
	more := ""
	if len(rest) > maxBriefFiles {
		more = fmt.Sprintf(" (+%d more)", len(rest)-maxBriefFiles)
		rest = rest[:maxBriefFiles]
	}
	if len(rest) > 0 {
		files = strings.Join(rest, ", ") + more
	}
	return strings.Join(lines, "\n"), files
}

func siblingLine(projectID string, s sibling) string {
	title := strings.TrimSpace(s.conv.Title)
	if title == "" {
		title = s.conv.ID
	}
	if r := []rune(title); len(r) > 60 {
		title = string(r[:60]) + "…"
	}
	list, more := s.files, ""
	if len(list) > maxSiblingFiles {
		more = fmt.Sprintf(" (+%d more)", len(list)-maxSiblingFiles)
		list = list[:maxSiblingFiles]
	}
	line := fmt.Sprintf("- %q (/projects/%s?tab=chat&c=%s)", title, projectID, s.conv.ID)
	if by := strings.TrimPrefix(s.conv.CreatedBy, "human:"); by != "" {
		line += " by " + by
	}
	line += ": " + strings.Join(list, ", ") + more
	if len(s.shared) > 0 {
		line += "\n  ⚠ you changed these too: " + strings.Join(s.shared, ", ")
	}
	return line
}
