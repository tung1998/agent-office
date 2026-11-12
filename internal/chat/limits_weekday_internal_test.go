package chat

import (
	"errors"
	"testing"
	"time"
)

// A weekly limit says the day of the week it resets on ("resets Mon 9am",
// "resets Thursday 3:30pm (Asia/Saigon)"): the connection is held until
// then, not for the 5 minutes of a report without a time (ADR-269). Codex's
// dated form ("try again at Nov 13th, 2026 10:18 AM") is read too.
func TestLimitResetsOnAWeekday(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("ICT", 7*3600)
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, loc) // a Sunday
	cases := []struct {
		msg  string
		want time.Time
	}{
		{"claude: You've hit your weekly limit · resets Mon 9am", time.Date(2026, 11, 9, 9, 0, 0, 0, loc)},
		{"claude: You've hit your weekly limit · resets Thursday 3:30pm (Asia/Saigon)", time.Date(2026, 11, 12, 15, 30, 0, 0, loc)},
		{"claude: You've hit your weekly limit · resets Sun 9am", time.Date(2026, 11, 15, 9, 0, 0, 0, loc)}, // today's hour has passed: next week
		{"claude: You've hit your weekly limit · resets Sun, 11am", time.Date(2026, 11, 8, 11, 0, 0, 0, loc)},
		{"codex: You’ve hit your usage limit. Upgrade to Pro, or try again at Nov 13th, 2026 10:18 AM.", time.Date(2026, 11, 13, 10, 18, 0, 0, loc)},
	}
	for _, c := range cases {
		l := limitsFromError(nil, errors.New(c.msg), now)
		if l == nil || l.Status != "rejected" || !l.Windows["quota"].ResetsAt.Equal(c.want) {
			t.Fatalf("%q: limits = %+v, want reset %v", c.msg, l, c.want)
		}
	}
}
