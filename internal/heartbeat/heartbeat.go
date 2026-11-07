package heartbeat

import (
	"context"
	"log/slog"
	"runtime/debug"
	"slices"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/features"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
)

const (
	// Every is how often each project is checked.
	Every = 5 * time.Minute
	// cooldown: after a self-check turn, the project is left to it this long
	cooldown = time.Hour
	// the same sign is raised again only after this long
	remember = 6 * time.Hour
)

// Chatter starts a chat with an agent and waits for its answer (office's
// automation executor).
type Chatter interface {
	RunChat(ctx context.Context, projectID, agentID, conversationID, prompt, editMode string) (conv, reply string, err error)
}

// Service checks every project on a timer.
type Service struct {
	store storage.Store
	chat  Chatter
	now   func() time.Time
	// opened is called with the chat a self-check started (tests)
	opened func(projectID, conv string)
	// restored is called after a failed turn's state rollback is saved (tests)
	restored func(projectID string)
}

func New(store storage.Store, chat Chatter) *Service {
	return &Service{store: store, chat: chat, now: time.Now}
}

// state is what a project's self-check remembers between rounds.
type state struct {
	Last time.Time            `json:"last"` // the last self-check turn
	Seen map[string]time.Time `json:"seen"` // signal key → when raised
}

func stateKey(projectID string) string { return "heartbeat:" + projectID }

// Run checks every project each Every until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(Every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Round(ctx)
		}
	}
}

// Round checks each project once; a project with signs gets a self-check turn.
func (s *Service) Round(ctx context.Context) {
	projects, err := s.store.Repos().List(ctx)
	if err != nil {
		return
	}
	for _, p := range projects {
		s.Check(ctx, p)
	}
}

// Check looks at one project and starts a self-check turn when it needs one;
// it returns the signs raised (none: nothing new, or in cooldown).
func (s *Service) Check(ctx context.Context, p storage.Repo) []Signal {
	now := s.now().UTC()
	var st state
	if _, err := s.store.Settings().Get(ctx, stateKey(p.ID), &st); err != nil {
		return nil
	}
	if now.Sub(st.Last) < cooldown || !features.Has(features.For(ctx, s.store, p.ID), features.SelfCheck) {
		return nil
	}
	f, err := s.store.Harness().Facts(ctx, p.ID, now.Add(-Window))
	if err != nil || len(f.Jobs) == 0 { // no agent worked: nothing can be stuck
		return nil
	}
	var fresh []Signal
	for _, sig := range Detect(f, now) {
		if at, ok := st.Seen[sig.Key]; !ok || now.Sub(at) >= remember {
			fresh = append(fresh, sig)
		}
	}
	// cards waiting alone are the person's to decide, and they see them:
	// a turn would only tell them so again, or add cards (29/10)
	if !slices.ContainsFunc(fresh, func(sig Signal) bool { return sig.Kind != "stalled_cards" }) {
		return nil
	}
	lead, ok := s.coordinator(ctx, p)
	if !ok {
		return nil
	}
	// a deep copy: the sign is marked seen and the project put in cooldown
	// right away (RunChat waits for the whole turn, often well past the 5min
	// Every, so the next round must not open a second turn for it too), but
	// if open() never actually reaches the person we restore this to undo it.
	prev := state{Last: st.Last, Seen: make(map[string]time.Time, len(st.Seen))}
	for k, v := range st.Seen {
		prev.Seen[k] = v
	}
	if st.Seen == nil {
		st.Seen = map[string]time.Time{}
	}
	for k, at := range st.Seen {
		if now.Sub(at) >= remember {
			delete(st.Seen, k)
		}
	}
	for _, sig := range fresh {
		st.Seen[sig.Key] = now
	}
	st.Last = now
	if err := s.store.Settings().Set(ctx, stateKey(p.ID), st); err != nil {
		return nil // not saved: it would raise them again each round
	}
	prompt := prompts.Render("heartbeat/stuck", map[string]any{"Project": p.ID, "Signals": fresh, "Admin": lead.Permissions.FullAccess})
	go s.open(p.ID, lead, prompt, prev)
	return fresh
}

// open starts the self-check turn. If RunChat fails — the turn never
// actually ran — it restores the state to what it was before Check marked
// the signals seen, so the next Check tries again instead of silently
// sitting quiet for the remember/cooldown window (mirrors
// limitalert.Alerter.Check's notify-then-mark pattern).
func (s *Service) open(projectID string, lead storage.Agent, prompt string, prev state) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("heartbeat: panic", "project", projectID, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	ctx := actor.With(context.Background(), "heartbeat")
	conv, _, err := s.chat.RunChat(ctx, projectID, lead.ID, "", prompt, "")
	if err != nil {
		slog.Warn("heartbeat: self-check turn", "project", projectID, "agent", lead.Name, "err", err)
		if err := s.store.Settings().Set(ctx, stateKey(projectID), prev); err != nil {
			slog.Warn("heartbeat: restore state", "project", projectID, "err", err)
		}
		if s.restored != nil {
			s.restored(projectID)
		}
		return
	}
	if s.opened != nil {
		s.opened(projectID, conv)
	}
}

// coordinator is who looks into it: an agent with administrator permission
// can decide the cards, so the project's default agent if it has it, else
// the first that does, else the default agent.
func (s *Service) coordinator(ctx context.Context, p storage.Repo) (storage.Agent, bool) {
	agents, err := s.store.Agents().List(ctx, p.ID)
	if err != nil {
		return storage.Agent{}, false
	}
	def, ok := storage.DefaultAgent(p, agents)
	if !ok {
		return storage.Agent{}, false
	}
	if def.Permissions.FullAccess {
		return def, true
	}
	for _, a := range agents {
		if !a.Disabled && a.Permissions.FullAccess {
			return a, true
		}
	}
	return def, true
}
