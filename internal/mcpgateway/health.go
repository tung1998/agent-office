package mcpgateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// MCP health (ADR-250): what office knows of a server comes from its checks
// and from the runs' real calls. A server found broken is not given to runs
// (NamesFor) until a check or a call finds it working again; the person is
// told once per break (BrokenSince, an incident).

// Broken reports whether a server's last known status keeps it from runs.
func Broken(m storage.MCPServer) bool {
	return m.LastCheckStatus == "error" || m.LastCheckStatus == "needs_login"
}

// BrokenSinceKey is the setting holding when the server last stopped
// working (a zero time: it works).
func BrokenSinceKey(id string) string { return "mcp_broken_since/" + id }

// BrokenSince is when m stopped working (zero: it works, or office never
// saw it work and break).
func BrokenSince(ctx context.Context, st storage.Store, id string) time.Time {
	var at time.Time
	_, _ = st.Settings().Get(ctx, BrokenSinceKey(id), &at)
	return at
}

// errClosed: office is going down; says nothing of the server (not saved).
var errClosed = errors.New("office đang tắt")

// backoffError is a stdio start refused while an earlier failure's backoff
// runs: the same failure again, not a new one (B2: not logged as a warning).
type backoffError struct {
	err  error
	wait time.Duration
}

func (e *backoffError) Error() string {
	return fmt.Sprintf("%v (thử chạy lại sau %d giây)", e.err, int(e.wait.Seconds())+1)
}

func (e *backoffError) Unwrap() error { return e.err }

// statusOf turns what a check or a call found into a status and its message;
// a backoff says the failure it waits on, so the message stays the same.
func statusOf(err error) (string, string) {
	if err == nil {
		return "ok", ""
	}
	if errors.Is(err, ErrNeedsLogin) || errors.Is(err, ErrSessionExpired) {
		return "needs_login", err.Error()
	}
	var b *backoffError
	if errors.As(err, &b) {
		return "error", b.err.Error()
	}
	return "error", err.Error()
}

// health serializes the status writes of real calls.
type health struct{ mu sync.Mutex }

// note records what a run's real call found of m: written only when its
// status changes (a broken server called many times writes once).
func (g *Gateway) note(ctx context.Context, m storage.MCPServer, err error) {
	if errors.Is(err, errClosed) || errors.Is(err, context.Canceled) {
		return
	}
	status, msg := statusOf(err)
	if m.LastCheckStatus == status {
		return
	}
	ctx = context.WithoutCancel(ctx)
	g.health.mu.Lock()
	defer g.health.mu.Unlock()
	fresh, ferr := g.Store.MCPServers().Get(ctx, m.ID)
	if ferr != nil || fresh.LastCheckStatus == status {
		return
	}
	if _, serr := g.save(ctx, fresh, status, msg, fresh.LastTools); serr != nil {
		g.log().Warn("mcp gateway: save status", "server", m.Name, "err", serr)
	}
}

// save stores a status of m, keeps when it broke, and tells open pages when
// it changed.
func (g *Gateway) save(ctx context.Context, m storage.MCPServer, status, msg string, tools []storage.MCPTool) (storage.MCPServer, error) {
	prev := m.LastCheckStatus
	now := time.Now().UTC()
	if err := g.Store.MCPServers().SetCheck(ctx, m.ID, status, msg, tools, now); err != nil {
		return m, err
	}
	m.LastCheckAt, m.LastCheckStatus, m.LastCheckError, m.LastTools = &now, status, msg, tools
	switch {
	case status == "ok":
		if prev != status {
			_ = g.Store.Settings().Set(ctx, BrokenSinceKey(m.ID), time.Time{})
		}
	case !Broken(storage.MCPServer{LastCheckStatus: prev}) || BrokenSince(ctx, g.Store, m.ID).IsZero():
		// a new break, or one from before office kept when (still broken
		// at this check): kept once, so its alert's key stays the same
		_ = g.Store.Settings().Set(ctx, BrokenSinceKey(m.ID), now)
	}
	if prev == status {
		return m, nil
	}
	g.log().Info("mcp gateway: status changed", "server", m.Name, "from", prev, "to", status)
	if g.OnStatus != nil {
		g.OnStatus(m)
	}
	return m, nil
}

// clearBackoff forgets a stdio server's start failures: a check asked for
// (after installing what it needs) tries at once (B3).
func (g *Gateway) clearBackoff(id string) {
	pl := &g.pool
	pl.mu.Lock()
	defer pl.mu.Unlock()
	delete(pl.fails, id)
}

// Recheck intervals of the background sweep: a broken server soon (it heals
// once its command is installed or its login done), a working one seldom.
const (
	RecheckBroken = 15 * time.Minute
	RecheckOK     = 6 * time.Hour
)

// Due reports whether m should be checked again at now.
func Due(m storage.MCPServer, now time.Time) bool {
	if !m.Enabled || m.Scope != "machine" || (m.Kind != "http" && m.Kind != "stdio") {
		return false
	}
	if m.LastCheckAt == nil {
		return true
	}
	every := RecheckOK
	if Broken(m) {
		every = RecheckBroken
	}
	return now.Sub(*m.LastCheckAt) >= every
}

// CheckDue checks every server Due at now, one after another; it returns
// how many it checked. A server waiting for a login is not checked (a check
// can not log in) unless all is set.
func (g *Gateway) CheckDue(ctx context.Context, now time.Time, all bool) (int, error) {
	list, err := g.Store.MCPServers().List(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range list {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		if all {
			if !m.Enabled || m.Scope != "machine" || (m.Kind != "http" && m.Kind != "stdio") {
				continue
			}
		} else if !Due(m, now) || m.LastCheckStatus == "needs_login" {
			continue
		}
		if _, err := g.CheckServer(ctx, m); err != nil {
			g.log().Warn("mcp gateway: check", "server", m.Name, "err", err)
			continue
		}
		n++
	}
	return n, nil
}

// Sweep rechecks due servers every tick until ctx ends (0: a minute).
func (g *Gateway) Sweep(ctx context.Context, tick time.Duration) {
	if tick <= 0 {
		tick = time.Minute
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			_, _ = g.CheckDue(ctx, now, false)
		}
	}
}
