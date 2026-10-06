package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func openStore(t *testing.T) (storage.Store, storage.Repo) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	return st, p
}

func TestJobsQueueAndFinish(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	now := time.Now().UTC()
	j, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "task", Origin: "automation", OriginID: "aut_1", Trigger: "webhook", Status: "pending", DedupeKey: "d1", NextAttemptAt: &now})
	if err != nil || j.ID == "" {
		t.Fatalf("create = %+v %v", j, err)
	}
	if _, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "task", Origin: "automation", OriginID: "aut_1", Status: "pending", DedupeKey: "d1"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate dedupe err = %v", err)
	}
	// busy origins are skipped; claimed jobs become running
	if got, _ := st.Jobs().Claim(ctx, now.Add(time.Second), 2, []string{"aut_1"}); len(got) != 0 {
		t.Fatalf("claimed a busy origin: %+v", got)
	}
	got, err := st.Jobs().Claim(ctx, now.Add(time.Second), 2, nil)
	if err != nil || len(got) != 1 || got[0].Status != "running" || got[0].StartedAt == nil {
		t.Fatalf("claim = %+v %v", got, err)
	}
	cost := 0.25
	st.Runs().Create(ctx, storage.Run{Kind: "task", ProjectID: p.ID, JobID: j.ID, Status: "ok", CostUSD: &cost, InputTokens: 10, OutputTokens: 4, DurationMS: 1500})
	st.Runs().Create(ctx, storage.Run{Kind: "task", ProjectID: p.ID, JobID: j.ID, Status: "ok", CostUSD: &cost, InputTokens: 5, OutputTokens: 1, DurationMS: 500})
	done, err := st.Jobs().Finish(ctx, j.ID, "done", "", "", now.Add(3*time.Second))
	if err != nil || done.CostUSD != 0.5 || done.InputTokens != 15 || done.DurationMS < 2000 {
		t.Fatalf("finish = %+v %v", done, err)
	}
	// restart: running jobs fail
	st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", Status: "running", StartedAt: &now})
	if n, _ := st.Jobs().FailRunning(ctx, "restart", "office khởi động lại", now); n != 1 {
		t.Fatalf("fail running = %d", n)
	}
	list, _ := st.Jobs().List(ctx, storage.JobFilter{ProjectID: p.ID, Status: "failed"})
	if len(list) != 1 || list[0].ErrorCode != "restart" {
		t.Fatalf("failed list = %+v", list)
	}
	stats, _ := st.Jobs().Stats(ctx, storage.JobFilter{ProjectID: p.ID}, "kind")
	if len(stats) != 2 {
		t.Fatalf("stats by kind = %+v", stats)
	}
}

func TestAutomationsDue(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	past := time.Now().UTC().Add(-time.Minute)
	a, err := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "sáng", Source: "schedule", Action: "task", Enabled: true,
		Config: storage.AutomationConfig{Cron: "0 8 * * 1-5", Timezone: "Asia/Ho_Chi_Minh"}, NextRunAt: &past})
	if err != nil {
		t.Fatal(err)
	}
	st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "hook", Source: "webhook", Action: "chat", Enabled: true, NextRunAt: &past})
	due, _ := st.Automations().Due(ctx, time.Now().UTC())
	if len(due) != 1 || due[0].ID != a.ID || due[0].Config.Timezone != "Asia/Ho_Chi_Minh" {
		t.Fatalf("due = %+v", due)
	}
	a.Enabled = false
	st.Automations().Update(ctx, a)
	if due, _ := st.Automations().Due(ctx, time.Now().UTC()); len(due) != 0 {
		t.Fatalf("disabled is due: %+v", due)
	}
}

