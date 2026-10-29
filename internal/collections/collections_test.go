package collections

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func setup(t *testing.T) (*Service, string) {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop"})
	return New(st), p.ID
}

var customers = storage.Collection{Key: "customers", Name: "Khách hàng", Fields: []storage.Field{
	{Name: "name", Type: storage.FieldText, Required: true},
	{Name: "email", Type: storage.FieldText, Unique: true},
	{Name: "tier", Type: storage.FieldEnum, Options: []string{"vip", "normal"}, Indexed: true},
	{Name: "tags", Type: storage.FieldTags},
	{Name: "orders", Type: storage.FieldNumber},
	{Name: "active", Type: storage.FieldBool},
}}

// ADR-172: records checked against the schema, an upsert matching a unique
// field, filters, search without accents, history.
func TestRecords(t *testing.T) {
	ctx := context.Background()
	s, p := setup(t)
	if _, err := s.Define(ctx, p, customers); err != nil {
		t.Fatal(err)
	}
	a, created, err := s.Upsert(ctx, p, "customers", "", map[string]any{"name": "Nguyễn An", "email": "an@x.vn", "tier": "vip", "tags": "hay hỏi, vip", "orders": 3.0, "active": true}, "agent:cskh")
	if err != nil || !created {
		t.Fatalf("insert: %v %v", created, err)
	}
	// the same email: the same record, patched
	b, created, err := s.Upsert(ctx, p, "customers", "", map[string]any{"email": "an@x.vn", "orders": "4"}, "agent:cskh")
	if err != nil || created || b.ID != a.ID || b.Data["orders"] != 4.0 || b.Data["name"] != "Nguyễn An" {
		t.Fatalf("upsert by unique: %+v %v %v", b, created, err)
	}
	s.Upsert(ctx, p, "customers", "", map[string]any{"name": "Trần Bình", "email": "binh@x.vn", "tier": "normal", "active": false}, "person")

	for _, c := range []struct {
		q    Query
		want int
	}{
		{Query{Where: []storage.Cond{{Field: "tier", Op: "eq", Value: "vip"}}}, 1},
		{Query{Where: []storage.Cond{{Field: "tags", Op: "contains", Value: "hay hỏi"}}}, 1},
		{Query{Where: []storage.Cond{{Field: "orders", Op: "gte", Value: 4}}}, 1},
		{Query{Where: []storage.Cond{{Field: "active", Op: "eq", Value: false}}}, 1},
		{Query{Where: []storage.Cond{{Field: "tier", Op: "in", Value: []any{"vip", "normal"}}}}, 2},
		{Query{Search: "nguyen"}, 1},
		{Query{}, 2},
	} {
		got, total, err := s.Query(ctx, p, "customers", c.q)
		if err != nil || total != c.want || len(got) != c.want {
			t.Fatalf("%+v: %d %v", c.q, total, err)
		}
	}
	got, _, _ := s.Query(ctx, p, "customers", Query{Sort: "name"})
	if got[0].Data["name"] != "Nguyễn An" {
		t.Fatalf("sorted by name: %v", got[0].Data)
	}
	if h, _ := s.History(ctx, p, "customers", a.ID); len(h) != 2 || h[0].Op != "update" || h[1].Op != "create" {
		t.Fatalf("history: %+v", h)
	}

	for name, data := range map[string]map[string]any{
		"required": {"email": "c@x.vn"},
		"unknown":  {"name": "C", "phone": "1"},
		"enum":     {"name": "C", "tier": "gold"},
		"number":   {"name": "C", "orders": "nhiều"},
	} {
		if _, _, err := s.Upsert(ctx, p, "customers", "", data, "x"); !IsInvalid(err) {
			t.Fatalf("%s: want invalid, got %v", name, err)
		}
	}
	// the other's email: taken
	bin, _, _ := s.Upsert(ctx, p, "customers", "", map[string]any{"email": "binh@x.vn"}, "x")
	if _, _, err := s.Upsert(ctx, p, "customers", bin.ID, map[string]any{"email": "an@x.vn"}, "x"); !IsInvalid(err) {
		t.Fatalf("unique taken: %v", err)
	}
	if err := s.Delete(ctx, p, "customers", a.ID, "person"); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := s.Query(ctx, p, "customers", Query{}); total != 1 {
		t.Fatalf("deleted: %d left", total)
	}
}

