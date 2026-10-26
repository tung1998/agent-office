package memory_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func open(t *testing.T) (storage.Store, string) {
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	t.Cleanup(func() { st.Close() })
	st.Migrate(context.Background())
	p, _ := st.Repos().Create(context.Background(), storage.Repo{Name: "p", Path: t.TempDir()})
	return st, p.ID
}

// Notes go into every new conversation; too long, they are compacted (the
// old ones kept, and restorable).
func TestMemory(t *testing.T) {
	ctx := context.Background()
	st, pid := open(t)
	compacted := 0
	svc := memory.New(st, func(_ context.Context, _, _ string, items []storage.Memory) ([]string, error) {
		compacted++
		return []string{"gộp " + items[0].Text}, nil
	})
	svc.Limit = 60
	if _, err := svc.Add(ctx, pid, "agt_1", "  ", "person", "a"); err == nil {
		t.Fatal("an empty note was taken")
	}
	svc.Add(ctx, pid, "agt_1", "project dùng pnpm", "person", "human:a")
	if b := svc.Block(ctx, pid, "agt_1"); !strings.Contains(b, "- project dùng pnpm") {
		t.Fatalf("block = %q", b)
	}
	if svc.Block(ctx, pid, "agt_2") != "" {
		t.Fatal("another agent has a block")
	}
	svc.Add(ctx, pid, "agt_1", "không sửa file generated, chạy pnpm test trước khi báo xong", "agent", "Trợ lý")
	if compacted != 1 {
		t.Fatalf("compacted %d times", compacted)
	}
	list, _ := st.Memories().List(ctx, pid, "agt_1")
	if len(list) != 1 || list[0].Text != "gộp project dùng pnpm" || list[0].Source != "compact" {
		t.Fatalf("after compaction = %+v", list)
	}
	revs, _ := st.Memories().Revisions(ctx, pid, "agt_1")
	if len(revs) != 1 || len(revs[0].Items) != 2 {
		t.Fatalf("revisions = %+v", revs)
	}
	if err := svc.Restore(ctx, revs[0].ID, "human:a"); err != nil {
		t.Fatal(err)
	}
	if list, _ = st.Memories().List(ctx, pid, "agt_1"); len(list) != 2 {
		t.Fatalf("restored = %+v", list)
	}
	if revs, _ = st.Memories().Revisions(ctx, pid, "agt_1"); len(revs) != 2 { // what it was before the restore, too
		t.Fatalf("revisions after restore = %d", len(revs))
	}
	svc.SetAuto(ctx, pid, true)
	if !svc.Auto(ctx, pid) {
		t.Fatal("auto not kept")
	}
}

// ADR-215: notes are kept right, not only added to: Revise drops, rewrites
// and adds in one go (what they were kept as a revision); recall shows ids.
func TestRevise(t *testing.T) {
	ctx := context.Background()
	st, pid := open(t)
	svc := memory.New(st, nil)
	old, _ := svc.Add(ctx, pid, "agt_1", "repo cũ ở /tmp/a, đã xoá ngày 25/10", "agent", "a")
	keep, _ := svc.Add(ctx, pid, "agt_1", "chạy go test ./... trước khi báo xong", "agent", "a")
	other, _ := svc.Add(ctx, pid, "agt_2", "ghi nhớ của agent khác", "agent", "a")
	n, err := svc.Revise(ctx, pid, "agt_1", []storage.Memory{{Text: "mẫu nằm ở templates/packs/startup", Topic: "Company pack", Summary: "mẫu công ty"}},
		map[string]string{keep.ID: "chạy go test ./... và gofmt -l . trước khi báo xong"}, []string{old.ID, other.ID}, "dọn")
	if err != nil || n != 1 {
		t.Fatalf("revise = %d %v", n, err)
	}
	list, _ := st.Memories().List(ctx, pid, "agt_1")
	if len(list) != 2 || list[0].Text != "chạy go test ./... và gofmt -l . trước khi báo xong" || list[1].Topic != "company-pack" {
		t.Fatalf("notes = %+v", list)
	}
	if x, _ := st.Memories().List(ctx, pid, "agt_2"); len(x) != 1 {
		t.Fatal("another agent's note was dropped")
	}
	if revs, _ := st.Memories().Revisions(ctx, pid, "agt_1"); len(revs) != 1 || len(revs[0].Items) != 2 {
		t.Fatalf("revisions = %+v", revs)
	}
	out, _ := memory.Recall(ctx, st, pid, "agt_1", "core", "")
	if !strings.Contains(out, "["+keep.ID+"]") || strings.Contains(out, "templates/packs") {
		t.Fatalf("recall core = %q", out)
	}
}

