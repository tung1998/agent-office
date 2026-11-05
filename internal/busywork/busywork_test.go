package busywork_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/busywork"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

type turns int

func (t turns) ActiveTurns() int { return int(t) }

func open(t *testing.T) *sqlite.Store {
	t.Helper()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// Every kind of work a restart cuts off is counted, with a name and a link
// (ADR-255): before, only the answers being written were.
func TestCountEveryKind(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/p"})
	c := busywork.Counter{Store: st, Chat: turns(0)}
	if r := c.Count(ctx); r.Total() != 0 || r.Summary() != "" {
		t.Fatalf("empty office = %+v", r)
	}

	if _, err := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: "cnv_w", WorkflowKey: "daily-upgrade", WorkflowName: "Nâng cấp hằng ngày", Status: storage.RunRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: "cnv_d", WorkflowKey: "done", Status: storage.RunDone}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "script", Origin: "automation", OriginID: "aut_1", Status: "running", Title: "Sao lưu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", ConversationID: "cnv_c", Status: "running", Title: "Sửa lỗi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", Status: "done", Title: "xong"}); err != nil {
		t.Fatal(err)
	}
	task, err := st.Tasks().Create(ctx, storage.Task{ProjectID: p.ID, Title: "Viết docs", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID, ModelTier: "fast", State: "running", RunBranch: "burn/x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "Sửa test", Status: "doing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "chưa làm", Status: "queued"}); err != nil {
		t.Fatal(err)
	}

	c.Chat = turns(1)
	r := c.Count(ctx)
	if r.Chats != 1 || len(r.ChatItems) != 1 || r.ChatItems[0].Link != "/projects/"+p.ID+"?tab=chat&c=cnv_c" {
		t.Errorf("chats = %d %+v", r.Chats, r.ChatItems)
	}
	if len(r.Workflows) != 1 || r.Workflows[0].Name != "Nâng cấp hằng ngày" || !strings.Contains(r.Workflows[0].Link, "c=cnv_w") {
		t.Errorf("workflows = %+v", r.Workflows)
	}
	if len(r.Automations) != 1 || r.Automations[0].Link != "/projects/"+p.ID+"/automations/aut_1" {
		t.Errorf("automations = %+v", r.Automations)
	}
	if len(r.Tasks) != 1 || r.Tasks[0].ID != task.ID {
		t.Errorf("tasks = %+v", r.Tasks)
	}
	if len(r.Burn) != 1 || r.Burn[0].Name != "Sửa test" {
		t.Errorf("burn = %+v", r.Burn)
	}
	if r.Total() != 5 {
		t.Errorf("total = %d", r.Total())
	}
	if s := r.Summary(); !strings.Contains(s, "Nâng cấp hằng ngày") || !strings.Contains(s, "1 lượt chat") {
		t.Errorf("summary = %q", s)
	}
}
