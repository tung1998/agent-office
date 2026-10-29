package collections

import (
	"context"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/officetools"
)

// ADR-172: the data_* tools are there in a project (its links always are,
// ADR-218); writing needs "propose".
func TestTools(t *testing.T) {
	ctx := context.Background()
	s, p := setup(t)
	tools := NewTools(s, nil)
	read, write := officetools.Scope{ProjectID: p, Level: "read"}, officetools.Scope{ProjectID: p, Level: "propose", Agent: "cskh"}
	if tools.Tools(officetools.Scope{Level: "read"}) != nil {
		t.Fatal("no project: no tools")
	}
	s.Define(ctx, p, customers)
	names := func(sc officetools.Scope) (out []string) {
		for _, x := range tools.Tools(sc) {
			out = append(out, x.Name)
		}
		return out
	}
	if got := strings.Join(names(read), ","); got != "data_collections,data_query,data_get" {
		t.Fatalf("read: %s", got)
	}
	if got := names(write); len(got) != 5 || got[3] != "data_upsert" || got[4] != "file_keep" {
		t.Fatalf("propose: %v", got)
	}
	out, isErr := tools.Call(ctx, write, "data_upsert", []byte(`{"collection":"customers","data":{"name":"An","email":"an@x.vn","tier":"vip"}}`))
	if isErr || !strings.Contains(out, `"created":true`) || !strings.Contains(out, `"updated_by":"agent:cskh"`) {
		t.Fatalf("upsert: %s", out)
	}
	if _, isErr := tools.Call(ctx, read, "data_upsert", []byte(`{"collection":"customers","data":{"name":"B"}}`)); !isErr {
		t.Fatal("read level wrote")
	}
	out, isErr = tools.Call(ctx, read, "data_query", []byte(`{"collection":"customers","where":[{"field":"tier","value":"vip"}]}`))
	if isErr || !strings.Contains(out, `"total":1`) {
		t.Fatalf("query: %s", out)
	}
	if out, _ := tools.Call(ctx, read, "data_collections", nil); !strings.Contains(out, "email: text (unique)") {
		t.Fatalf("collections: %s", out)
	}
	if out, isErr := tools.Call(ctx, read, "data_query", []byte(`{"collection":"nope"}`)); !isErr || !strings.Contains(out, "nope") {
		t.Fatalf("unknown collection: %s", out)
	}
}
