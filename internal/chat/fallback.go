package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// choices are the connections a turn of agent may run on, in order: its own
// one, then its fallbacks top to bottom. One known to be over its limit right
// now goes to the back (still tried when every other one fails).
func (e *Engine) choices(ctx context.Context, agent storage.Agent) ([]provider.Choice, error) {
	chain, err := e.providers.Chain(ctx, agent)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, errors.New("chưa có kết nối AI mặc định")
	}
	if err != nil {
		return nil, err
	}
	var ok, over []provider.Choice
	for _, c := range chain {
		if e.overLimit(ctx, c.Provider.ID) {
			over = append(over, c)
		} else {
			ok = append(ok, c)
		}
	}
	return append(ok, over...), nil
}

// overLimit: the connection's last report says it is out of its quota until
// a window resets.
func (e *Engine) overLimit(ctx context.Context, providerID string) bool {
	var l Limits
	if ok, _ := e.store.Settings().Get(ctx, LimitsKey(providerID), &l); !ok {
		return false
	}
	now := time.Now()
	for _, w := range l.Windows {
		if w.ResetsAt.After(now) && (l.Status == "rejected" || w.Utilization >= 1) {
			return true
		}
	}
	return false
}

// tryNext: a failed run moves on to the next connection only when it failed
// before doing anything (no tokens written, no tool; its text is then the
// error, as the CLI reports it) — a run that already acted is not repeated
// elsewhere — and not because it was stopped or its session outgrew the model.
func tryNext(ctx context.Context, res RunResult, err error) bool {
	return err != nil && ctx.Err() == nil && res.Usage.OutputTokens == 0 && len(res.Tools) == 0 &&
		!strings.Contains(err.Error(), "Prompt is too long")
}

// switchNote is the status line shown when a turn moves to the next connection.
func switchNote(agent string, from, to provider.Choice, err error) string {
	return fmt.Sprintf("%s: %s lỗi (%s), chuyển sang %s", agent, from.Provider.Name, truncate(err.Error(), 160), to.Provider.Name)
}
