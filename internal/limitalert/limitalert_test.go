package limitalert_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/limitalert"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// Past the threshold a window is told once per cycle, once more at 95%; a
// new cycle (another reset) starts over; nothing without a destination.
func TestAlerts(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	var sent []string
	a := limitalert.New(st, func(_ context.Context, channelID, chatID, text string) error {
		sent = append(sent, channelID+"|"+chatID+"|"+text)
		return nil
	})
	reset := time.Now().Add(2 * time.Hour)
	a.Check(ctx, "prv_1", "Claude Code", "five_hour", 0.9, reset)
	if len(sent) != 0 {
		t.Fatal("told with no destination")
	}
	limitalert.Save(ctx, st, limitalert.Settings{ChannelID: "chn_1", ChatID: "c9", Threshold: 80})
	a.Check(ctx, "prv_1", "Claude Code", "five_hour", 0.5, reset)
	a.Check(ctx, "prv_1", "Claude Code", "five_hour", 0.82, reset)
	a.Check(ctx, "prv_1", "Claude Code", "five_hour", 0.85, reset)
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "chn_1|c9|") || !strings.Contains(sent[0], "82%") {
		t.Fatalf("sent = %q", sent)
	}
	a.Check(ctx, "prv_1", "Claude Code", "five_hour", 0.96, reset)
	a.Check(ctx, "prv_1", "Claude Code", "five_hour", 0.99, reset)
	if len(sent) != 2 || !strings.Contains(sent[1], "96%") {
		t.Fatalf("at 95%%: %q", sent)
	}
	a.Check(ctx, "prv_1", "Claude Code", "five_hour", 0.81, reset.Add(5*time.Hour)) // the next cycle
	a.Check(ctx, "prv_1", "Claude Code", "seven_day", 0.81, reset)                  // another window
	if len(sent) != 4 {
		t.Fatalf("new cycle / window: %q", sent)
	}
}

// Runs ending together report the same window at once: told once.
func TestAlertOnceWhenTogether(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	limitalert.Save(ctx, st, limitalert.Settings{ChannelID: "chn_1", ChatID: "c9", Threshold: 80})
	var n atomic.Int32
	a := limitalert.New(st, func(context.Context, string, string, string) error { n.Add(1); return nil })
	reset := time.Now().Add(2 * time.Hour)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); a.Check(ctx, "prv_1", "Claude Code", "five_hour", 0.85, reset) }()
	}
	wg.Wait()
	if n.Load() != 1 {
		t.Fatalf("told %d times", n.Load())
	}
}
