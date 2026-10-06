package auth

import (
	"fmt"
	"testing"
	"time"
)

// Failures for ever-new keys (random emails) never grow the map past maxKeys.
func TestThrottleBounded(t *testing.T) {
	th := newThrottle(5, 15*time.Minute)
	now := time.Now()
	for i := 0; i < maxKeys+500; i++ {
		th.fail(fmt.Sprintf("u%d@x.io", i), now)
	}
	if n := th.size(); n > maxKeys {
		t.Fatalf("size = %d, want <= %d", n, maxKeys)
	}
	// a key failing again keeps counting
	for i := 0; i < 5; i++ {
		th.fail("admin@x.io", now)
	}
	if !th.locked("admin@x.io", now) {
		t.Fatal("admin not locked")
	}
}
