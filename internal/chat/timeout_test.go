package chat

import (
	"context"
	"testing"
	"time"
)

// ADR-082: a chat's turn has 20 minutes; an automation's says its own, and 0
// is no limit at all.
func TestTurnTimeout(t *testing.T) {
	ctx := context.Background()
	if turnTimeout(ctx) != 20*time.Minute {
		t.Fatalf("default = %v", turnTimeout(ctx))
	}
	none := WithTurnTimeout(ctx, 0)
	if turnTimeout(none) != 0 {
		t.Fatalf("0 = %v", turnTimeout(none))
	}
	c, cancel := withTimeout(none, turnTimeout(none))
	defer cancel()
	if _, has := c.Deadline(); has {
		t.Fatal("no limit still has a deadline")
	}
	c2, cancel2 := withTimeout(ctx, 3*time.Minute)
	defer cancel2()
	if d, has := c2.Deadline(); !has || time.Until(d) > 3*time.Minute {
		t.Fatalf("3 minutes = %v %v", d, has)
	}
}