func TestRefsAndSchema(t *testing.T) {
	ctx := context.Background()
	s, p := setup(t)
	s.Define(ctx, p, customers)
	notes := storage.Collection{Key: "interactions", Fields: []storage.Field{
		{Name: "customer", Type: storage.FieldRef, Ref: "customers", Required: true, Indexed: true},
		{Name: "body", Type: storage.FieldText},
		{Name: "link", Type: storage.FieldURL},
	}}
	if _, err := s.Define(ctx, p, notes); err != nil {
		t.Fatal(err)
	}
	c, _, _ := s.Upsert(ctx, p, "customers", "", map[string]any{"name": "An", "email": "an@x.vn"}, "x")
	if _, _, err := s.Upsert(ctx, p, "interactions", "", map[string]any{"customer": "rec_nope", "body": "?"}, "x"); !IsInvalid(err) {
		t.Fatalf("a ref to nothing: %v", err)
	}
	if _, _, err := s.Upsert(ctx, p, "interactions", "", map[string]any{"customer": c.ID, "link": "ftp://x"}, "x"); !IsInvalid(err) {
		t.Fatalf("a bad link: %v", err)
	}
	n, _, err := s.Upsert(ctx, p, "interactions", "", map[string]any{"customer": c.ID, "body": "hỏi đổi size", "link": "https://crisp.chat/x"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, refs, _ := s.Get(ctx, p, "interactions", n.ID); refs["customer"].ID != c.ID {
		t.Fatalf("refs: %+v", refs)
	}
	for name, bad := range map[string]storage.Collection{
		"key":       {Key: "Khách", Fields: customers.Fields},
		"no fields": {Key: "x"},
		"type":      {Key: "x", Fields: []storage.Field{{Name: "a", Type: "money"}}},
		"enum":      {Key: "x", Fields: []storage.Field{{Name: "a", Type: storage.FieldEnum}}},
		"ref":       {Key: "x", Fields: []storage.Field{{Name: "a", Type: storage.FieldRef, Ref: "nope"}}},
		"dup":       {Key: "x", Fields: []storage.Field{{Name: "a", Type: "text"}, {Name: "a", Type: "text"}}},
	} {
		if _, err := s.Define(ctx, p, bad); !IsInvalid(err) {
			t.Fatalf("%s: want invalid, got %v", name, err)
		}
	}
	// unique on a field records already share: refused, the schema stays
	s.Upsert(ctx, p, "customers", "", map[string]any{"name": "An", "email": "an2@x.vn"}, "x")
	fields := append([]storage.Field(nil), customers.Fields...)
	fields[0].Unique = true
	if _, err := s.Define(ctx, p, storage.Collection{Key: "customers", Fields: fields}); !IsInvalid(err) {
		t.Fatalf("unique over duplicates: %v", err)
	}
	if got, _ := s.Collection(ctx, p, "customers"); got.Fields[0].Unique {
		t.Fatal("a refused change was kept")
	}
}

func TestRecordCap(t *testing.T) {
	ctx := context.Background()
	s, p := setup(t)
	s.maxRecords = 1
	s.Define(ctx, p, customers)
	s.Upsert(ctx, p, "customers", "", map[string]any{"name": "A"}, "x")
	if _, _, err := s.Upsert(ctx, p, "customers", "", map[string]any{"name": "B"}, "x"); !IsInvalid(err) {
		t.Fatalf("over the cap: %v", err)
	}
}

// ADR-218: every project has its links; a link saved twice (same url) is
// one record keeping its title, a kind not known becomes khac.
func TestLinks(t *testing.T) {
	ctx := context.Background()
	s, p := setup(t)
	list, err := s.List(ctx, p)
	if err != nil || len(list) != 2 || list[0].Key != LinksKey && list[1].Key != LinksKey { // and files (ADR-233)
		t.Fatalf("collections = %+v, %v", list, err)
	}
	if err := s.SaveLink(ctx, p, Link{Title: "Bản build", URL: "https://x.io/b.zip", Kind: "tai-ve"}, "agent:A"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveLink(ctx, p, Link{URL: "https://x.io/b.zip", Kind: "lạ", Note: "bản 0.2"}, "agent:A"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveLink(ctx, p, Link{URL: " "}, "agent:A"); err == nil {
		t.Fatal("an empty link was kept")
	}
	recs, total, err := s.Query(ctx, p, LinksKey, Query{})
	if err != nil || total != 1 {
		t.Fatalf("links = %+v, %v", recs, err)
	}
	if d := recs[0].Data; d["kind"] != "khac" || d["note"] != "bản 0.2" || d["title"] != "Bản build" {
		t.Fatalf("link = %+v", d)
	}
}
