package burn

import (
	"context"
	"log/slog"
	"strings"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
)

// OnBoard keeps a task of the work board in step with the hand-off it is
// (ADR-210): an agent given it with delegate (task_id) has it in progress
// (handed: the Burn leaves it alone), its answer makes it done with that
// answer as its summary, a hand-off that ends without one makes it failed.
// A task of another project's board, or one already finished, is left as it is.
func (s *Service) OnBoard(ctx context.Context, ev chat.BoardEvent) {
	it, err := s.store.Burn().Item(ctx, ev.Task)
	if err != nil {
		return
	}
	b, err := s.store.Burn().SessionByID(ctx, it.SessionID)
	if err != nil || b.ProjectID != ev.ProjectID {
		return
	}
	from := it.Status
	switch {
	case !ev.Done:
		if !openStatus[from] || from == "doing" { // the Burn is on it
			return
		}
		it.Status, it.Summary = StatusHanded, "→ "+ev.Agent
	case from != StatusHanded:
		return // moved on meanwhile (skipped by the person, taken by the Burn)
	case ev.Failed || strings.TrimSpace(ev.Result) == "":
		it.Status, it.Summary = "failed", ev.Agent+": "+clip(strings.TrimSpace(ev.Result), 1500)
	default:
		it.Status, it.Summary = "done", ev.Agent+": "+clip(strings.TrimSpace(ev.Result), 3000)
	}
	if err := s.store.Burn().UpdateItemFrom(ctx, it, from); err != nil {
		slog.Warn("burn: board task", "item", it.ID, "err", err)
		return
	}
	s.wake(b.ProjectID)
}

// Next is the oldest quest of the work board waiting for agent (found or
// queued, its owner, or no owner for the default agent): what it takes on
// once its turn ends (ADR-221). None while the Burn runs: it takes them itself.
func (s *Service) Next(ctx context.Context, projectID string, agent storage.Agent, dflt bool) (chat.NextWork, bool) {
	b, err := s.store.Burn().Session(ctx, projectID)
	if err != nil || b.Active() {
		return chat.NextWork{}, false
	}
	items, err := s.store.Burn().Items(ctx, b.ID)
	if err != nil {
		return chat.NextWork{}, false
	}
	var next chat.NextWork
	var first storage.BurnItem
	var sig strings.Builder
	for _, it := range items {
		if !openStatus[it.Status] {
			continue
		}
		sig.WriteString(it.ID + ":" + it.Status + ";")
		mine := it.Owner == agent.Key || (it.Owner == "" && dflt)
		if it.Kind != KindQuest || !mine || (it.Status != "found" && it.Status != "queued") {
			continue
		}
		if next.Open++; next.Open == 1 || it.CreatedAt.Before(first.CreatedAt) {
			first = it
		}
	}
	if next.Open == 0 {
		return chat.NextWork{}, false
	}
	next.Task, next.Title, next.Detail, next.Sig = first.ID, first.Title, clip(first.Detail, 3000), sig.String()
	return next, true
}

// Open tells whether a task of the project's board is still to do (a
// hand-off waiting for its agent starts only then; ADR-221).
func (s *Service) Open(ctx context.Context, projectID, task string) bool {
	it, err := s.store.Burn().Item(ctx, task)
	if err != nil {
		return false
	}
	b, err := s.store.Burn().SessionByID(ctx, it.SessionID)
	return err == nil && b.ProjectID == projectID && openStatus[it.Status]
}
