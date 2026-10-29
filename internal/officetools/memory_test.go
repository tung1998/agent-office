package officetools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/team"
)

// ADR-134: remember puts a note under a topic; only the topic's line is in
// the prompt, recall reads its notes, by topic or by words.
func TestRememberTopicAndRecall(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	team.NewService(st, nil).AddFromLibrary(ctx, p.ID, team.StarterAgent)
	agents, _ := st.Agents().List(ctx, p.ID)
	mem := memory.New(st, nil)
	mem.SetAuto(ctx, p.ID, true)
	acts := actions.New(st, nil)
	acts.SetMemory(mem)
	tb := New(st, nil, acts)
	sc := Scope{ProjectID: p.ID, RunRef: "r1", Agent: agents[0].Name, Level: perm.Propose}

	out, isErr := tb.Call(ctx, sc, "remember", []byte(`{"note":"Hoàn tiền PayPal chạy qua job refund_sync","topic":"Thanh toán","summary":"hoàn tiền, webhook PayPal/Stripe","reason":"mất 10 phút tìm"}`))
	if isErr || !strings.Contains(out, "topic thanh-toan") {
		t.Fatalf("remember = %v %s", isErr, out)
	}
	sc.RunRef = "r2"
	tb.Call(ctx, sc, "remember", []byte(`{"note":"Repo dùng pnpm, không dùng npm","reason":"lockfile"}`))
	list, _ := st.Memories().List(ctx, p.ID, agents[0].ID)
	if len(list) != 2 || list[0].Topic != "thanh-toan" || list[0].Summary != "hoàn tiền, webhook PayPal/Stripe" || list[1].Topic != "" {
		t.Fatalf("notes = %+v", list)
	}
	b := mem.Block(ctx, p.ID, agents[0].ID)
	if !strings.Contains(b, "- Repo dùng pnpm") || !strings.Contains(b, "- thanh-toan: hoàn tiền, webhook PayPal/Stripe") || strings.Contains(b, "refund_sync") {
		t.Fatalf("block = %s", b)
	}

	if out, isErr = tb.Call(ctx, sc, "recall", []byte(`{"topic":"thanh-toan"}`)); isErr || !strings.Contains(out, "refund_sync") || strings.Contains(out, "pnpm") {
		t.Fatalf("recall topic = %v %s", isErr, out)
	}
	if out, isErr = tb.Call(ctx, sc, "recall", []byte(`{"query":"hoan tien paypal"}`)); isErr || !strings.Contains(out, "[thanh-toan] Hoàn tiền") {
		t.Fatalf("recall query = %v %s", isErr, out)
	}
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{"query":"pnpm"}`)); !strings.Contains(out, "[core] Repo dùng pnpm") {
		t.Fatalf("recall core = %s", out)
	}
	// a word no note has does not empty the answer: the notes with most words, best first
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{"query":"hoan tien paypal loi"}`)); !strings.Contains(out, "most of them") || !strings.Contains(out, "[thanh-toan] Hoàn tiền") || strings.Contains(out, "pnpm") {
		t.Fatalf("recall partial = %s", out)
	}
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{"query":"pnpm stripe"}`)); !strings.Contains(out, "pnpm") || !strings.Contains(out, "Hoàn tiền") {
		t.Fatalf("recall half = %s", out)
	}
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{"query":"pnpm lockfile deploy docker"}`)); !strings.Contains(out, "No notes match") {
		t.Fatalf("recall under half = %s", out)
	}
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{}`)); !strings.Contains(out, "- thanh-toan: hoàn tiền") {
		t.Fatalf("recall topics = %s", out)
	}
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{"topic":"deploy"}`)); !strings.Contains(out, "No notes match. Topics: thanh-toan") {
		t.Fatalf("recall unknown = %s", out)
	}
	// the project's wiki (ADR-220) is searched with the notes
	os.MkdirAll(filepath.Join(p.Path, "docs", "wiki"), 0o755)
	os.WriteFile(filepath.Join(p.Path, "docs", "wiki", "quyet-dinh.md"), []byte("# Quyết định\n## Hoàn tiền\nHoàn tiền trong 7 ngày, qua PayPal.\n"), 0o644)
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{"query":"hoan tien paypal"}`)); !strings.Contains(out, "[thanh-toan] Hoàn tiền") || !strings.Contains(out, "From the wiki") || !strings.Contains(out, "docs/wiki/quyet-dinh.md › Hoàn tiền: Hoàn tiền trong 7 ngày") {
		t.Fatalf("recall wiki = %s", out)
	}
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{"topic":"thanh-toan"}`)); strings.Contains(out, "From the wiki") {
		t.Fatalf("wiki without a query = %s", out)
	}
	if !tb.Has(sc, "recall") {
		t.Fatal("recall not offered where remember is")
	}

	// with Cognee (ADR-195): notes by meaning follow, never in place of keywords
	tb.SetRelated(func(_ context.Context, pid, q, skip string, _ int) ([]string, error) {
		if !strings.Contains(skip, "Repo dùng pnpm") {
			return nil, errors.New("keyword hits not passed")
		}
		return []string{"[Ops · deploy] build bằng pnpm trước khi ship"}, nil
	})
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{"query":"pnpm"}`)); !strings.Contains(out, "[core] Repo dùng pnpm") || !strings.Contains(out, "Related by meaning") || !strings.Contains(out, "[Ops · deploy]") {
		t.Fatalf("recall related = %s", out)
	}
	if out, _ = tb.Call(ctx, sc, "recall", []byte(`{"topic":"thanh-toan"}`)); strings.Contains(out, "Related by meaning") {
		t.Fatalf("related without a query = %s", out)
	}
	tb.SetRelated(func(context.Context, string, string, string, int) ([]string, error) { return nil, errors.New("down") })
	if out, isErr = tb.Call(ctx, sc, "recall", []byte(`{"query":"pnpm"}`)); isErr || !strings.Contains(out, "[core] Repo dùng pnpm") || strings.Contains(out, "Related") {
		t.Fatalf("recall with Cognee down = %v %s", isErr, out)
	}
}

// ADR-215: an agent keeps its notes right: remember with replaces takes the
// place of the notes it corrects, forget drops wrong or outdated ones (its
// own only), and recall shows the ids to name them by.
func TestReplaceAndForget(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	team.NewService(st, nil).AddFromLibrary(ctx, p.ID, team.StarterAgent)
	agents, _ := st.Agents().List(ctx, p.ID)
	mem := memory.New(st, nil)
	acts := actions.New(st, nil)
	acts.SetMemory(mem)
	tb := New(st, nil, acts)
	sc := Scope{ProjectID: p.ID, RunRef: "r1", Agent: agents[0].Name, Level: perm.Propose}
	old, _ := mem.Add(ctx, p.ID, agents[0].ID, "Repo dùng npm, chạy npm test", "agent", "a")
	gone, _ := mem.Add(ctx, p.ID, agents[0].ID, "Repo cũ ở /tmp/agent-company, giữ mẫu công ty", "agent", "a")
	other, _ := mem.Add(ctx, p.ID, "agt_khac", "Ghi nhớ của một agent khác trong project", "agent", "a")

	if out, _ := tb.Call(ctx, sc, "recall", []byte(`{"topic":"core"}`)); !strings.Contains(out, "["+old.ID+"] Repo dùng npm") {
		t.Fatalf("recall core = %s", out)
	}
	// the same words again: refused, unless it replaces the note
	if out, isErr := tb.Call(ctx, sc, "remember", []byte(`{"note":"Repo dùng npm, chạy npm test","reason":"lockfile"}`)); !isErr || !strings.Contains(out, old.ID) {
		t.Fatalf("duplicate = %v %s", isErr, out)
	}
	if out, isErr := tb.Call(ctx, sc, "remember", []byte(`{"note":"Repo dùng pnpm (không phải npm), chạy pnpm test","reason":"lockfile đổi","replaces":["`+old.ID+`"]}`)); isErr || !strings.Contains(out, "Written") {
		t.Fatalf("replace = %v %s", isErr, out)
	}
	sc.RunRef = "r2"
	if out, isErr := tb.Call(ctx, sc, "forget", []byte(`{"ids":["`+gone.ID+`","`+other.ID+`"],"reason":"thư mục đã xoá"}`)); isErr || !strings.Contains(out, "Dropped") {
		t.Fatalf("forget = %v %s", isErr, out)
	}
	list, _ := st.Memories().List(ctx, p.ID, agents[0].ID)
	if len(list) != 1 || !strings.Contains(list[0].Text, "pnpm") {
		t.Fatalf("notes = %+v", list)
	}
	if x, _ := st.Memories().List(ctx, p.ID, "agt_khac"); len(x) != 1 {
		t.Fatal("another agent's note was dropped")
	}
	if out, isErr := tb.Call(ctx, sc, "forget", []byte(`{"ids":["mem_none"],"reason":"x"}`)); !isErr {
		t.Fatalf("forget of nothing = %s", out)
	}
	if revs, _ := st.Memories().Revisions(ctx, p.ID, agents[0].ID); len(revs) != 2 {
		t.Fatalf("revisions = %d, want one per change", len(revs))
	}
}
