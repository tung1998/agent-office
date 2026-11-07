package api

import (
	"sync"
	"sync/atomic"
	"testing"
)

// gitLock must give the project's git lock to exactly one caller at a time:
// a check-then-store split (gitProjectBusy then a separately keyed
// LoadOrStore) leaves a window where two concurrent callers both pass the
// check before either stores, so both would acquire. Run many goroutines at
// once (under -race) to catch that.
func TestGitLockExclusive(t *testing.T) {
	const n = 64
	var wg sync.WaitGroup
	var acquired int32
	releases := make(chan func(), n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		kind := "op"
		if i%2 == 0 {
			kind = "push"
		}
		go func(kind string) {
			defer wg.Done()
			if release, busy := gitLock("proj-x", kind); !busy {
				atomic.AddInt32(&acquired, 1)
				releases <- release
			}
		}(kind)
	}
	wg.Wait()
	close(releases)
	if acquired != 1 {
		t.Fatalf("acquired = %d, want exactly 1 (two git processes must never race on the same repo)", acquired)
	}
	for release := range releases {
		release()
	}
	if gitProjectBusy("proj-x") {
		t.Fatal("lock still held after release")
	}
	if release, busy := gitLock("proj-x", "fetch"); busy {
		t.Fatal("lock should be free again")
	} else {
		release()
	}
}
