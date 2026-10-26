// Package memory keeps each agent's long-term notes in a project (ADR-068):
// what it learned (conventions, decisions, what the person prefers), put at
// the top of every new conversation. Too long, the notes are compacted by a
// fast model; what they were is kept and can be put back.
//
// Two tiers (ADR-134): core notes (no topic) are always in the prompt; a
// topic's notes are not, only a line of the index (topic: summary), and the
// agent reads them with the recall tool when it works in that area.
package memory

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
)

// Compactor rewrites notes shorter (a fast model): the new notes, one each.
type Compactor func(ctx context.Context, projectID, agentID string, items []storage.Memory) ([]string, error)

// Reviewer reads a turn an agent just answered beside its notes (with
// their ids) and says what to keep up to date: notes to add, rewrite or drop.
type Reviewer func(ctx context.Context, projectID, agentID string, notes []storage.Memory, asked, answer string) ([]Op, error)

// Op is one change to an agent's notes: add (Text, Topic, Summary), edit
// (ID, Text) or drop (ID); or a link to keep in the project's links (Topic
// its kind, Summary its title, Text its url, ID a note; ADR-218).
type Op struct {
	Op      string
	ID      string
	Text    string
	Topic   string
	Summary string
}

// LinkSaver keeps a link in the project's links (ADR-218).
type LinkSaver func(ctx context.Context, projectID, agentID string, o Op) error

// Service adds, compacts and restores notes.
type Service struct {
	store   storage.Store
	compact Compactor
	review  Reviewer
	link    LinkSaver
	busy    sync.Map // projectID/agentID: a review under way
	// Limit: characters of core notes before they are compacted (and the
	// most of them a conversation is given).
	Limit int
	// TopicLimit: characters of one topic's notes before they are compacted.
	TopicLimit int
}

const (
	// MaxNote is the longest one note may be.
	MaxNote = 500
	// MaxSummary is the longest a topic's index line may be.
	MaxSummary = 150
	// CoreLimit, IndexLimit: what of the core notes and of the topic index a
	// conversation is given; TopicLimit: one topic before it is compacted.
	CoreLimit  = 2500
	IndexLimit = 1500
	TopicLimit = 4000
)

func New(store storage.Store, c Compactor) *Service {
	return &Service{store: store, compact: c, Limit: CoreLimit, TopicLimit: TopicLimit}
}

// SetReviewer keeps the notes up to date after each turn (ADR-215).
func (s *Service) SetReviewer(r Reviewer) { s.review = r }

// SetLinkSaver keeps the links a review finds (ADR-218).
func (s *Service) SetLinkSaver(l LinkSaver) { s.link = l }

var ErrEmpty = errors.New("ghi nhớ trống")

// Add keeps a core note (source: person | agent), then compacts if they grew too long.
func (s *Service) Add(ctx context.Context, projectID, agentID, text, source, by string) (storage.Memory, error) {
	return s.Keep(ctx, storage.Memory{ProjectID: projectID, AgentID: agentID, Text: text, Source: source, CreatedBy: by})
}

// Keep keeps a note, core or of m.Topic, then compacts its group if it grew too long.
func (s *Service) Keep(ctx context.Context, m storage.Memory) (storage.Memory, error) {
	m.Text = strings.TrimSpace(m.Text)
	if m.Text == "" {
		return storage.Memory{}, ErrEmpty
	}
	if utf8.RuneCountInString(m.Text) > MaxNote {
		m.Text = string([]rune(m.Text)[:MaxNote-1]) + "…"
	}
	m.Topic, m.Summary = Clean(m.Topic, m.Summary)
	m, err := s.store.Memories().Create(ctx, m)
	if err != nil {
		return m, err
	}
	limit := s.Limit
	if m.Topic != "" {
		limit = s.TopicLimit
	}
	if s.size(ctx, m.ProjectID, m.AgentID, m.Topic) > limit && s.compact != nil {
		_ = s.CompactTopic(ctx, m.ProjectID, m.AgentID, m.Topic, "tự rút gọn khi sổ quá dài")
	}
	return m, nil
}