func TestFailRunningCountsCostAndEndsTasks(t *testing.T) { // I6
	ctx := context.Background()
	st, p := openStore(t)
	now := time.Now().UTC()
	j, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "task", Origin: "automation", OriginID: "a", Status: "running", StartedAt: &now})
	cost := 0.4
	st.Runs().Create(ctx, storage.Run{Kind: "task", ProjectID: p.ID, JobID: j.ID, Status: "ok", CostUSD: &cost, InputTokens: 7})
	st.Jobs().FailRunning(ctx, "restart", "restart", now)
	if got, _ := st.Jobs().Get(ctx, j.ID); got.CostUSD != 0.4 || got.InputTokens != 7 {
		t.Fatalf("failed job = %+v", got)
	}
	task, _ := st.Tasks().Create(ctx, storage.Task{ProjectID: p.ID, Title: "x", Goal: "x", Mode: "single", Status: "running"})
	if n, err := st.Tasks().FailRunning(ctx, "office khởi động lại", now); err != nil || n != 1 {
		t.Fatalf("fail tasks = %d %v", n, err)
	}
	if got, _ := st.Tasks().Get(ctx, task.ID); got.Status != "failed" || got.FinishedAt == nil {
		t.Fatalf("task = %+v", got)
	}
}

func TestDebounceOnlyTouchesPending(t *testing.T) { // I2
	ctx := context.Background()
	st, p := openStore(t)
	now := time.Now().UTC()
	j, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: "a", Status: "pending", DebounceKey: "k", NextAttemptAt: &now})
	st.Jobs().Claim(ctx, now.Add(time.Second), 1, nil) // the runner took it
	if ok, err := st.Jobs().Debounce(ctx, j.ID, `{"n":2}`, now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("debounced a running job: %v %v", ok, err)
	}
	if got, _ := st.Jobs().Get(ctx, j.ID); got.Status != "running" || got.Payload == `{"n":2}` {
		t.Fatalf("job = %+v", got)
	}
}

func TestScriptJobsAndAutomations(t *testing.T) { // ADR-041
	ctx := context.Background()
	st, p := openStore(t)
	j, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "script", Origin: "automation", OriginID: "a", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Jobs().SetOutput(ctx, j.ID, "hello\n", 3); err != nil {
		t.Fatal(err)
	}
	child, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: "a", Trigger: "escalate", Status: "pending", ParentJobID: j.ID})
	got, _ := st.Jobs().Get(ctx, j.ID)
	if got.Output != "hello\n" || got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("script job = %+v", got)
	}
	if c, _ := st.Jobs().Get(ctx, child.ID); c.ParentJobID != j.ID {
		t.Fatalf("child = %+v", c)
	}
	if list, _ := st.Jobs().List(ctx, storage.JobFilter{Kind: "script"}); len(list) != 1 {
		t.Fatalf("script jobs = %d", len(list))
	}
	a, err := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "log", Source: "schedule", Action: "script", Enabled: true,
		Script:   storage.AutomationScript{Lang: "bash", Body: "echo ok", TimeoutS: 60},
		Escalate: storage.AutomationEscalate{When: "failure", Action: "chat", Prompt: "Lỗi: {{output}}"}})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := st.Automations().Get(ctx, a.ID); b.Script.Body != "echo ok" || b.Escalate.When != "failure" || b.Action != "script" {
		t.Fatalf("automation = %+v", b)
	}
}

func TestAutomationConversations(t *testing.T) { // ADR-042
	ctx := context.Background()
	st, p := openStore(t)
	plain, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "chat"})
	draft, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "tự động", Purpose: "automation"})
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := st.Chat().ListConversations(ctx, p.ID, 50); len(list) != 1 || list[0].ID != plain.ID {
		t.Fatalf("list = %+v", list)
	}
	if err := st.Chat().LinkAutomation(ctx, draft.ID, "aut_1"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Chat().AutomationConversation(ctx, "aut_1")
	if err != nil || got.ID != draft.ID || got.Purpose != "automation" || got.AutomationID != "aut_1" {
		t.Fatalf("automation conversation = %+v %v", got, err)
	}
	m, _ := st.Chat().AddMessage(ctx, storage.Message{ConversationID: draft.ID, Role: "user", Content: "viết script", Context: `{"page":"automation"}`})
	msgs, _ := st.Chat().ListMessages(ctx, draft.ID)
	if len(msgs) != 1 || msgs[0].ID != m.ID || msgs[0].Context != `{"page":"automation"}` {
		t.Fatalf("messages = %+v", msgs)
	}
}

