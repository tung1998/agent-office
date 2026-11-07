package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// UpdateTicket is a blind full-row overwrite with no CAS, so two independent
// read-modify-write paths on the same ticket (an agent's support_set setting
// tags, an inbound message transitioning status) race: whichever write lands
// second silently reverts the first's field instead of being refused.
func TestSupportUpdateTicketConflictsWithConcurrentWrite(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	src, err := st.Support().CreateSource(ctx, storage.SupportSource{ProjectID: p.ID, Kind: "api", Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := st.Support().CreateTicket(ctx, storage.SupportTicket{
		ProjectID: p.ID, SourceID: src.ID, Subject: "help", Status: "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	// two independent reads of the same row, like support_set tagging it and
	// an inbound message moving it back to open at the same time.
	fromAgent := ticket
	fromInbound := ticket

	fromAgent.Tags = []string{"billing"}
	if err := st.Support().UpdateTicket(ctx, fromAgent); err != nil {
		t.Fatalf("first update: %v", err)
	}

	fromInbound.Status = "open"
	if err := st.Support().UpdateTicket(ctx, fromInbound); !errors.Is(err, storage.ErrStale) {
		t.Fatalf("stale second update = %v, want ErrStale", err)
	}

	got, err := st.Support().Ticket(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "billing" {
		t.Fatalf("tags set by the first update were lost: %+v", got)
	}
}