// Revise changes an agent's notes in one go: drops the ones in drop and
// rewrites the ones in edit (only the agent's own), then keeps add; what the
// notes were is kept as a revision, so it can be put back (ADR-215).
func (s *Service) Revise(ctx context.Context, projectID, agentID string, add []storage.Memory, edit map[string]string, drop []string, reason string) (dropped int, err error) {
	touched := map[string]bool{}
	err = s.store.InTx(ctx, func(tx storage.Store) error {
		list, err := tx.Memories().List(ctx, projectID, agentID)
		if err != nil {
			return err
		}
		own := map[string]storage.Memory{}
		for _, m := range list {
			own[m.ID] = m
		}
		if _, err := tx.Memories().SaveRevision(ctx, storage.MemoryRevision{ProjectID: projectID, AgentID: agentID, Items: list, Reason: reason}); err != nil {
			return err
		}
		for _, id := range drop {
			if _, ok := own[id]; !ok {
				continue
			}
			if err := tx.Memories().Delete(ctx, id); err != nil {
				return err
			}
			delete(own, id)
			dropped++
		}
		for id, text := range edit {
			m, ok := own[id]
			if text = clip(text); !ok || text == "" {
				continue
			}
			m.Text = text
			if err := tx.Memories().Update(ctx, m); err != nil {
				return err
			}
			touched[m.Topic] = true
		}
		for _, m := range add {
			if m.Text = clip(m.Text); m.Text == "" {
				continue
			}
			m.ProjectID, m.AgentID = projectID, agentID
			m.Topic, m.Summary = Clean(m.Topic, m.Summary)
			if _, err := tx.Memories().Create(ctx, m); err != nil {
				return err
			}
			touched[m.Topic] = true
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for topic := range touched {
		limit := s.Limit
		if topic != "" {
			limit = s.TopicLimit
		}
		if s.size(ctx, projectID, agentID, topic) > limit && s.compact != nil {
			_ = s.CompactTopic(ctx, projectID, agentID, topic, "tự rút gọn khi sổ quá dài")
		}
	}
	return dropped, nil
}

// clip trims a note and cuts it to MaxNote.
func clip(text string) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > MaxNote {
		text = string([]rune(text)[:MaxNote-1]) + "…"
	}
	return text
}

// Most a review after one turn adds, and rewrites or drops (ADR-215).
const (
	ReviewAdds    = 2
	ReviewChanges = 10
	ReviewLinks   = 3
)

// Review keeps an agent's notes up to date after a turn it answered: a fast
// model reads the turn beside the notes and adds what was learned, rewrites
// what turned out wrong and drops what is outdated, repeated or never of use
// again (ADR-215). Off when the project does not keep notes on its own; one
// review at a time per agent (a turn ending meanwhile is skipped).
func (s *Service) Review(ctx context.Context, projectID, agentID, asked, answer string) error {
	if s.review == nil || !s.Auto(ctx, projectID) {
		return nil
	}
	key := projectID + "/" + agentID
	if _, busy := s.busy.LoadOrStore(key, true); busy {
		return nil
	}
	defer s.busy.Delete(key)
	list, err := s.store.Memories().List(ctx, projectID, agentID)
	if err != nil {
		return err
	}
	ops, err := s.review(ctx, projectID, agentID, list, asked, answer)
	if err != nil || len(ops) == 0 {
		return err
	}
	own := map[string]bool{}
	for _, m := range list {
		own[m.ID] = true
	}
	var add []storage.Memory
	edit, drop := map[string]string{}, []string{}
	links := 0
	for _, o := range ops {
		switch {
		case o.Op == "link" && s.link != nil && links < ReviewLinks && strings.TrimSpace(o.Text) != "":
			links++
			_ = s.link(ctx, projectID, agentID, o) // a link it could not keep leaves the notes as they are
		case o.Op == "add" && len(add) < ReviewAdds && strings.TrimSpace(o.Text) != "":
			if _, dup := s.Duplicate(ctx, projectID, agentID, o.Text); !dup {
				add = append(add, storage.Memory{Text: o.Text, Topic: o.Topic, Summary: o.Summary, Source: "review"})
			}
		case o.Op == "edit" && own[o.ID] && len(edit)+len(drop) < ReviewChanges:
			edit[o.ID] = o.Text
		case o.Op == "drop" && own[o.ID] && len(edit)+len(drop) < ReviewChanges:
			drop = append(drop, o.ID)
		}
	}
	if len(add)+len(edit)+len(drop) == 0 {
		return nil
	}
	_, err = s.Revise(ctx, projectID, agentID, add, edit, drop, "dọn sau lượt chạy")
	return err
}

// Clean makes topic a slug and summary one short line ("" for a core note).
func Clean(topic, summary string) (string, string) {
	topic = Slug(topic)
	if topic == "" {
		return "", ""
	}
	summary = strings.Join(strings.Fields(summary), " ")
	if utf8.RuneCountInString(summary) > MaxSummary {
		summary = string([]rune(summary)[:MaxSummary-1]) + "…"
	}
	return topic, summary
}

// Slug: "Thanh toán PayPal" → "thanh-toan-paypal" (40 characters at most).
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range fold(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	return b.String()
}

// fold lowercases and drops diacritics so "Đơn hàng" matches "don hang".
func fold(s string) string {
	s = strings.NewReplacer("đ", "d", "Đ", "d").Replace(strings.ToLower(s))
	out, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err != nil {
		return s
	}
	return out
}

func (s *Service) size(ctx context.Context, projectID, agentID, topic string) int {
	list, _ := s.store.Memories().List(ctx, projectID, agentID)
	n := 0
	for _, m := range list {
		if m.Topic == topic {
			n += utf8.RuneCountInString(m.Text)
		}
	}
	return n
}

// Compact rewrites the core notes shorter, keeping what they were.
func (s *Service) Compact(ctx context.Context, projectID, agentID, reason string) error {
	return s.CompactTopic(ctx, projectID, agentID, "", reason)
}

// CompactTopic rewrites one topic's notes ("" = core) shorter; the revision
// keeps all the notes as they were, so a restore puts every topic back.
func (s *Service) CompactTopic(ctx context.Context, projectID, agentID, topic, reason string) error {
	if s.compact == nil {
		return errors.New("chưa rút gọn được: không có model")
	}
	list, err := s.store.Memories().List(ctx, projectID, agentID)
	if err != nil {
		return err
	}
	var group []storage.Memory
	summary := ""
	for _, m := range list {
		if m.Topic == topic {
			group = append(group, m)
			summary = cmp.Or(m.Summary, summary) // the newest
		}
	}
	if len(group) == 0 {
		return nil
	}
	texts, err := s.compact(ctx, projectID, agentID, group)
	if err != nil {
		return err
	}
	var items []storage.Memory
	for i, t := range texts {
		if t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "- ")); t != "" { // in the place of the old ones
			items = append(items, storage.Memory{Text: t, Source: "compact", Topic: topic, CreatedAt: group[0].CreatedAt.Add(time.Duration(i) * time.Microsecond)})
		}
	}
	if len(items) == 0 {
		return errors.New("rút gọn ra rỗng: giữ nguyên")
	}
	items[len(items)-1].Summary = summary // the topic keeps its index line
	snapshot := map[string]time.Time{}
	for _, m := range group {
		snapshot[m.ID] = m.UpdatedAt
	}
	return s.store.InTx(ctx, func(tx storage.Store) error {
		if _, err := tx.Memories().SaveRevision(ctx, storage.MemoryRevision{ProjectID: projectID, AgentID: agentID, Items: list, Reason: reason}); err != nil {
			return err
		}
		now, err := tx.Memories().List(ctx, projectID, agentID)
		if err != nil {
			return err
		}
		for _, m := range now {
			snapAt, inGroup := snapshot[m.ID]
			// other topics, what was written while the model compacted, and
			// notes edited mid-compaction (kept as-is, not overwritten by a
			// summary generated from their pre-edit text): kept
			if !inGroup || m.UpdatedAt.After(snapAt) {
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
	return s.store.InTx(ctx, func(tx storage.Store) error {
		now, err := tx.Memories().List(ctx, rev.ProjectID, rev.AgentID)
		if err != nil {
			return err
		}
		if _, err := tx.Memories().SaveRevision(ctx, storage.MemoryRevision{ProjectID: rev.ProjectID, AgentID: rev.AgentID, Items: now, Reason: "trước khi khôi phục (" + by + ")"}); err != nil {
			return err
		}
		items := make([]storage.Memory, len(rev.Items))
		for i, m := range rev.Items {
			items[i] = storage.Memory{Text: m.Text, Source: m.Source, Topic: m.Topic, Summary: m.Summary, CreatedBy: m.CreatedBy, CreatedAt: m.CreatedAt}
		}
		return tx.Memories().Replace(ctx, rev.ProjectID, rev.AgentID, items)
	})
}

// Block is the notes as a conversation's system prompt gets them ("" = none).
func (s *Service) Block(ctx context.Context, projectID, agentID string) string {
	return Block(ctx, s.store, projectID, agentID)
}

// Block formats an agent's notes for its system prompt: the core notes and
// the index of topics (their notes are read with recall).
func Block(ctx context.Context, store storage.Store, projectID, agentID string) string {
	list, err := store.Memories().List(ctx, projectID, agentID)
	if err != nil {
		return ""
	}
	return Render(list, CoreLimit, IndexLimit)
}

// Render formats notes (oldest first): the core ones, the newest kept within
// coreCap characters, then one index line per topic, the most recently
// written first, within indexCap.
func Render(list []storage.Memory, coreCap, indexCap int) string {
	var core []string
	for _, m := range list {
		if m.Topic == "" {
			core = append(core, m.Text)
		}
	}
	var notes []string
	n := 0
	for i := len(core) - 1; i >= 0; i-- { // the newest first, within the cap
		n += utf8.RuneCountInString(core[i])
		if n > coreCap && len(notes) > 0 {
			break
		}
		notes = append([]string{core[i]}, notes...)
	}
	lines := Index(list)
	var index []string
	n = 0
	for _, l := range lines {
		n += utf8.RuneCountInString(l) + 1
		if n > indexCap && len(index) > 0 {
			break
		}
		index = append(index, l)
	}
	if len(notes) == 0 && len(index) == 0 {
		return ""
	}
	return "\n\n" + prompts.Render("memory/notes", map[string]any{"Notes": notes, "Dropped": len(core) - len(notes),
		"Index": index, "More": len(lines) - len(index)})
}

// Topic is one topic of an agent's notes.
type Topic struct {
	Name, Summary string
	Notes, Size   int
	last          int // where its newest note is in the list
}

// Topics groups the notes by topic, the most recently written first; a
// topic's summary is its newest one, else the start of its newest note.
func Topics(list []storage.Memory) []Topic {
	at := map[string]int{}
	var out []Topic
	newest := map[string]string{}
	for i, m := range list {
		if m.Topic == "" {
			continue
		}
		k, ok := at[m.Topic]
		if !ok {
			k = len(out)
			at[m.Topic] = k
			out = append(out, Topic{Name: m.Topic})
		}
		t := &out[k]
		t.Notes++
		t.Size += utf8.RuneCountInString(m.Text)
		t.last = i
		t.Summary = cmp.Or(m.Summary, t.Summary)
		newest[m.Topic] = m.Text
	}
	for i := range out {
		if out[i].Summary == "" {
			out[i].Summary = short(newest[out[i].Name])
		}
	}
	slices.SortStableFunc(out, func(a, b Topic) int { return b.last - a.last })
	return out
}

func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > 100 {
		s = string([]rune(s)[:99]) + "…"
	}
	return s
}

// Index is one line per topic: "- topic: summary".
func Index(list []storage.Memory) []string {
	var out []string
	for _, t := range Topics(list) {
		out = append(out, "- "+t.Name+": "+t.Summary)
	}
	return out
}

// Recall is what the recall tool reads: the notes of topic, or those with
// every word of query (accents ignored), or both; neither lists the topics.
func Recall(ctx context.Context, store storage.Store, projectID, agentID, topic, query string) (string, error) {
	list, err := store.Memories().List(ctx, projectID, agentID)
	if err != nil {
		return "", err
	}
	topic, words := Slug(topic), strings.Fields(fold(query))
	core := topic == "core" // the notes with no topic, as the prompt has them
	if topic == "" && len(words) == 0 {
		lines := Index(list)
		if len(lines) == 0 {
			return "No topics in your notes yet.", nil
		}
		return fmt.Sprintf("%d topics (recall one by its name):\n%s", len(lines), strings.Join(lines, "\n")), nil
	}
	var hits []storage.Memory
	for _, m := range list {
		if core && m.Topic != "" || !core && topic != "" && m.Topic != topic {
			continue
		}
		hay := fold(m.Topic + " " + m.Summary + " " + m.Text)
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) }) {
			hits = append(hits, m)
		}
	}
	if len(hits) == 0 {
		var names []string
		for _, t := range Topics(list) {
			names = append(names, t.Name)
		}
		return fmt.Sprintf("No notes match. Topics: %s.", cmp.Or(strings.Join(names, ", "), "(none)")), nil
	}
	if len(hits) > 30 { // the newest
		hits = hits[len(hits)-30:]
	}
	var b strings.Builder
	if topic != "" && len(words) == 0 {
		fmt.Fprintf(&b, "Notes on %s (id first: remember with replaces, or forget, keeps them right):\n", topic)
		for _, m := range hits {
			fmt.Fprintf(&b, "- [%s] %s\n", m.ID, m.Text)
		}
		return b.String(), nil
	}
	fmt.Fprintf(&b, "%d notes match:\n", len(hits))
	for _, m := range hits {
		fmt.Fprintf(&b, "- [%s] [%s] %s\n", m.ID, cmp.Or(m.Topic, "core"), m.Text)
	}
	return b.String(), nil
}

