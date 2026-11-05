package selfupdate

import (
	"context"
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// Revision is the commit the running build was made from ("" built
// without git).
func Revision() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// Behind counts the commits of the source the running build (revision) has
// not got; -1 when it cannot be told (no revision, not in the source).
func (s Source) Behind(ctx context.Context, revision string) int {
	if revision == "" {
		return -1
	}
	out, err := gitFor(ctx, 3*time.Second, s.Root, "rev-list", "--count", revision+"..HEAD")
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return -1
	}
	return n
}

// AutoSettingKey stores the "update on its own when idle" switch (ADR-255).
const AutoSettingKey = "system.auto_update"

// AutoSetting is that switch.
type AutoSetting struct {
	Enabled bool `json:"enabled"`
}

// autoWatch decides when a source change is updated on its own.
type autoWatch struct {
	settle time.Duration
	seen   string          // the source's HEAD last seen
	since  time.Time       // since when it is that
	tried  map[string]bool // the HEADs already updated to (or tried): never twice
}

// due: the source moved past the running build, and has not moved for
// settle (a run of merged diffs lands as one update); a HEAD is tried once.
func (a *autoWatch) due(running, head string, now time.Time) bool {
	if head == "" || running == "" {
		return false
	}
	if head != a.seen {
		a.seen, a.since = head, now
	}
	if head == running || a.tried[head] || now.Sub(a.since) < a.settle {
		return false
	}
	a.tried[head] = true
	return true
}

// AutoBy is who an update office starts on its own is by.
const AutoBy = "tự cập nhật"

// Watch updates office on its own when its source gets new commits and the
// switch is on: a when-idle update with tests, once the commits stop coming
// for settle. It never builds over files being edited (tracked changes not
// committed). running is the running build's revision.
func (u *Updater) Watch(ctx context.Context, enabled func(context.Context) bool, running string, every, settle time.Duration) {
	if running == "" {
		return // built by hand without git: no way to tell what is newer
	}
	a := &autoWatch{settle: settle, tried: map[string]bool{}}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !enabled(ctx) {
			continue
		}
		switch u.State().Status {
		case "running", "waiting", "restarting":
			continue
		}
		head, err := git(ctx, u.src.Root, "rev-parse", "HEAD")
		if err != nil {
			continue
		}
		if !a.due(running, strings.TrimSpace(string(head)), u.now()) {
			continue
		}
		if dirty, err := git(ctx, u.src.Root, "status", "--porcelain", "--untracked-files=no"); err != nil || strings.TrimSpace(string(dirty)) != "" {
			delete(a.tried, a.seen) // looked at again once the edits are committed
			continue
		}
		if err := u.Start(Options{WhenIdle: true, Test: true, By: AutoBy}); err != nil {
			slog.Warn("selfupdate: auto update", "err", err)
		}
	}
}
