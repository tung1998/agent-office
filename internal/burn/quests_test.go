package burn_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/storage"
)

// An automation's quests (ADR-132): a project with no Burn yet gets one with
// the defaults; a title an open piece has (case and spaces aside) is not
// given twice, one done is; without start the Burn stays stopped.
func TestAddQuestsDedupes(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	added, started, err := f.svc.AddQuests(ctx, f.project.ID, []burn.Quest{{Title: "Sửa lỗi đăng nhập", Detail: "trang /login"}, {Title: "Thêm test"}}, false, "auto:x")
	if err != nil || added != 2 || started {
		t.Fatalf("added %d started %v err %v", added, started, err)
	}
	b, err := f.st.Burn().Session(ctx, f.project.ID)
	if err != nil || b.State != "stopped" || b.AgentID == "" {
		t.Fatalf("session = %+v, %v", b, err)
	}
	items, _ := f.st.Burn().Items(ctx, b.ID)
	for _, it := range items {
		if it.Title == "Thêm test" { // done: the same title may come again
			it.Status = "done"
			f.st.Burn().UpdateItem(ctx, it)
		}
	}
	added, _, err = f.svc.AddQuests(ctx, f.project.ID, []burn.Quest{{Title: "  sửa LỖI   đăng nhập "}, {Title: "Thêm test"}}, false, "auto:x")
	if err != nil || added != 1 {
		t.Fatalf("again: added %d, %v", added, err)
	}
	items, _ = f.st.Burn().Items(ctx, b.ID)
	if len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
}

// With start, a stopped Burn starts on its settings once a quest was added,
// with the suggested stop time when it had none.
func TestAddQuestsStartsTheBurn(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.Start(ctx)
	if added, started, err := f.svc.AddQuests(ctx, f.project.ID, nil, true, "auto:x"); added != 0 || started || err != nil {
		t.Fatalf("no quest: %d %v %v", added, started, err)
	}
	added, started, err := f.svc.AddQuests(ctx, f.project.ID, []burn.Quest{{Title: "Dọn log"}}, true, "auto:sáng")
	if err != nil || added != 1 || !started {
		t.Fatalf("added %d started %v err %v", added, started, err)
	}
	defer f.svc.Stop(context.Background(), f.project.ID)
	b, _ := f.st.Burn().Session(ctx, f.project.ID)
	if !b.Active() || b.StartedBy != "auto:sáng" || b.EndsAt == nil || b.EndsAt.Before(time.Now().Add(7*time.Hour)) {
		t.Fatalf("session = %+v", b)
	}
	// running already: quests are added, nothing more starts
	if _, started, _ := f.svc.AddQuests(ctx, f.project.ID, []burn.Quest{{Title: "Khác"}}, true, "auto:x"); started {
		t.Fatal("started again")
	}
}

// burn_quest from any chat of the project: added, and read back with burn_quests.
func TestQuestTools(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	sc := actions.Scope{ProjectID: f.project.ID, Agent: "Thực thi"}
	if _, err := f.svc.Tool(ctx, sc, "burn_quest", burn.ToolInput{Title: "Không mô tả"}); err == nil {
		t.Fatal("a quest with no detail was added")
	}
	out, err := f.svc.Tool(ctx, sc, "burn_quest", burn.ToolInput{Title: "Viết test", Detail: "cho gói features"})
	if err != nil || !strings.Contains(out, "added to the board") {
		t.Fatalf("quest: %q, %v", out, err)
	}
	if _, err := f.svc.Tool(ctx, sc, "burn_quest", burn.ToolInput{Title: "viết  TEST", Detail: "lại"}); err == nil {
		t.Fatal("the same open quest was added twice")
	}
	out, err = f.svc.Tool(ctx, sc, "burn_quests", burn.ToolInput{})
	if err != nil || !strings.Contains(out, "[found] (default agent) Viết test") {
		t.Fatalf("quests: %q, %v", out, err)
	}
}

// ADR-197: a quest has who follows it up: the calling agent of the project,
// or one named; burn_quests filters by it; burn_assign hands one quest, or
// every open quest of an agent, to another.
func TestQuestOwners(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, a := range []storage.Agent{{ProjectID: f.project.ID, Key: "ceo", Name: "Tổng giám đốc", ModelTier: storage.TierStrong}, {ProjectID: f.project.ID, Key: "cto", Name: "Giám đốc kỹ thuật", ModelTier: storage.TierStrong}} {
		if _, err := f.st.Agents().Create(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	ceo := actions.Scope{ProjectID: f.project.ID, Agent: "Tổng giám đốc"}
	f.svc.Tool(ctx, ceo, "burn_quest", burn.ToolInput{Title: "Kế hoạch", Detail: "d"})
	f.svc.Tool(ctx, ceo, "burn_quest", burn.ToolInput{Title: "API thanh toán", Detail: "d", Owner: "cto"})
	if _, err := f.svc.Tool(ctx, ceo, "burn_quest", burn.ToolInput{Title: "X", Detail: "d", Owner: "nobody"}); err == nil {
		t.Fatal("a quest went to an agent the project does not have")
	}
	out, _ := f.svc.Tool(ctx, ceo, "burn_quests", burn.ToolInput{Owner: "ceo"})
	if !strings.Contains(out, "(ceo) Kế hoạch") || strings.Contains(out, "API thanh toán") {
		t.Fatalf("the CEO's quests: %q", out)
	}
	if out, err := f.svc.Tool(ctx, ceo, "burn_assign", burn.ToolInput{Owner: "Giám đốc kỹ thuật", From: "ceo"}); err != nil || !strings.Contains(out, "1 quest") {
		t.Fatalf("handover: %q %v", out, err)
	}
	out, _ = f.svc.Tool(ctx, ceo, "burn_quests", burn.ToolInput{Owner: "cto"})
	if !strings.Contains(out, "(cto) Kế hoạch") || !strings.Contains(out, "(cto) API thanh toán") {
		t.Fatalf("after the handover: %q", out)
	}
	if _, err := f.svc.Tool(ctx, ceo, "burn_assign", burn.ToolInput{Owner: "ceo"}); err == nil {
		t.Fatal("assigned with neither item nor from")
	}
}

// burn_drop takes a quest off the board with why (a duplicate, one a newer
// plan replaced): it is skipped, its reason the summary; one already closed,
// or no reason, is refused.
func TestQuestDrop(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	sc := actions.Scope{ProjectID: f.project.ID, Agent: "Lead"}
	f.svc.Tool(ctx, sc, "burn_quest", burn.ToolInput{Title: "W1 skin", Detail: "d"})
	b, _ := f.svc.Session(ctx, f.project.ID)
	items, _ := f.st.Burn().Items(ctx, b.ID)
	id := items[0].ID
	if _, err := f.svc.Tool(ctx, sc, "burn_drop", burn.ToolInput{Item: id}); err == nil {
		t.Fatal("dropped with no reason")
	}
	if _, err := f.svc.Tool(ctx, sc, "burn_drop", burn.ToolInput{Item: id, Reason: "thay bằng hợp đồng mới"}); err != nil {
		t.Fatal(err)
	}
	it, _ := f.st.Burn().Item(ctx, id)
	if it.Status != "skipped" || !strings.Contains(it.Summary, "thay bằng hợp đồng mới") {
		t.Fatalf("dropped = %s %q", it.Status, it.Summary)
	}
	if _, err := f.svc.Tool(ctx, sc, "burn_drop", burn.ToolInput{Item: id, Reason: "again"}); err == nil {
		t.Fatal("a closed quest dropped again")
	}
}
