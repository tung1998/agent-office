package events_test

import (
	"slices"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/events"
)

// Writes close together reach every subscriber once, as one set of tables;
// tables nobody follows (sessions…) are not told.
func TestBus(t *testing.T) {
	b := events.New(30 * time.Millisecond)
	sa, stopA := b.Subscribe(events.Viewer{UserID: "u1"})
	sc, stopC := b.Subscribe(events.Viewer{UserID: "u2"})
	a, c := sa.C, sc.C
	defer stopC()
	b.Wrote("messages")
	b.Wrote("jobs")
	b.Wrote("messages")
	b.Wrote("sessions")
	for _, ch := range []<-chan events.Event{a, c} {
		select {
		case ev := <-ch:
			got := ev.Data.(map[string]any)["tables"].([]string)
			if len(got) != 2 || !slices.Contains(got, "messages") || !slices.Contains(got, "jobs") {
				t.Fatalf("got %v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("nothing told")
		}
	}
	stopA()
	b.Wrote("sessions")
	select {
	case got := <-c:
		t.Fatalf("an unfollowed table was told: %v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// Pushed data goes to the pages it is for: admins only, or those that turned
// a topic on (and only their owner may turn it on).
func TestSendTo(t *testing.T) {
	b := events.New(time.Millisecond)
	admin, stop1 := b.Subscribe(events.Viewer{UserID: "a", Admin: true})
	defer stop1()
	member, stop2 := b.Subscribe(events.Viewer{UserID: "m"})
	defer stop2()
	admins := func(v events.Viewer, _ map[string]bool) bool { return v.Admin }
	b.Send(events.Event{Name: "stats", Data: 1}, admins)
	if ev := <-admin.C; ev.Name != "stats" {
		t.Fatal(ev)
	}
	select {
	case ev := <-member.C:
		t.Fatalf("a member got %v", ev)
	default:
	}
	if b.SetTopic(admin.ID, "m", "machine", true) {
		t.Fatal("someone else turned a topic on")
	}
	var told []string
	b.OnSubscribe(nil, func(s *events.Sub, topic string) { told = append(told, topic) })
	b.SetTopic(admin.ID, "a", "machine", true)
	watching := func(v events.Viewer, topics map[string]bool) bool { return v.Admin && topics["machine"] }
	if b.Count(watching) != 1 || len(told) != 1 {
		t.Fatalf("watching = %d, told %v", b.Count(watching), told)
	}
	b.SetTopic(admin.ID, "a", "machine", false)
	if b.Count(watching) != 0 {
		t.Fatal("still watching")
	}
}