func autoKey(projectID string) string { return "memory_auto/" + projectID }

// Auto: an agent's notes are kept without a person approving each (on
// unless the project turned it off); the guards below still hold.
func (s *Service) Auto(ctx context.Context, projectID string) bool {
	on := true
	_, _ = s.store.Settings().Get(ctx, autoKey(projectID), &on)
	return on
}

const (
	// MinNote: shorter than this, a note says too little to save time later.
	MinNote = 20
	// AutoPerTurn: notes one turn keeps on its own; more wait for a person.
	AutoPerTurn = 2
)

// Duplicate is the agent's note that already says text (same words, or one
// holding the other), so the same thing is not noted twice.
func (s *Service) Duplicate(ctx context.Context, projectID, agentID, text string) (storage.Memory, bool) {
	list, _ := s.store.Memories().List(ctx, projectID, agentID)
	w := words(text)
	for _, m := range list {
		if similar(w, words(m.Text)) {
			return m, true
		}
	}
	return storage.Memory{}, false
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.FieldsFunc(fold(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out[f] = true
	}
	return out
}

// similar: the smaller set nearly inside the larger (85% of its words).
func similar(a, b map[string]bool) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	if len(a) == 0 {
		return false
	}
	n := 0
	for w := range a {
		if b[w] {
			n++
		}
	}
	return n*100 >= len(a)*85
}

func (s *Service) SetAuto(ctx context.Context, projectID string, on bool) error {
	return s.store.Settings().Set(ctx, autoKey(projectID), on)
}
