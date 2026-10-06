// Package events tells the dashboard what changed (ADR-072): every write to
// a table people look at (messages, jobs, automations, bots…) whoever made
// it (a bot's message, an agent, a run in the background), gathered for a
// moment so a burst is one notice.
package events

import (
	"sort"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Followed are the tables a dashboard page shows.
var Followed = map[string]bool{
	"conversations": true, "messages": true, "conversation_agents": true, "patches": true, "actions": true,
	"jobs": true, "automations": true, "channels": true, "repos": true, "org_models": true, "agents": true,
	"processes": true, "monitors": true, "agent_memories": true, "providers": true, "users": true,
	"runs": true, "org_revisions": true, "audit_log": true, "monitor_events": true, "settings": true, "conversation_reads": true, "burn_sessions": true, "burn_items": true,
	"mcp_servers": true,
}

// Event is one notice: its name (change, stats, machine, message…) and data.
type Event struct {
	Name string
	Data any
}

// Viewer is who an open page belongs to: some events are only theirs.
type Viewer struct {
	UserID string
	Email  string
	Admin  bool
}

// Sub is one open page following the events.
type Sub struct {
	ID     int
	C      <-chan Event
	viewer Viewer
	ch     chan Event
	topics map[string]bool // what it asked for besides the rest (machine…)
}

// Bus fans notices out to the dashboard's open pages: the tables written
// (gathered for a moment), and data the server pushes as it changes.
type Bus struct {
	wait    time.Duration
	mu      sync.Mutex
	subs    map[int]*Sub
	next    int
	pending map[string]bool
	timer   *time.Timer
	onSub   func(*Sub) // a page started following (its first figures)
	onTopic func(*Sub, string)
	onFlush func(tables []string) // after the tables' notice (what is worked out from them)
	onChat  func(storage.Change)  // a chat written (pushed with its data)
	work    chan func()           // what follows a write, in order, off the writer's path
}

func New(wait time.Duration) *Bus {
	b := &Bus{wait: wait, subs: map[int]*Sub{}, pending: map[string]bool{}, work: make(chan func(), 1024)}
	go func() {
		for fn := range b.work {
			fn()
		}
	}()
	return b
}

// OnFlush tells fn the tables of each notice; OnChat each chat written.
// Both run in order, off the writer's path, and only while someone looks.
func (b *Bus) OnFlush(fn func(tables []string)) { b.mu.Lock(); b.onFlush = fn; b.mu.Unlock() }
func (b *Bus) OnChat(fn func(storage.Change))   { b.mu.Lock(); b.onChat = fn; b.mu.Unlock() }

// Chat is the store's OnChat.
func (b *Bus) Chat(c storage.Change) {
	b.mu.Lock()
	fn, looking := b.onChat, len(b.subs) > 0
	b.mu.Unlock()
	if fn == nil || !looking {
		return
	}
	select {
	case b.work <- func() { fn(c) }:
	default: // a flood: the pages' table notice still comes
	}
}

// Viewers are the people with a page open, once each.
func (b *Bus) Viewers() []Viewer {
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := map[string]bool{}
	var out []Viewer
	for _, s := range b.subs {
		if !seen[s.viewer.UserID] {
			seen[s.viewer.UserID] = true
			out = append(out, s.viewer)
		}
	}
	return out
}

// OnSubscribe tells fn of each new page, and of a topic it turns on.
func (b *Bus) OnSubscribe(sub func(*Sub), topic func(*Sub, string)) {
	b.mu.Lock()
	b.onSub, b.onTopic = sub, topic
	b.mu.Unlock()
}

// Wrote is the store's OnWrite: a table changed.
func (b *Bus) Wrote(table string) {
	if !Followed[table] {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) == 0 {
		return // nobody looking
	}
	b.pending[table] = true
	if b.timer == nil {
		b.timer = time.AfterFunc(b.wait, b.flush)
	}
}

func (b *Bus) flush() {
	b.mu.Lock()
	b.timer = nil
	tables := make([]string, 0, len(b.pending))
	for t := range b.pending {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	b.pending = map[string]bool{}
	fn := b.onFlush
	b.mu.Unlock()
	b.Send(Event{Name: "change", Data: map[string]any{"tables": tables}}, nil)
	if fn != nil {
		select {
		case b.work <- func() { fn(tables) }:
		default:
		}
	}
}

// Send gives ev to the pages match takes (nil: every page).
func (b *Bus) Send(ev Event, match func(Viewer, map[string]bool) bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		if match != nil && !match(s.viewer, s.topics) {
			continue
		}
		select {
		case s.ch <- ev:
		default: // a slow page catches up on a later one (or reconnects)
		}
	}
}

// SendTo gives ev to one page.
func (b *Bus) SendTo(s *Sub, ev Event) {
	select {
	case s.ch <- ev:
	default:
	}
}

// Count is how many pages match takes.
func (b *Bus) Count(match func(Viewer, map[string]bool) bool) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, s := range b.subs {
		if match == nil || match(s.viewer, s.topics) {
			n++
		}
	}
	return n
}

// Subscribe follows the events for a page of v until stop.
func (b *Bus) Subscribe(v Viewer) (*Sub, func()) {
	b.mu.Lock()
	id := b.next
	b.next++
	ch := make(chan Event, 32)
	s := &Sub{ID: id, C: ch, viewer: v, ch: ch, topics: map[string]bool{}}
	b.subs[id] = s
	on := b.onSub
	b.mu.Unlock()
	if on != nil {
		on(s)
	}
	return s, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
	}
}

// SetTopic turns a topic on or off for page id, if it is userID's.
func (b *Bus) SetTopic(id int, userID, topic string, on bool) bool {
	b.mu.Lock()
	s, ok := b.subs[id]
	if !ok || s.viewer.UserID != userID {
		b.mu.Unlock()
		return false
	}
	was := s.topics[topic]
	if on {
		s.topics[topic] = true
	} else {
		delete(s.topics, topic)
	}
	cb := b.onTopic
	b.mu.Unlock()
	if on && !was && cb != nil {
		cb(s, topic)
	}
	return true
}

// Viewer is whose page s is.
func (s *Sub) Viewer() Viewer { return s.viewer }
