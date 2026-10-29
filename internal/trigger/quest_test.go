package trigger_test

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// @@quest lines (ADR-132): a title, a detail after " :: ", each title once
// (case and spaces aside); other lines and look-alikes are not quests.
func TestQuestLines(t *testing.T) {
	out := strings.Join([]string{
		"checked 12 issues",
		"@@quest Sửa lỗi đăng nhập :: trang /login trả 500",
		"  @@quest   Thêm   test cho API  ",
		"@@quest: Dọn log :: a :: b",
		"@@quest sửa LỖI đăng   nhập :: lặp lại",
		"@@questions are not quests",
		"@@quest ",
		"@@agent: nhìn giúp",
	}, "\n")
	want := []trigger.Quest{
		{Title: "Sửa lỗi đăng nhập", Detail: "trang /login trả 500"},
		{Title: "Thêm test cho API"},
		{Title: "Dọn log", Detail: "a :: b"},
	}
	if got := trigger.Quests(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("quests = %#v", got)
	}
	if long := trigger.Quests("@@quest " + strings.Repeat("x", 300)); len(long) != 1 || len([]rune(long[0].Title)) != 160 {
		t.Fatalf("a long title: %v", long)
	}
}

type questSink struct {
	mu     sync.Mutex
	got    []trigger.Quest
	starts []bool
}

func (s *questSink) give(_ context.Context, _ string, qs []trigger.Quest, start bool, _ string) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got, s.starts = append(s.got, qs...), append(s.starts, start)
	return len(qs) - 1, start, nil // the first one was open already
}

// A script's quests go to the Burn; the job's output says how many, and the
// answer leaves the @@quest lines out. burn_start starts it on a run by hand
// or a schedule, never on a webhook's.
func TestScriptGivesQuests(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	sink := &questSink{}
	r := trigger.New(st, &recExec{})
	r.SetQuests(sink.give)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "triage", Source: "schedule", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{EveryMinutes: 5},
		Script: storage.AutomationScript{Lang: "bash", Body: "echo xong\necho '@@quest Một'\necho '@@quest Hai :: chi tiết'", TimeoutS: 30, BurnStart: true}})
	runOnce(t, r, st, a, "")
	if len(sink.got) != 2 || sink.got[1] != (trigger.Quest{Title: "Hai", Detail: "chi tiết"}) || !sink.starts[0] {
		t.Fatalf("quests = %+v starts %v", sink.got, sink.starts)
	}
	s, _ := jobsOf(t, st, a)
	if s.Status != "done" || !strings.Contains(s.Output, "Đã thêm 1 quest cho Burn (bỏ qua 1 đã có); Burn đã bật") || !strings.Contains(s.Output, "@@quest Một") {
		t.Fatalf("output = %q", s.Output)
	}
	// a webhook's run: quests, but no start
	if _, _, err := r.Enqueue(ctx, a, "webhook", `{"x":1}`, "", ""); err != nil {
		t.Fatal(err)
	}
	r.StartReady(ctx, time.Now().UTC())
	r.Wait()
	if len(sink.starts) != 2 || sink.starts[1] {
		t.Fatalf("starts = %v", sink.starts)
	}
}

// @@workflow lines (ADR-237): a script opens a workflow of the project, its
// input after the key, each key once; a webhook's script opens none.
func TestScriptStartsWorkflows(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	if _, err := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a", Name: "A", ModelTier: "fast"}); err != nil {
		t.Fatal(err)
	}
	if got := trigger.Flows("x\n@@workflow kiem-tra deploy 16h\n@@workflow: kiem-tra again\n@@workflows no\n@@workflow b@d"); len(got) != 1 || got[0] != (trigger.FlowCall{Key: "kiem-tra", Input: "deploy 16h"}) {
		t.Fatalf("flows = %+v", got)
	}
	ex := &recExec{returnCnv: "cnv_w"}
	r := trigger.New(st, ex)
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "deploy", Source: "schedule", Action: "script", Enabled: true,
		Config: storage.AutomationConfig{EveryMinutes: 10},
		Script: storage.AutomationScript{Lang: "bash", Body: "echo alarm\necho '@@workflow kiem-tra deploy 16h'", TimeoutS: 30}})
	runOnce(t, r, st, a, "")
	if len(ex.prompts) != 1 || ex.prompts[0] != "#kiem-tra deploy 16h" {
		t.Fatalf("prompts = %q", ex.prompts)
	}
	s, kids := jobsOf(t, st, a)
	if s.Status != "done" || !strings.Contains(s.Output, "Đã mở quy trình #kiem-tra") {
		t.Fatalf("output = %q", s.Output)
	}
	if len(kids) != 1 || kids[0].Status != "done" || kids[0].ParentJobID != s.ID || kids[0].ConversationID != "cnv_w" {
		t.Fatalf("children = %+v", kids)
	}
	if _, _, err := r.Enqueue(ctx, a, "webhook", `{"x":1}`, "", ""); err != nil {
		t.Fatal(err)
	}
	r.StartReady(ctx, time.Now().UTC())
	r.Wait()
	if len(ex.prompts) != 1 {
		t.Fatalf("a webhook's script opened a workflow: %q", ex.prompts)
	}
}
