package cleanup_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/cleanup"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/usage"
)

type fx struct {
	st      storage.Store
	svc     *cleanup.Service
	engine  *chat.Engine
	project storage.Repo
	attach  string
}

// setup: a project with its lead on a Claude Code that answers with a summary.
func setup(t *testing.T) fx {
	t.Helper()
	ctx := context.Background()
	tmp := t.TempDir()
	st, err := sqlite.Open(filepath.Join(tmp, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	bin := filepath.Join(tmp, "claude")
	os.WriteFile(bin, []byte(`#!/bin/sh
cat > /dev/null
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"- sửa lỗi thanh toán, nhánh fix/pay","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	provs.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	engine := chat.NewEngine(st, provs, u)
	attach := filepath.Join(tmp, "attachments")
	return fx{st: st, svc: cleanup.New(st, engine, nil, attach, filepath.Join(tmp, "o.db")), engine: engine, project: project, attach: attach}
}

// chatWith makes a chat of the project with n messages, last active at.
func (f fx) chatWith(t *testing.T, purpose string, n int, at time.Time) storage.Conversation {
	t.Helper()
	ctx := context.Background()
	c, err := f.engine.StartConversationPurpose(ctx, f.project.ID, "", purpose)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		f.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Content: strings.Repeat("nội dung ", 50), CreatedAt: at})
	}
	return c
}

func TestUsageAndContent(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	old := time.Now().AddDate(0, 0, -40)
	c := f.chatWith(t, "", 3, old)
	f.chatWith(t, "burn", 2, old)
	fresh := f.chatWith(t, "", 1, time.Now())
	usage, err := f.st.Data().Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]storage.DataUsage{}
	for _, u := range usage {
		kinds[u.Kind] = u
	}
	if kinds["chat"].Items != 2 || kinds["chat"].Messages != 4 || kinds["burn"].Items != 1 || kinds["chat"].Bytes == 0 {
		t.Fatalf("usage = %+v", usage)
	}
	// older than 30 days, chats only: the old one, not the Burn's, not the fresh one
	p, err := f.svc.Plan(ctx, cleanup.Request{Kinds: []string{"chat"}, OlderThanDays: 30, Level: cleanup.LevelContent})
	if err != nil || len(p.Items) != 1 || p.Items[0].ID != c.ID {
		t.Fatalf("plan = %+v %v", p, err)
	}
	if res, err := f.svc.Run(ctx, cleanup.Request{Kinds: []string{"chat"}, OlderThanDays: 30, Level: cleanup.LevelContent}); err != nil || res.Done != 1 {
		t.Fatalf("run = %+v %v", res, err)
	}
	got, _ := f.st.Chat().GetConversation(ctx, c.ID)
	msgs, _ := f.st.Chat().ListMessages(ctx, c.ID)
	if got.Cleaned != storage.CleanContent || got.Title != c.Title || len(msgs) != 1 || !strings.Contains(msgs[0].Content, "đã được dọn") {
		t.Fatalf("cleaned = %+v, messages %+v", got, msgs)
	}
	if _, _, err := f.engine.Send(ctx, c.ID, "còn đó không?", nil); !errors.Is(err, chat.ErrCleaned) {
		t.Fatalf("a cleaned chat took a message: %v", err)
	}
	if again, _ := f.svc.Plan(ctx, cleanup.Request{Kinds: []string{"chat"}, OlderThanDays: 30, Level: cleanup.LevelContent}); len(again.Items) != 0 {
		t.Fatalf("cleaned twice: %+v", again.Items)
	}
	if fm, _ := f.st.Chat().ListMessages(ctx, fresh.ID); len(fm) != 1 {
		t.Fatal("the fresh chat was touched")
	}
}

func TestSummaryAndDelete(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	c := f.chatWith(t, "", 2, time.Now())
	if res, err := f.svc.Run(ctx, cleanup.Request{IDs: []string{c.ID}, Level: cleanup.LevelSummary}); err != nil || res.Done != 1 {
		t.Fatalf("summary = %+v %v", res, err)
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, c.ID)
	if got, _ := f.st.Chat().GetConversation(ctx, c.ID); got.Cleaned != storage.CleanSummary || len(msgs) != 1 || !strings.Contains(msgs[0].Content, "fix/pay") {
		t.Fatalf("summary: %+v %+v", got, msgs)
	}
	// a cleaned chat may still go for good
	if res, err := f.svc.Run(ctx, cleanup.Request{IDs: []string{c.ID}, Level: cleanup.LevelDelete}); err != nil || res.Done != 1 {
		t.Fatalf("delete = %+v %v", res, err)
	}
	if _, err := f.st.Chat().GetConversation(ctx, c.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
}

// A chat with a diff waiting, a running task: left as they are, and why.
func TestBusyIsLeft(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	c := f.chatWith(t, "", 1, time.Now())
	msgs, _ := f.st.Chat().ListMessages(ctx, c.ID)
	f.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: c.ID, MessageID: msgs[0].ID, Diff: "x", Status: "pending"})
	running, _ := f.st.Tasks().Create(ctx, storage.Task{ProjectID: f.project.ID, Goal: "đang làm", Mode: "single", Status: "running"})
	done, _ := f.st.Tasks().Create(ctx, storage.Task{ProjectID: f.project.ID, Goal: "xong rồi", Mode: "single", Status: "done", Result: "ok"})
	f.st.Tasks().AddStep(ctx, storage.TaskStep{TaskID: done.ID, Phase: "work", Output: "chi tiết dài", Status: "done"})
	p, err := f.svc.Plan(ctx, cleanup.Request{Level: cleanup.LevelContent})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Skipped) != 1 || p.Skipped[0].ID != c.ID || p.Skipped[0].Reason == "" {
		t.Fatalf("skipped = %+v", p.Skipped)
	}
	for _, it := range p.Items {
		if it.ID == running.ID {
			t.Fatal("a running task was picked")
		}
	}
	if _, err := f.svc.Run(ctx, cleanup.Request{Kinds: []string{"task"}, Level: cleanup.LevelContent}); err != nil {
		t.Fatal(err)
	}
	got, _ := f.st.Tasks().Get(ctx, done.ID)
	steps, _ := f.st.Tasks().ListSteps(ctx, done.ID)
	if got.Cleaned != storage.CleanContent || len(steps) != 0 || !strings.Contains(got.Result, "đã được dọn") {
		t.Fatalf("task = %+v, steps %d", got, len(steps))
	}
}

// An attachment no message points to, older than a day, is junk; one in use stays.
func TestSweepAttachments(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	write := func(id string, at time.Time) {
		dir := filepath.Join(f.attach, id)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dữ liệu"), 0o600)
		raw, _ := json.Marshal(map[string]any{"id": id, "project_id": f.project.ID, "created_at": at})
		os.WriteFile(filepath.Join(dir, "meta.json"), raw, 0o600)
	}
	old := time.Now().AddDate(0, 0, -3)
	write("att_orphan", old)
	write("att_used", old)
	write("att_new", time.Now())
	c := f.chatWith(t, "", 0, time.Now())
	f.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Content: "ảnh", Attachments: []storage.Attachment{{ID: "att_used", Name: "a.txt"}}})
	files, err := f.svc.Files(ctx)
	if err != nil || files.Projects[f.project.ID] == nil || files.Projects[f.project.ID].JunkItems != 1 || files.DBBytes == 0 {
		t.Fatalf("files = %+v %v", files, err)
	}
	if freed, err := f.svc.SweepFiles(ctx); err != nil || freed == 0 {
		t.Fatalf("sweep = %d %v", freed, err)
	}
	for id, want := range map[string]bool{"att_orphan": false, "att_used": true, "att_new": true} {
		if _, err := os.Stat(filepath.Join(f.attach, id)); (err == nil) != want {
			t.Errorf("%s: there = %v, want %v", id, err == nil, want)
		}
	}
}

// Cleaning on its own: off by default; on, once a day.
func TestAutoSettings(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if a := f.svc.AutoSettings(ctx); a.Enabled || a.Days != 30 || a.Level != cleanup.LevelContent {
		t.Fatalf("default = %+v", a)
	}
	if _, err := f.svc.SetAuto(ctx, cleanup.Auto{Enabled: true, Days: 0, Level: cleanup.LevelContent}); err == nil {
		t.Fatal("0 days saved")
	}
	if _, err := f.svc.SetAuto(ctx, cleanup.Auto{Enabled: true, Days: 7, Level: "wipe"}); err == nil {
		t.Fatal("an unknown level saved")
	}
	if a, err := f.svc.SetAuto(ctx, cleanup.Auto{Enabled: true, Days: 7, Level: cleanup.LevelSummary}); err != nil || !a.Enabled || a.Days != 7 {
		t.Fatalf("saved = %+v %v", a, err)
	}
}
