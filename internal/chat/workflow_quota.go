package chat

import (
	"errors"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// A run's time while its steps wait for their agents' quota (ADR-270): the
// wait is not work, so the run's expected end moves on by it (a week's
// quota no longer reads as a run past its time, told to be stopped), a run
// that called it stands still with it, and a person may end a wait now.

// QuotaMaxWait is the longest a step waits for its agent's quota: a weekly
// limit and some. A reset further off fails the run, saying why.
var QuotaMaxWait = 8 * 24 * time.Hour

// ErrNoQuotaWait: no step of the run waits for its agent's quota.
var ErrNoQuotaWait = errors.New("không có bước nào đang chờ quota")

// pauseTime stops the clock of run, and of the runs that called it (the
// wait spends their time too), from from on: a step starts waiting for
// quota. With e.wf.mu held.
func (e *Engine) pauseTime(run *wfRun, from time.Time) {
	for r := run; r != nil; r = r.parent {
		if r.quotaWaiting == 0 {
			r.pausedAt = from
			if r.timer != nil {
				r.timer.Stop()
			}
		} else if from.Before(r.pausedAt) {
			r.pausedAt = from
		}
		r.quotaWaiting++
	}
}

// goOnTime starts the clocks pauseTime stopped again once no step of a run
// waits any more (at: when the wait ended): its expected end moves on by
// the time it stood still. With e.wf.mu held.
func (e *Engine) goOnTime(run *wfRun, at time.Time) {
	for r := run; r != nil; r = r.parent {
		if r.quotaWaiting == 0 {
			continue
		}
		if r.quotaWaiting--; r.quotaWaiting > 0 {
			continue
		}
		d := max(at.Sub(r.pausedAt), 0)
		r.paused += d
		r.deadline = r.deadline.Add(d)
		r.pausedAt = time.Time{}
		e.armDeadline(r)
	}
}

// armDeadline sets the run's timer to its expected end again, unless the
// chat was already told it ran past it. With e.wf.mu held.
func (e *Engine) armDeadline(r *wfRun) {
	if r.expired || r.rec.Status != storage.RunRunning {
		return
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	conv, id := r.rec.ConversationID, r.rec.ID
	r.timer = time.AfterFunc(max(time.Until(r.deadline), 0), func() { e.wfExpire(conv, id) })
}

// SkipQuotaWait ends now the quota waits of a run's steps (Chạy ngay): they
// try their agent at once. How many it ended, or ErrNoQuotaWait.
func (e *Engine) SkipQuotaWait(runID string) (int, error) {
	e.wf.mu.Lock()
	defer e.wf.mu.Unlock()
	for _, r := range e.wf.byConv {
		if r.rec.ID != runID {
			continue
		}
		n := 0
		for id, ch := range r.quotaSkip {
			close(ch)
			delete(r.quotaSkip, id)
			n++
		}
		if n == 0 {
			return 0, ErrNoQuotaWait
		}
		return n, nil
	}
	return 0, ErrNoQuotaWait
}
