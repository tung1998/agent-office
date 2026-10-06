package trigger_test

import (
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

func TestNextCronInTimezone(t *testing.T) {
	cfg := storage.AutomationConfig{Cron: "0 8 * * 1-5", Timezone: "Asia/Ho_Chi_Minh"}
	// Friday 2026-10-02 09:00 at +07 → next is Monday 2026-10-05 08:00 +07 = 01:00 UTC
	after := time.Date(2026, 10, 2, 2, 0, 0, 0, time.UTC)
	got, err := trigger.Next(cfg, after)
	if err != nil || !got.Equal(time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %v %v", got, err)
	}
}

func TestNextEveryAndValidate(t *testing.T) {
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got, _ := trigger.Next(storage.AutomationConfig{EveryMinutes: 5}, after); !got.Equal(after.Add(5 * time.Minute)) {
		t.Fatalf("every = %v", got)
	}
	for _, bad := range []storage.AutomationConfig{{}, {EveryMinutes: -1}, {Cron: "61 * * * *"}, {Cron: "0 8 * * *", Timezone: "Mars/Base"}} {
		if trigger.Validate(bad) == nil {
			t.Fatalf("valid: %+v", bad)
		}
	}
	// DST: 02:30 daily in New York skips the missing hour on 2026-03-08
	ny := storage.AutomationConfig{Cron: "30 2 * * *", Timezone: "America/New_York"}
	got, _ := trigger.Next(ny, time.Date(2026, 3, 7, 12, 0, 0, 0, time.UTC))
	if got.Before(time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("dst next = %v", got)
	}
	if n := trigger.Upcoming(storage.AutomationConfig{EveryMinutes: 10}, after, 5); len(n) != 5 || !n[4].Equal(after.Add(50*time.Minute)) {
		t.Fatalf("upcoming = %v", n)
	}
}
