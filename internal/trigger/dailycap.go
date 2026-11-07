package trigger

import (
	"context"
	"fmt"
	"log/slog"
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

// DailyCapUnknownNote says a run did not start because today's spend could
// not be read, not because the cap is actually known to be hit: unlike
// DailyCapNote it must not promise the next run (possibly moments later)
// will fail the same way.
func DailyCapUnknownNote(a storage.Automation) string {
	return "không đọc được chi phí trong ngày (lỗi lưu trữ tạm thời); tạm chặn lượt này, thử lại ở lượt sau"
}

// reopenCapped turns back on the automations office turned off at their
// daily cap before ADR-263, once their day turned and they are under it.
func (r *Runner) reopenCapped(ctx context.Context, now time.Time) {
	list, err := r.store.Automations().DisabledWith(ctx, DailyCapCode)
	if err != nil {
		return
	}
	for _, a := range list {
		_, over, err := a.OverDailyCap(ctx, r.store.Jobs(), now)
		if err != nil {
			slog.Warn("trigger: reopenCapped could not read today's spend, leaving disabled", "automation", a.ID, "err", err)
			continue
		}
		if over {
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
