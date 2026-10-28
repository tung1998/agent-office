package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// choices are the connections a turn of agent may run on, in order: its own
// one, then its fallbacks top to bottom. One known to be over its limit right
// now or marked broken goes to the back (still tried when every other one fails); one past a
// stop threshold the person set is left out (ADR-136), and when that leaves
// none the turn does not start (a *CapError, read as out of tokens).
func (e *Engine) choices(ctx context.Context, agent storage.Agent) ([]provider.Choice, error) {
	chain, err := e.providers.Chain(ctx, agent)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, errors.New("chưa có kết nối AI mặc định")
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var ok, over []provider.Choice
	var capped *CapError
	for _, c := range chain {
		l, has := e.limits(ctx, c.Provider.ID)
		if ce := capHit(c.Provider, l, has, now); ce != nil {
			if capped == nil || ce.Until.Before(capped.Until) { // the first one free again
				capped = ce
			}
			continue
		}
		if (has && overNow(l, now)) || c.Provider.Status == "error" { // a broken one too: its fallbacks answer first
			over = append(over, c)
		} else {
			ok = append(ok, c)
		}
	}
	if len(ok)+len(over) == 0 && capped != nil {
		return nil, capped
	}
	return append(ok, over...), nil
}

// CapStop: every connection agent may run on is past its stop threshold —
// until the first of them resets (a Burn waits for it).
func (e *Engine) CapStop(ctx context.Context, agent storage.Agent) (time.Time, bool) {
	if e.providers == nil {
		return time.Time{}, false
	}
	var ce *CapError
	if _, err := e.choices(ctx, agent); errors.As(err, &ce) {
		return ce.Until, true
	}
	return time.Time{}, false
}

// CapError: a turn not started because its connections passed the stop
// thresholds the person set on them. Its text says "quota", so the turn is
// out of tokens: the chat goes on once a window resets.
type CapError struct {
	Provider string
	Window   string
	Percent  int
	Until    time.Time
}

func (c *CapError) Error() string {
	name := map[string]string{"five_hour": "giới hạn 5 giờ", "seven_day": "giới hạn tuần"}[c.Window] // i18n-ignore
	if name == "" {
		name = c.Window
	}
	return fmt.Sprintf("%s đã dùng %d%% %s, chạm ngưỡng dừng quota đã đặt: chờ tới %s", // i18n-ignore
		c.Provider, c.Percent, name, c.Until.Local().Format("02/01 15:04"))
}

// capHit: p's last report shows a window at or past the stop threshold set
// for it, not reset yet. Past several: the one resetting last holds it.
func capHit(p storage.Provider, l Limits, has bool, now time.Time) *CapError {
	if !has || len(p.LimitCaps) == 0 {
		return nil
	}
	var hit *CapError
	for name, pct := range p.LimitCaps {
		w, ok := l.Windows[name]
		if pct <= 0 || !ok || !w.ResetsAt.After(now) || w.Utilization*100 < float64(pct)-0.5 {
			continue
		}
		if hit == nil || w.ResetsAt.After(hit.Until) {
			hit = &CapError{Provider: p.Name, Window: name, Percent: int(w.Utilization*100 + 0.5), Until: w.ResetsAt}
		}
	}
	return hit
}

// limits is the connection's last usage report.
func (e *Engine) limits(ctx context.Context, providerID string) (Limits, bool) {
	var l Limits
	ok, _ := e.store.Settings().Get(ctx, LimitsKey(providerID), &l)
	return l, ok
}

// overLimit: the connection's last report says it is out of its quota until
// a window resets.
func (e *Engine) overLimit(ctx context.Context, providerID string) bool {
	l, ok := e.limits(ctx, providerID)
	return ok && overNow(l, time.Now())
}

func overNow(l Limits, now time.Time) bool {
	hasFullWindow := false
	for _, w := range l.Windows {
		if w.Utilization < 1 {
			continue
		}
		hasFullWindow = true
		if w.ResetsAt.After(now) {
			return true
		}
	}
	// A rejected report with no full window has no reliable reset time.
	// Once a reported full window resets, a still-open weekly window with
	// spare capacity must not keep the connection behind its fallback.
	return l.Status == "rejected" && !hasFullWindow && l.UpdatedAt.Add(RejectedCooldown).After(now)
}

// RejectedCooldown is how long a provider stays deprioritized/blocked after a
// "rejected" report that carried no window detail to time a real reset by.
// Shared with burn.limitHit so chat fallback and Burn's wait agree on it.
const RejectedCooldown = 5 * time.Minute

// tryNext: a failed run moves on to the next connection only when it failed
// before doing anything (no tokens written, no tool; its text is then the
// error, as the CLI reports it) — a run that already acted is not repeated
// elsewhere — and not because it was stopped or its session outgrew the model.
// A read-only run (a review, a perspective of a workflow) changed nothing:
// one that read files then ended with no answer (agy's quota or a tool that
// broke) is asked again on the next connection, not lost.
func tryNext(ctx context.Context, res RunResult, err error, readOnly bool) bool {
	if err == nil || ctx.Err() != nil || strings.Contains(err.Error(), "Prompt is too long") {
		return false
	}
	return (res.Usage.OutputTokens == 0 && len(res.Tools) == 0) || (readOnly && strings.TrimSpace(res.Text) == "")
}

// readOnly: a turn at level that can change nothing, files or proposals,
// and not run with the machine's full access.
func readOnly(level string, req RunRequest) bool {
	return !perm.AtLeast(level, perm.Propose) && !req.Write && !req.FullAccess
}

// switchNote is the status line shown when a turn moves to the next connection.
func switchNote(agent string, from, to provider.Choice, err error) string {
	return fmt.Sprintf("%s: %s lỗi (%s), chuyển sang %s", agent, choiceName(from, to), truncate(err.Error(), 160), choiceName(to, from))
}

// choiceName names a connection, with its model when other names the same one.
func choiceName(c, other provider.Choice) string {
	if c.Provider.ID == other.Provider.ID && c.Model != "" {
		return c.Provider.Name + " · " + c.Model
	}
	return c.Provider.Name
}

// connectionBroken: a run failed on the connection itself, not on the task:
// its account is signed out or its CLI never answered.
func connectionBroken(err error) bool {
	return errors.Is(err, llm.ErrNeedsLogin) || errors.Is(err, ErrSilent)
}

// keepHealth writes what a run showed of its connection onto it, so Kết nối
// AI and the chat say it is broken instead of the last good check; a run that
// answers on a connection marked broken clears the mark.
func (e *Engine) keepHealth(p storage.Provider, err error) {
	ctx := context.Background()
	switch {
	case connectionBroken(err):
		_ = e.store.Providers().SetStatus(ctx, p.ID, "error", err.Error(), nil, time.Now())
	case err == nil && p.Status == "error":
		_ = e.store.Providers().SetStatus(ctx, p.ID, "ok", "lượt chạy gần nhất trả lời được", nil, time.Now())
	}
}
