// Package cliuse counts the runs that have an AI CLI (claude, codex,
// gemini…) running right now, and lets an update of that CLI hold new runs
// back while its files are replaced (ADR-259): npm swapping a package's
// native binary under a run is how a run dies with "spawn … ENOENT".
package cliuse

import (
	"context"
	"path/filepath"
	"sync"
)

// bins maps a CLI's command to its tool id (clitools' ids).
var bins = map[string]string{"claude": "claude", "codex": "codex", "gemini": "gemini", "agy": "antigravity", "dsh": "dsh"}

// Tool is the tool id of the CLI at bin ("" = not one office updates).
func Tool(bin string) string { return bins[filepath.Base(bin)] }

type hold struct{ done chan struct{} }

var (
	mu    sync.Mutex
	count = map[string]int{}
	holds = map[string]*hold{}
)

// Begin says a run is about to start the CLI at bin; call what it returns
// once the process is done. While an update of that CLI holds runs back, it
// waits for the update to let go (the update's own commands time out, so the
// wait is bounded), or until ctx ends: then the error is ctx's.
func Begin(ctx context.Context, bin string) (func(), error) {
	tool := Tool(bin)
	if tool == "" {
		return func() {}, nil
	}
	for {
		mu.Lock()
		h := holds[tool]
		if h == nil {
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
		select {
		case <-ctx.Done():
			return func() {}, ctx.Err()
		case <-h.done:
		}
	}
}

// InUse is how many runs have tool's CLI running now.
func InUse(tool string) int {
	mu.Lock()
	defer mu.Unlock()
	return count[tool]
}

// TryHold holds new runs of tool back only if none runs now, in one step
// (no run can start between the count and the hold); ok false leaves
// nothing held. An update when idle takes the CLI this way.
func TryHold(tool string) (release func(), ok bool) {
	mu.Lock()
	defer mu.Unlock()
	if count[tool] > 0 || holds[tool] != nil {
		return nil, false
	}
	return hold1(tool), true
}

// Hold holds new runs of tool back until release, whatever runs now (an
// update now, a rollback). A hold already on is kept: release ends this one.
func Hold(tool string) (release func()) {
	mu.Lock()
	defer mu.Unlock()
	return hold1(tool)
}

func hold1(tool string) func() {
	h := holds[tool]
	if h == nil {
		h = &hold{done: make(chan struct{})}
		holds[tool] = h
	}
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
	return holds[tool] != nil
}
