package proctrack

import (
	"context"
	"testing"
)

func TestTrack(t *testing.T) {
	ctx := With(context.Background(), Info{Kind: "agent", TurnID: "t1", Label: "Dev"})
	done := Track(ctx, 4242)
	if info, ok := Lookup(4242); !ok || info.TurnID != "t1" {
		t.Fatalf("lookup = %+v %v", info, ok)
	}
	done()
	if _, ok := Lookup(4242); ok {
		t.Fatal("still tracked after done")
	}
	Track(context.Background(), 7)()
	if _, ok := Lookup(7); ok {
		t.Fatal("a process without info was tracked")
	}
}
