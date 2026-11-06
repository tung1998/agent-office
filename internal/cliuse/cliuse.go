// Package cliuse counts the runs that have an AI CLI (claude, codex,
// gemini…) running right now, and lets an update of that CLI hold new runs
// back while its files are replaced (ADR-259): npm swapping a package's
// native binary under a run is how a run dies with "spawn … ENOENT".
package cliuse

import (
	"context"
	"path/filepath"
	"sync"
	"time"
)

// bins maps a CLI's command to its tool id (clitools' ids).
var bins = map[string]string{"claude": "claude", "codex": "codex", "gemini": "gemini", "agy": "antigravity", "dsh": "dsh"}

// Tool is the tool id of the CLI at bin ("" = not one office updates).
func Tool(bin string) string { return bins[filepath.Base(bin)] }

type hold struct {
	done  chan struct{}
	until time.Time
}

var (
	mu    sync.Mutex
	count = map[string]int{}
	holds = map[string]*hold{}
)

// Begin says a run is about to start the CLI at bin; call what it returns
// once the process is done. While an update of that CLI holds runs back, it
// waits for the update to end (at most the hold's own limit, or until ctx
// ends: then the error is ctx's).
func Begin(ctx context.Context, bin string) (func(), error) {
	tool := Tool(bin)
	if tool == "" {
		return func() {}, nil
	}
	for {
		mu.Lock()
		h := holds[tool]
		if h == nil || !time.Now().Before(h.until) {
			count[tool]++
			mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					mu.Lock()
					if count[tool]--; count[tool] <= 0 {
						delete(count, tool)
					}
					mu.Unlock()
				})
			}, nil
		}
		mu.Unlock()
		t := time.NewTimer(time.Until(h.until))
		select {
		case <-ctx.Done():
			t.Stop()
			return func() {}, ctx.Err()
		case <-h.done:
		case <-t.C:
		}
		t.Stop()
	}
}

// InUse is how many runs have tool's CLI running now.
func InUse(tool string) int {
	mu.Lock()
	defer mu.Unlock()
	return count[tool]
}

// Hold makes new runs of tool wait (at most max) until release is called:
// an update replacing its files. A hold already on is replaced.
func Hold(tool string, max time.Duration) (release func()) {
	h := &hold{done: make(chan struct{}), until: time.Now().Add(max)}
	mu.Lock()
	if old := holds[tool]; old != nil {
		close(old.done)
	}
	holds[tool] = h
	mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			if holds[tool] == h {
				delete(holds, tool)
				close(h.done)
			}
			mu.Unlock()
		})
	}
}

// Held says whether runs of tool wait for an update now.
func Held(tool string) bool {
	mu.Lock()
	defer mu.Unlock()
	h := holds[tool]
	return h != nil && time.Now().Before(h.until)
}
