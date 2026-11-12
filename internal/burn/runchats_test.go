package burn_test

import (
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/storage"
)

// Each run gets the Burn chat made in its own window; the current run its saved one.
func TestRunChats(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	runs := map[string]time.Time{"burn/a": t0, "burn/b": t0.Add(24 * time.Hour), "burn/c": t0.Add(48 * time.Hour)}
	chat := func(id string, at time.Time, purpose string) storage.Conversation {
		return storage.Conversation{ID: id, CreatedAt: at, Purpose: purpose}
	}
	chats := []storage.Conversation{
		chat("a1", t0.Add(time.Second), "burn"),
		chat("a2", t0.Add(3*time.Hour), "burn"), // its agent changed mid-run: the later one
		chat("x", t0.Add(4*time.Hour), ""),      // not a Burn chat
		chat("b1", t0.Add(24*time.Hour+time.Second), "burn"),
		chat("c-old", t0.Add(48*time.Hour+time.Second), "burn"),
	}
	got := burn.RunChats(runs, "burn/c", "c-now", chats)
	want := map[string]string{"burn/a": "a2", "burn/b": "b1", "burn/c": "c-now"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q (all %v)", k, got[k], v, got)
		}
	}
	// a run with no chat of its own gets none
	if got := burn.RunChats(map[string]time.Time{"burn/z": t0.Add(-time.Hour)}, "", "", nil); len(got) != 0 {
		t.Errorf("no chats: got %v", got)
	}
}