// ADR-215: after a turn, a review keeps the notes up to date: within its
// bounds, only the agent's own notes, nothing when the project keeps notes
// by hand.
func TestReview(t *testing.T) {
	ctx := context.Background()
	st, pid := open(t)
	svc := memory.New(st, nil)
	stale, _ := svc.Add(ctx, pid, "agt_1", "đồng bộ public 15/10: private f30c1b6 → public 6fec5da", "agent", "a")
	other, _ := svc.Add(ctx, pid, "agt_2", "ghi nhớ của agent khác", "agent", "a")
	var asked string
	svc.SetReviewer(func(_ context.Context, _, _ string, notes []storage.Memory, q, _ string) ([]memory.Op, error) {
		asked = q
		return []memory.Op{{Op: "drop", ID: stale.ID}, {Op: "drop", ID: other.ID},
			{Op: "add", Text: "repo là bản private, không đồng bộ sang public nữa"}, {Op: "add", Text: "điều thứ hai đủ dài để ghi lại"}, {Op: "add", Text: "điều thứ ba quá giới hạn mỗi lượt"}}, nil
	})
	if err := svc.Review(ctx, pid, "agt_1", "repo đã private chưa?", "rồi"); err != nil || asked != "repo đã private chưa?" {
		t.Fatalf("review = %v (asked %q)", err, asked)
	}
	list, _ := st.Memories().List(ctx, pid, "agt_1")
	if len(list) != memory.ReviewAdds || list[0].Source != "review" || strings.Contains(list[0].Text, "f30c1b6") {
		t.Fatalf("notes = %+v", list)
	}
	if x, _ := st.Memories().List(ctx, pid, "agt_2"); len(x) != 1 {
		t.Fatal("another agent's note was dropped")
	}
	svc.SetAuto(ctx, pid, false)
	asked = ""
	svc.Review(ctx, pid, "agt_1", "lượt khác", "x")
	if asked != "" {
		t.Fatal("reviewed while the project keeps notes by hand")
	}
}

// ADR-218: a review's links go to the project's links, at most
// ReviewLinks a turn, and change no note.
func TestReviewLinks(t *testing.T) {
	ctx := context.Background()
	st, pid := open(t)
	svc := memory.New(st, nil)
	var kept []memory.Op
	svc.SetLinkSaver(func(_ context.Context, _, _ string, o memory.Op) error { kept = append(kept, o); return nil })
	svc.SetReviewer(func(context.Context, string, string, []storage.Memory, string, string) ([]memory.Op, error) {
		return []memory.Op{{Op: "link", Topic: "tai-ve", Summary: "Bản build", Text: "https://x.io/1.zip"},
			{Op: "link", Text: "https://x.io/2"}, {Op: "link", Text: "https://x.io/3"}, {Op: "link", Text: "https://x.io/4"}}, nil
	})
	if err := svc.Review(ctx, pid, "agt_1", "link tải đâu?", "đây"); err != nil {
		t.Fatal(err)
	}
	if len(kept) != memory.ReviewLinks || kept[0].Text != "https://x.io/1.zip" {
		t.Fatalf("links = %+v", kept)
	}
	if list, _ := st.Memories().List(ctx, pid, "agt_1"); len(list) != 0 {
		t.Fatalf("notes = %+v", list)
	}
}
