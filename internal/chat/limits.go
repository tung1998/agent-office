package chat

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/usage"

	"github.com/tung1998/agent-office/internal/storage"
)

// LimitWindow is one usage window of a subscription (Claude Code reports
// them: five_hour, seven_day, seven_day_<model>…).
type LimitWindow struct {
	Utilization float64   `json:"utilization"` // 0..1
	ResetsAt    time.Time `json:"resets_at"`
}

// Limits is the latest usage a provider reported.
type Limits struct {
	Status    string                 `json:"status"` // allowed | allowed_warning | rejected
	Windows   map[string]LimitWindow `json:"windows"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// ContextUse is how full the model's context was after a turn.
type ContextUse struct {
	Tokens int `json:"tokens"`
	Window int `json:"window"`
}

// LimitsKey is where a provider's latest Limits are kept (settings).
func LimitsKey(providerID string) string { return "provider_limits:" + providerID }

// keepLimits stores the usage windows a run reported for its provider.
func (e *Engine) keepLimits(p storage.Provider, l *Limits) {
	if l == nil || (l.Status == "" && len(l.Windows) == 0) {
		return
	}
	if len(l.Windows) == 0 && l.Status != "rejected" {
		// Status-only report with no window detail: nothing new to persist,
		// and saving it would wipe out whatever window data we already had.
		return
	}
	_ = e.store.Settings().Set(context.Background(), LimitsKey(p.ID), l)
	if fn := e.onLimits; fn != nil { // the limit alerts
		safeGo("onLimits", func() { fn(p, *l) })
	}
}

// resetIn is how an out-of-quota error says when it resets ("Resets in
// 9m51s", "try again in 30s").
var resetIn = regexp.MustCompile(`(?i)(?:resets?|try again|retry) in ((?:\d+h)?(?:\d+m)?(?:\d+(?:\.\d+)?s)?)`)

// limitsFromError reads a run's out-of-quota error as a "rejected" report
// for a connection that reports no usage itself (agy, an API's 429): until
// the reset the error names, else for RejectedCooldown. nil: not that error,
// or the connection already reported it.
func limitsFromError(reported *Limits, err error, now time.Time) *Limits {
	var be *usage.BudgetError
	if err == nil || !OutOfTokens(err) || errors.As(err, &be) || (reported != nil && reported.Status == "rejected") {
		return nil
	}
	l := &Limits{Status: "rejected", UpdatedAt: now}
	if m := resetIn.FindStringSubmatch(err.Error()); m != nil && m[1] != "" {
		if d, perr := time.ParseDuration(strings.ToLower(m[1])); perr == nil && d > 0 {
			l.Windows = map[string]LimitWindow{"quota": {Utilization: 1, ResetsAt: now.Add(d)}}
		}
	} else if at, ok := resetAtClock(err.Error(), now); ok {
		l.Windows = map[string]LimitWindow{"quota": {Utilization: 1, ResetsAt: at}}
	} else if at, ok := resetsClock(err.Error(), now); ok {
		l.Windows = map[string]LimitWindow{"quota": {Utilization: 1, ResetsAt: at}}
	}
	return l
}

// resetAt is how Codex says when its usage limit resets: a clock time on the
// machine ("try again at 9:18 PM"), with the date first when it is not today
// ("try again at Nov 6th, 2026 9:18 PM").
var resetAt = regexp.MustCompile(`(?i)try again at (?:([a-z]{3})[a-z]* (\d{1,2})(?:st|nd|rd|th)?,? (\d{4}),? )?(\d{1,2}):(\d{2})\s*([ap]\.?m\.?)?`)

// resetAtClock reads resetAt in now's time zone: a clock time already past
// today is tomorrow's.
func resetAtClock(msg string, now time.Time) (time.Time, bool) {
	m := resetAt.FindStringSubmatch(msg)
	if m == nil {
		return time.Time{}, false
	}
	h, _ := strconv.Atoi(m[4])
	mi, _ := strconv.Atoi(m[5])
	if ampm := strings.ToLower(strings.ReplaceAll(m[6], ".", "")); ampm != "" {
		if h < 1 || h > 12 {
			return time.Time{}, false
		}
		h %= 12
		if ampm == "pm" {
			h += 12
		}
	}
	if h > 23 || mi > 59 {
		return time.Time{}, false
	}
	y, mo, d := now.Date()
	if m[1] != "" {
		t, err := time.Parse("Jan", strings.ToUpper(m[1][:1])+strings.ToLower(m[1][1:]))
		if err != nil {
			return time.Time{}, false
		}
		mo = t.Month()
		d, _ = strconv.Atoi(m[2])
		y, _ = strconv.Atoi(m[3])
	}
	at := time.Date(y, mo, d, h, mi, 0, 0, now.Location())
	if m[1] == "" && !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	if !at.After(now) {
		return time.Time{}, false
	}
	return at, true
}

// resetsAt is how Claude Code's own limit message says when it resets: an
// hour of the day ("resets 3pm", "resets 3:30pm (Asia/Saigon)"), with the
// day first when it is not today ("resets Nov 13, 10am").
var resetsAt = regexp.MustCompile(`(?i)\bresets (?:([a-z]{3})[a-z]* (\d{1,2})(?:st|nd|rd|th)?,? (?:at )?)?(\d{1,2})(?::(\d{2}))?\s*([ap]m)\b(?:\s*\(([A-Za-z_]+(?:/[A-Za-z_+-]+)*)\))?`)

// resetsClock reads resetsAt, in the time zone it names (else now's): an
// hour already past today is tomorrow's, a day already past is next year's.
func resetsClock(msg string, now time.Time) (time.Time, bool) {
	m := resetsAt.FindStringSubmatch(msg)
	if m == nil {
		return time.Time{}, false
	}
	loc := now.Location()
	if m[6] != "" {
		if l, err := time.LoadLocation(m[6]); err == nil {
			loc = l
		}
	}
	h, _ := strconv.Atoi(m[3])
	mi, _ := strconv.Atoi(m[4])
	if h < 1 || h > 12 || mi > 59 {
		return time.Time{}, false
	}
	h %= 12
	if strings.EqualFold(m[5], "pm") {
		h += 12
	}
	ref := now.In(loc)
	y, mo, d := ref.Date()
	if m[1] != "" {
		t, err := time.Parse("Jan", strings.ToUpper(m[1][:1])+strings.ToLower(m[1][1:]))
		if err != nil {
			return time.Time{}, false
		}
		mo = t.Month()
		d, _ = strconv.Atoi(m[2])
	}
	at := time.Date(y, mo, d, h, mi, 0, 0, loc)
	if !at.After(now) {
		if m[1] == "" {
			at = at.AddDate(0, 0, 1)
		} else {
			at = at.AddDate(1, 0, 0)
		}
	}
	return at, true
}

// SetOnLimits hears every usage report of a provider (the limit alerts).
func (e *Engine) SetOnLimits(fn func(p storage.Provider, l Limits)) { e.onLimits = fn }