func TestOneBuildingChatPerAutomation(t *testing.T) { // review I2
	ctx := context.Background()
	st, p := openStore(t)
	a, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Purpose: "automation"})
	b, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Purpose: "automation"})
	if err := st.Chat().LinkAutomation(ctx, a.ID, "aut_1"); err != nil {
		t.Fatal(err)
	}
	if err := st.Chat().LinkAutomation(ctx, b.ID, "aut_1"); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("second chat linked: %v", err)
	}
}

// A bot's jobs are those of all its commands (and the messages none took).
func TestJobsByOriginIDs(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p"})
	for _, o := range []string{"aut_1", "aut_2", "chn_1", "aut_3"} {
		st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: o, Trigger: "discord", Status: "done"})
	}
	list, err := st.Jobs().List(ctx, storage.JobFilter{OriginIDs: []string{"aut_1", "aut_2", "chn_1"}})
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %d %v", len(list), err)
	}
}

// Jobs gathered by the work they belong to: a chat, a task, an automation.
func TestJobGroups(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	mk := func(j storage.Job) {
		j.ProjectID = p.ID
		if _, err := st.Jobs().Create(ctx, j); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	cost := func(v float64) {}
	_ = cost
	mk(storage.Job{Kind: "chat_turn", Origin: "user", ConversationID: "cnv_1", Status: "done", Title: "hỏi 1"})
	mk(storage.Job{Kind: "chat_turn", Origin: "user", ConversationID: "cnv_1", Status: "failed", Title: "hỏi 2"})
	mk(storage.Job{Kind: "script", Origin: "automation", OriginID: "aut_1", Status: "done", Title: "chạy 1"})
	mk(storage.Job{Kind: "script", Origin: "automation", OriginID: "aut_1", Status: "done", Title: "chạy 2"})
	mk(storage.Job{Kind: "task", Origin: "user", TaskID: "tsk_1", Status: "running", Title: "việc"})
	gs, err := st.Jobs().Groups(ctx, storage.JobFilter{ProjectID: p.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 4 {
		t.Fatalf("groups = %+v", gs)
	}
	if gs[0].TaskID != "tsk_1" || gs[0].Active != 1 {
		t.Fatalf("newest = %+v", gs[0])
	}
	// each automation run is its own
	if gs[1].OriginID != "aut_1" || gs[1].Runs != 1 || gs[1].Title != "chạy 2" || gs[2].Title != "chạy 1" {
		t.Fatalf("automation runs = %+v %+v", gs[1], gs[2])
	}
	if gs[3].ConversationID != "cnv_1" || gs[3].Runs != 2 || gs[3].Failed != 1 || gs[3].Status != "failed" {
		t.Fatalf("chat = %+v", gs[3])
	}
	// the next page starts after the last one shown
	more, err := st.Jobs().Groups(ctx, storage.JobFilter{ProjectID: p.ID, Limit: 10, Before: gs[2].Key})
	if err != nil || len(more) != 1 || more[0].ConversationID != "cnv_1" {
		t.Fatalf("next page = %+v %v", more, err)
	}
}

// A chat's jobs: its turns only.
func TestJobsOfAConversation(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", ConversationID: "cnv_1", Status: "done"})
	st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "user", ConversationID: "cnv_2", Status: "failed"})
	got, err := st.Jobs().List(ctx, storage.JobFilter{ConversationID: "cnv_1"})
	if err != nil || len(got) != 1 || got[0].ConversationID != "cnv_1" {
		t.Fatalf("jobs = %+v %v", got, err)
	}
}
