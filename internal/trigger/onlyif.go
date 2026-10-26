package trigger

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// HasWork reads a schedule's condition (ADR-194): it runs when a record of
// the collection matches Where, or with Changed when one changed since its
// last run, or when its project's work board has what Board asks (ADR-196).
// No condition, or one that cannot be read (a table or field gone): it runs,
// so a broken condition shows instead of going quiet. In Where, "$today" and
// "$now" stand for the date and time of the tick.
func HasWork(ctx context.Context, st storage.Store, a storage.Automation, now time.Time) bool {
	c := a.Config.OnlyIf
	if c == nil || (len(c.Where) == 0 && !c.Changed && c.Board == "") {
		return true
	}
	if c.Board != "" && boardHasWork(ctx, st, a, c.Board) {
		return true
	}
	if c.Collection == "" || (len(c.Where) == 0 && !c.Changed) {
		return false
	}
	col, err := st.Collections().Get(ctx, a.ProjectID, c.Collection)
	if err != nil {
		return true
	}
	if len(c.Where) > 0 {
		where := make([]storage.Cond, 0, len(c.Where))
		for _, w := range c.Where {
			if !slices.ContainsFunc(col.Fields, func(f storage.Field) bool { return f.Name == w.Field }) {
				return true
			}
			where = append(where, storage.Cond{Field: w.Field, Op: w.Op, Value: dateWord(w.Value, now, a.Config.Timezone)})
		}
		_, n, err := st.Collections().Query(ctx, storage.RecordQuery{CollectionID: col.ID, Where: where, Limit: 1})
		if err != nil || n > 0 {
			return true
		}
	}
	if c.Changed {
		recs, _, err := st.Collections().Query(ctx, storage.RecordQuery{CollectionID: col.ID, Sort: "updated_at", Desc: true, Limit: 1})
		if err != nil || len(recs) > 0 && (a.LastRunAt == nil || recs[0].UpdatedAt.After(*a.LastRunAt)) {
			return true
		}
	}
	return false
}

// boardHasWork reads the project's work board (its Burn's pieces): open =
// work no one is doing (the Burn off with pieces left, or one waiting for
// review); changed = a piece added, finished or waiting for review since the
// schedule's last run (a piece being worked on writes its cost and chats all
// along: that is no news for whoever follows it up); any = either. A board
// it cannot read lets it run.
func boardHasWork(ctx context.Context, st storage.Store, a storage.Automation, board string) bool {
	b, err := st.Burn().Session(ctx, a.ProjectID)
	if errors.Is(err, storage.ErrNotFound) {
		return false
	}
	if err != nil {
		return true
	}
	items, err := st.Burn().Items(ctx, b.ID)
	if err != nil {
		return true
	}
	open, changed := board == storage.BoardOpen || board == storage.BoardAny, board == storage.BoardChanged || board == storage.BoardAny
	for _, it := range items {
		switch {
		case changed && (a.LastRunAt == nil || it.CreatedAt.After(*a.LastRunAt)):
			return true
		case changed && it.UpdatedAt.After(*a.LastRunAt) && settled(it.Status):
			return true
		case open && it.Status == "review":
			return true
		case open && !b.Active() && (it.Status == "found" || it.Status == "queued" || it.Status == "paused"):
			return true
		}
	}
	return false
}

// settled: a piece's state its follower acts on (accept, return, hand the
// next one out).
func settled(status string) bool {
	return status == "review" || status == "done" || status == "failed" || status == "skipped"
}

func dateWord(v any, now time.Time, tz string) any {
	if loc, err := time.LoadLocation(tz); err == nil && tz != "" {
		now = now.In(loc)
	}
	switch v {
	case "$today":
		return now.Format("2006-01-02")
	case "$now":
		return now.UTC().Format(time.RFC3339)
	}
	return v
}
