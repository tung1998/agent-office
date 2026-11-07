package channels

import (
	"context"
	"errors"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// failOnceAdapter fails the first Send for a given chat, then succeeds.
type failOnceAdapter struct {
	failed map[string]bool
	sent   map[string][]string
}

func (a *failOnceAdapter) Run(context.Context, func(string), func(Incoming)) error { return nil }
func (a *failOnceAdapter) Send(_ context.Context, chatID, text string) ([]string, error) {
	if !a.failed[chatID] {
		a.failed[chatID] = true
		return nil, errors.New("fake: temporary send failure")
	}
	a.sent[chatID] = append(a.sent[chatID], text)
	return []string{"1"}, nil
}
func (a *failOnceAdapter) SetCommands(context.Context, []Command) {}
func (a *failOnceAdapter) Typing(context.Context, string)         {}

// A cut notice whose Send fails (temporary network error, bot briefly
// disconnected again) must stay queued in m.cut instead of being dropped, so
// the next ready/reconnect can retry it (ADR-258 follow-up).
func TestSendCutNotesKeepsNoteOnSendFailure(t *testing.T) {
	ctx := context.Background()
	m := NewManager(nil, nil, nil, func(storage.Channel) (Adapter, error) { return nil, nil })
	ad := &failOnceAdapter{failed: map[string]bool{}, sent: map[string][]string{}}

	m.mu.Lock()
	m.cut = map[string][]cutNote{"ch1": {{chat: "41", text: CutNotice}}}
	m.mu.Unlock()

	m.sendCutNotes(ctx, "ch1", ad)

	m.mu.Lock()
	kept := m.cut["ch1"]
	m.mu.Unlock()
	if len(kept) != 1 || kept[0].chat != "41" || kept[0].text != CutNotice {
		t.Fatalf("note lost after a failed Send: m.cut[ch1] = %+v", kept)
	}
	if len(ad.sent["41"]) != 0 {
		t.Fatalf("Send should have failed and not recorded anything, got %v", ad.sent["41"])
	}

	// retry: this time Send succeeds, the note must go out and be cleared.
	m.sendCutNotes(ctx, "ch1", ad)

	m.mu.Lock()
	kept = m.cut["ch1"]
	m.mu.Unlock()
	if len(kept) != 0 {
		t.Fatalf("note should be cleared after a successful retry, got %+v", kept)
	}
	if got := ad.sent["41"]; len(got) != 1 || got[0] != CutNotice {
		t.Fatalf("note not sent on retry: %v", got)
	}
}
