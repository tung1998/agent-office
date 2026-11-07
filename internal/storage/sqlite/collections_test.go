package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// Save is CAS'd on updated_at, so two concurrent Upserts that both read the
// same record (like two agents or two dashboard tabs patching one record)
// can't have the second silently overwrite the first's write: the loser
// gets ErrStale instead of dropping a change.
func TestCollectionSaveConflictsWithConcurrentWrite(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	c, err := st.Collections().Create(ctx, storage.Collection{ProjectID: p.ID, Key: "notes", Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.Collections().Insert(ctx, storage.Record{
		CollectionID: c.ID, ProjectID: p.ID, Data: map[string]any{"text": "a"}, CreatedBy: "u",
	})
	if err != nil {
		t.Fatal(err)
	}

	// two independent reads of the same record, racing each other.
	fromA, err := st.Collections().Record(ctx, c.ID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	fromB, err := st.Collections().Record(ctx, c.ID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}

	fromA.Data["text"] = "b"
	fromA.UpdatedBy = "u"
	if err := st.Collections().Save(ctx, fromA); err != nil {
		t.Fatalf("first save: %v", err)
	}

	fromB.Data["text"] = "c"
	fromB.UpdatedBy = "u"
	if err := st.Collections().Save(ctx, fromB); !errors.Is(err, storage.ErrStale) {
		t.Fatalf("stale second save = %v, want ErrStale", err)
	}

	got, err := st.Collections().Record(ctx, c.ID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Data["text"] != "b" {
		t.Fatalf("stale write landed despite ErrStale: %+v", got)
	}
}
