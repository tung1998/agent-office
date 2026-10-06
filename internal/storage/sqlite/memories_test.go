package sqlite_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// An agent's notes: added, edited, removed; a snapshot of them kept, and put back.
func TestMemories(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	mem := st.Memories()
	a, err := mem.Create(ctx, storage.Memory{ProjectID: p.ID, AgentID: "agt_1", Text: "project dùng pnpm", Source: "person", CreatedBy: "human:a@x.io"})
	if err != nil || a.ID == "" {
		t.Fatal(a, err)
	}
	b, _ := mem.Create(ctx, storage.Memory{ProjectID: p.ID, AgentID: "agt_1", Text: "không sửa file generated", Source: "agent"})
	mem.Create(ctx, storage.Memory{ProjectID: p.ID, AgentID: "agt_2", Text: "của agent khác", Source: "person"})
	if err := mem.Update(ctx, a.ID, "project dùng pnpm 9"); err != nil {
		t.Fatal(err)
	}
	list, _ := mem.List(ctx, p.ID, "agt_1")
	if len(list) != 2 || list[0].Text != "project dùng pnpm 9" || list[1].ID != b.ID {
		t.Fatalf("list = %+v", list)
	}
	rev, err := mem.SaveRevision(ctx, storage.MemoryRevision{ProjectID: p.ID, AgentID: "agt_1", Items: list, Reason: "rút gọn"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Replace(ctx, p.ID, "agt_1", []storage.Memory{{Text: "gộp: pnpm 9, không sửa generated", Source: "compact"}}); err != nil {
		t.Fatal(err)
	}
	if list, _ = mem.List(ctx, p.ID, "agt_1"); len(list) != 1 || list[0].Source != "compact" {
		t.Fatalf("replaced = %+v", list)
	}
	revs, _ := mem.Revisions(ctx, p.ID, "agt_1")
	if len(revs) != 1 || revs[0].ID != rev.ID || len(revs[0].Items) != 2 {
		t.Fatalf("revisions = %+v", revs)
	}
	got, err := mem.GetRevision(ctx, rev.ID)
	if err != nil || len(got.Items) != 2 {
		t.Fatalf("revision = %+v %v", got, err)
	}
	if err := mem.Delete(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = mem.List(ctx, p.ID, "agt_1"); len(list) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
	if other, _ := mem.List(ctx, p.ID, "agt_2"); len(other) != 1 {
		t.Fatal("another agent's notes were touched")
	}
}
