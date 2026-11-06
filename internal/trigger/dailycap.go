package trigger

import (
	"context"
	"fmt"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// DailyCapCode is the disabled code office once gave an automation that
// reached its daily cost cap; a cap now only skips the day's runs (ADR-263).
const DailyCapCode = "daily_cost"

// DailyCapNote says a run did not start for its automation's daily cap.
func DailyCapNote(a storage.Automation, spent float64) string {
	return fmt.Sprintf("chạm trần chi phí trong ngày ($%.2f/$%.2f, gồm các bước quy trình nó mở); chạy lại khi sang ngày mới", spent, a.Limits.DailyCostUSD)
}

// reopenCapped turns back on the automations office turned off at their
// daily cap before ADR-263, once their day turned and they are under it.
func (r *Runner) reopenCapped(ctx context.Context, now time.Time) {
	list, err := r.store.Automations().DisabledWith(ctx, DailyCapCode)
	if err != nil {
		return
	}
	for _, a := range list {
		if _, over := a.OverDailyCap(ctx, r.store.Jobs(), now); over {
			continue
		}
		var next *time.Time
		if a.Source == "schedule" {
			if n, err := NextRun(a.Config, now); err == nil {
				next = &n
			}
		}
		_ = r.store.Automations().Reenable(ctx, a.ID, next)
	}
}
