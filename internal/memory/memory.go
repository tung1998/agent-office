// Package memory keeps each agent's long-term notes in a project (ADR-068):
// what it learned (conventions, decisions, what the person prefers), put at
// the top of every new conversation. Too long, the notes are compacted by a
// fast model; what they were is kept and can be put back.
package memory

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/storage"
)

// Compactor rewrites notes shorter (a fast model): the new notes, one each.
type Compactor func(ctx context.Context, projectID, agentID string, items []storage.Memory) ([]string, error)

// Service adds, compacts and restores notes.
type Service struct {
	store   storage.Store
	compact Compactor
	// Limit: characters of notes before they are compacted (and the most a
	// conversation is given).
	Limit int
}

// MaxNote is the longest one note may be.
const MaxNote = 500

func New(store storage.Store, c Compactor) *Service {
	return &Service{store: store, compact: c, Limit: 4000}
}

var ErrEmpty = errors.New("ghi nhớ trống")

// Add keeps a note (source: person | agent), then compacts if they grew too long.
func (s *Service) Add(ctx context.Context, projectID, agentID, text, source, by string) (storage.Memory, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return storage.Memory{}, ErrEmpty
	}
	if utf8.RuneCountInString(text) > MaxNote {
		text = string([]rune(text)[:MaxNote-1]) + "…"
	}
	m, err := s.store.Memories().Create(ctx, storage.Memory{ProjectID: projectID, AgentID: agentID, Text: text, Source: source, CreatedBy: by})
	if err != nil {
		return m, err
	}
	if s.size(ctx, projectID, agentID) > s.Limit && s.compact != nil {
		_ = s.Compact(ctx, projectID, agentID, "tự rút gọn khi sổ quá dài")
	}
	return m, nil
}

func (s *Service) size(ctx context.Context, projectID, agentID string) int {
	list, _ := s.store.Memories().List(ctx, projectID, agentID)
	n := 0
	for _, m := range list {
		n += utf8.RuneCountInString(m.Text)
	}
	return n
}

// Compact rewrites the notes shorter, keeping what they were.
func (s *Service) Compact(ctx context.Context, projectID, agentID, reason string) error {
	if s.compact == nil {
		return errors.New("chưa rút gọn được: không có model")
	}
	list, err := s.store.Memories().List(ctx, projectID, agentID)
	if err != nil || len(list) == 0 {
		return err
	}
	texts, err := s.compact(ctx, projectID, agentID, list)
	if err != nil {
		return err
	}
	var items []storage.Memory
	for i, t := range texts {
		if t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "- ")); t != "" { // in the place of the old ones
			items = append(items, storage.Memory{Text: t, Source: "compact", CreatedAt: list[0].CreatedAt.Add(time.Duration(i) * time.Microsecond)})
		}
	}
	if len(items) == 0 {
		return errors.New("rút gọn ra rỗng: giữ nguyên")
	}
	compacted := map[string]bool{}
	for _, m := range list {
		compacted[m.ID] = true
	}
	return s.store.InTx(ctx, func(tx storage.Store) error {
		if _, err := tx.Memories().SaveRevision(ctx, storage.MemoryRevision{ProjectID: projectID, AgentID: agentID, Items: list, Reason: reason}); err != nil {
			return err
		}
		now, err := tx.Memories().List(ctx, projectID, agentID)
		if err != nil {
			return err
		}
		for _, m := range now { // written while the model compacted: kept
			if !compacted[m.ID] {
				items = append(items, m)
			}
		}
		return tx.Memories().Replace(ctx, projectID, agentID, items)
	})
}

// Restore puts a revision back (what is there now is kept as a revision too).
func (s *Service) Restore(ctx context.Context, revisionID, by string) error {
	rev, err := s.store.Memories().GetRevision(ctx, revisionID)
	if err != nil {
		return err
	}
	now, err := s.store.Memories().List(ctx, rev.ProjectID, rev.AgentID)
	if err != nil {
		return err
	}
	if _, err := s.store.Memories().SaveRevision(ctx, storage.MemoryRevision{ProjectID: rev.ProjectID, AgentID: rev.AgentID, Items: now, Reason: "trước khi khôi phục (" + by + ")"}); err != nil {
		return err
	}
	items := make([]storage.Memory, len(rev.Items))
	for i, m := range rev.Items {
		items[i] = storage.Memory{Text: m.Text, Source: m.Source, CreatedBy: m.CreatedBy, CreatedAt: m.CreatedAt}
	}
	return s.store.Memories().Replace(ctx, rev.ProjectID, rev.AgentID, items)
}

// Block is the notes as a conversation's system prompt gets them ("" = none).
func (s *Service) Block(ctx context.Context, projectID, agentID string) string {
	return Block(ctx, s.store, projectID, agentID, s.Limit)
}

// Block formats an agent's notes for its system prompt, newest kept when too long.
func Block(ctx context.Context, store storage.Store, projectID, agentID string, limit int) string {
	list, err := store.Memories().List(ctx, projectID, agentID)
	if err != nil || len(list) == 0 {
		return ""
	}
	var lines []string
	n := 0
	for i := len(list) - 1; i >= 0; i-- { // the newest first, within the limit
		n += utf8.RuneCountInString(list[i].Text)
		if n > limit && len(lines) > 0 {
			break
		}
		lines = append([]string{"- " + list[i].Text}, lines...)
	}
	return "\n\n## Ghi nhớ của bạn ở project này (từ các lần trước)\n" + strings.Join(lines, "\n") +
		"\nDùng những điều này khi làm việc. Học được điều gì đáng nhớ lâu dài (quy ước, quyết định, điều người dùng muốn) thì dùng công cụ remember; đừng ghi việc chỉ của lần này."
}

func autoKey(projectID string) string { return "memory_auto/" + projectID }

// Auto: an agent's notes are kept without a person approving each.
func (s *Service) Auto(ctx context.Context, projectID string) bool {
	var on bool
	_, _ = s.store.Settings().Get(ctx, autoKey(projectID), &on)
	return on
}

func (s *Service) SetAuto(ctx context.Context, projectID string, on bool) error {
	return s.store.Settings().Set(ctx, autoKey(projectID), on)
}
