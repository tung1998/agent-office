package support

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tung1998/agent-office/internal/channels"
	"github.com/tung1998/agent-office/internal/collections"
	"github.com/tung1998/agent-office/internal/features"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-171: off by default; while off the bot tap takes nothing, the routes
// answer 404 and no tool is listed.
func TestOffChangesNothing(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop"})
	s := New(st)
	pass := func(h http.HandlerFunc) http.Handler { return h }
	mux := http.NewServeMux()
	s.Routes(mux, pass, pass)
	get := func() int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/projects/"+p.ID+"/support", nil))
		return w.Code
	}

	if s.On(ctx, p.ID) || get() != http.StatusNotFound || s.Tools(officetools.Scope{ProjectID: p.ID}) != nil ||
		s.Take(ctx, storage.Channel{ProjectID: p.ID}, channels.Incoming{Text: "hi"}) {
		t.Fatal("off by default: nothing taken, 404, no tools")
	}
	features.Save(ctx, st, p.ID, features.Config{Packs: map[string]bool{"support": true}})
	if list, _ := st.Collections().List(ctx, p.ID); len(list) != 0 {
		t.Fatal("off: no collection made")
	}
	if !s.On(ctx, p.ID) || get() != http.StatusOK {
		t.Fatal("turned on: the routes answer")
	}
	if list, _ := st.Collections().List(ctx, p.ID); len(list) != 2 || list[0].Key != "customers" || list[0].Pack != "support" {
		t.Fatalf("on: the pack's collections, got %+v", list)
	}
}

// A phone-only contact (no email) has no field customer() can dedup on
// reliably when two inbound messages race: Upsert must reject the second
// insert at the DB's unique index, not create a second "customers" record.
func TestCustomerPhoneOnlyConcurrentNoDuplicate(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop"})
	s := New(st)
	features.Save(ctx, st, p.ID, features.Config{Packs: map[string]bool{"support": true}})
	if !s.On(ctx, p.ID) {
		t.Fatal("support should turn on")
	}

	const n = 8
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			ids[i] = s.customer(ctx, p.ID, Contact{Name: "Khách", Phone: "+1555000111"})
		}(i)
	}
	wg.Wait()

	list, total, err := s.collections.Query(ctx, p.ID, "customers", collections.Query{Where: []storage.Cond{{Field: "phone", Value: "+1555000111"}}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("expected exactly one customer record for the phone, got %d: %+v", total, list)
	}
	for _, id := range ids {
		if id != list[0].ID {
			t.Fatalf("customer() returned ids pointing at different records: %v, want all %q", ids, list[0].ID)
		}
	}
}

// A project that had the support pack before this fix has a "customers"
// collection with phone/external_ref only Indexed, plus old records with a
// blank "" phone (e.g. edited from the dashboard). On() must upgrade those
// fields to Unique — scrubbing the blanks first so they don't collide with
// each other — and the race must then be closed for that project too, not
// just for ones turning the pack on for the first time.
func TestEnsureUpgradesExistingCollectionToUnique(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop"})
	cs := collections.New(st)

	// the pack's pre-fix shape: phone/external_ref only Indexed
	old := storage.Collection{Key: "customers", Name: "Khách hàng", Pack: "support", Fields: []storage.Field{
		{Name: "name", Label: "Tên", Type: storage.FieldText, Required: true},
		{Name: "email", Label: "Email", Type: storage.FieldText, Unique: true},
		{Name: "phone", Label: "Điện thoại", Type: storage.FieldText, Indexed: true},
		{Name: "external_ref", Label: "Mã ở hệ thống khác", Type: storage.FieldText, Indexed: true},
	}}
	if _, err := cs.Define(ctx, p.ID, old); err != nil {
		t.Fatal(err)
	}
	// old records with an explicit blank phone, allowed since it wasn't unique yet
	for i := 0; i < 2; i++ {
		if _, _, err := cs.Upsert(ctx, p.ID, "customers", "", map[string]any{"name": "No phone", "phone": ""}, "support"); err != nil {
			t.Fatal(err)
		}
	}
	features.Save(ctx, st, p.ID, features.Config{Packs: map[string]bool{"support": true}})

	s := New(st)
	if !s.On(ctx, p.ID) {
		t.Fatal("support should turn on")
	}
	c, err := st.Collections().Get(ctx, p.ID, "customers")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"phone", "external_ref"} {
		i := fieldIndex(c.Fields, name)
		if i < 0 || !c.Fields[i].Unique {
			t.Fatalf("field %q should now be Unique, got %+v", name, c.Fields)
		}
	}
	// the blank phones didn't collide, and existing-collection projects are
	// now covered by the same race protection as brand new ones
	if list, total, err := cs.Query(ctx, p.ID, "customers", collections.Query{Where: []storage.Cond{{Field: "name", Value: "No phone"}}}); err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("blank-phone records should survive the upgrade untouched, got %d %v %v", total, list, err)
	}

	const n = 8
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			ids[i] = s.customer(ctx, p.ID, Contact{Name: "Khách", Phone: "+1555000222"})
		}(i)
	}
	wg.Wait()
	list, total, err := cs.Query(ctx, p.ID, "customers", collections.Query{Where: []storage.Cond{{Field: "phone", Value: "+1555000222"}}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("expected exactly one customer record for the phone, got %d: %+v", total, list)
	}
	for _, id := range ids {
		if id != list[0].ID {
			t.Fatalf("customer() returned ids pointing at different records: %v, want all %q", ids, list[0].ID)
		}
	}
}

// A known customer C (email only) sends a message whose contact also gives
// A's phone number (A is a different, already-known customer, e.g. family
// sharing one phone). The pre-check in customer() resolves id=C by email, so
// Upsert(id=C, {email, phone}) hits A's phone unique index and conflicts.
// customer() must still return C's id, not "" — and must not touch A's
// record.
func TestCustomerKeepsIDOnFieldConflictWithAnotherRecord(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop"})
	s := New(st)
	features.Save(ctx, st, p.ID, features.Config{Packs: map[string]bool{"support": true}})
	if !s.On(ctx, p.ID) {
		t.Fatal("support should turn on")
	}

	aID := s.customer(ctx, p.ID, Contact{Name: "A", Phone: "+1555000333"})
	if aID == "" {
		t.Fatal("A should be created")
	}
	cID := s.customer(ctx, p.ID, Contact{Name: "C", Email: "c@x.com"})
	if cID == "" || cID == aID {
		t.Fatalf("C should be a distinct record, got %q (A=%q)", cID, aID)
	}

	got := s.customer(ctx, p.ID, Contact{Name: "C", Email: "c@x.com", Phone: "+1555000333"})
	if got != cID {
		t.Fatalf("customer() should keep resolving to C's id %q on a field conflict with A, got %q", cID, got)
	}

	a, _, err := s.collections.Get(ctx, p.ID, "customers", aID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Data["phone"] != "+1555000333" {
		t.Fatalf("A's record should be untouched, got %+v", a.Data)
	}

	list, total, err := s.collections.Query(ctx, p.ID, "customers", collections.Query{Where: []storage.Cond{{Field: "phone", Value: "+1555000333"}}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].ID != aID {
		t.Fatalf("exactly one record should still hold that phone (A's), got %d: %+v", total, list)
	}
}
