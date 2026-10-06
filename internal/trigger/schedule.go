// Package trigger runs a project's automations: schedules and webhooks that
// start a chat turn or a task as a job (ADR-040).
package trigger

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata" // a missing zone must not fall back to UTC

	"github.com/robfig/cron/v3"

	"github.com/tung1998/agent-office/internal/storage"
)

func location(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	return time.LoadLocation(tz)
}

// Validate checks a schedule before it is saved.
func Validate(c storage.AutomationConfig) error {
	if _, err := location(c.Timezone); err != nil {
		return fmt.Errorf("múi giờ %q không có", c.Timezone)
	}
	switch {
	case c.Cron != "":
		if _, err := cron.ParseStandard(c.Cron); err != nil {
			return fmt.Errorf("cron không hợp lệ: %v", err)
		}
	case c.EveryMinutes >= 1:
	default:
		return errors.New("cần cron hoặc số phút lặp lại (từ 1)")
	}
	return nil
}

// Next is the first run after `after` (UTC).
func Next(c storage.AutomationConfig, after time.Time) (time.Time, error) {
	if err := Validate(c); err != nil {
		return time.Time{}, err
	}
	if c.Cron == "" {
		return after.Add(time.Duration(c.EveryMinutes) * time.Minute).UTC(), nil
	}
	loc, _ := location(c.Timezone)
	s, _ := cron.ParseStandard(c.Cron)
	return s.Next(after.In(loc)).UTC(), nil
}

// Upcoming lists the next n runs from `from`.
func Upcoming(c storage.AutomationConfig, from time.Time, n int) []time.Time {
	out := []time.Time{}
	t := from
	for i := 0; i < n; i++ {
		next, err := Next(c, t)
		if err != nil || next.IsZero() {
			break
		}
		out = append(out, next)
		t = next
	}
	return out
}
