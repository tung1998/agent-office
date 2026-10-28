package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/harness"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/verify"
)

// The bundle: assets from the folder; diffs split by who ended them; a merged
// diff counts as checked only with a check since the chat's previous one;
// office's own messages are not the person correcting an agent.
func TestCollect(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# rules\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, ".github/workflows"), 0o755)
	os.WriteFile(filepath.Join(dir, ".github/workflows/ci.yml"), []byte("on: push\n"), 0o644)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: dir})
	c, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	msg := func(role, author, content string, tools ...storage.ToolCall) {
		if _, err := st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: role, Author: author, Content: content, Tools: tools}); err != nil {
			t.Fatal(err)
		}
	}
	patch := func(status, by, detail string) {
		if _, err := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: c.ID, Diff: "d", Status: status, DecidedBy: by, Detail: detail}); err != nil {
			t.Fatal(err)
		}
	}
	msg("assistant", "", "ok", storage.ToolCall{Name: "Bash", Summary: "Chạy go test ./internal/..."})
	patch("applied", "human:a@x.io", "")
	patch("applied", "human:a@x.io", "") // nothing ran since the previous one
	patch("rejected", "human:a@x.io", "không đúng ý")
	patch("rejected", "office", "Thay bằng thay đổi mới hơn")
	patch("failed", "", "Chưa gộp được: còn dấu xung đột ở a.go")
	patch("rejected", "office", verify.Held+":\n`go vet ./...` lỗi\nThay bằng thay đổi mới hơn")
	patch("applied", "auto:x", verify.Passed+" (go build ./...)\nĐã áp dụng vào a.go") // office checked it: a check ran
	msg("user", "human:a@x.io", "sai rồi, làm lại đi")
	msg("user", "human:a@x.io", "[office] a@x.io decided your proposals: sai")
	msg("user", "burn:a@x.io", "vẫn lỗi")
	st.Actions().Create(ctx, storage.Action{ProjectID: p.ID, ConversationID: c.ID, Kind: "run_command", Target: "rm -rf x", Status: "rejected"})

	ev, err := harness.Collect(ctx, st, p, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.Assets.Instructions) != 1 || len(ev.Assets.CI) != 1 {
		t.Fatalf("assets = %+v", ev.Assets)
	}
	d := ev.Diffs
	if d.Total != 7 || d.Merged != 3 || d.RejectedByPerson != 1 || d.Superseded != 2 || d.FailedToMerge != 1 || d.Rejected[0].Text != "không đúng ý" {
		t.Fatalf("diffs = %+v", d)
	}
	if v := ev.Validation; v.MergedDiffs != 3 || v.WithCheck != 2 || v.GatePassed != 1 || v.GateHeld != 1 || len(v.WithoutCheck) != 1 || v.CheckCommands[0].Key != "go test" {
		t.Fatalf("validation = %+v", v)
	}
	if co := ev.Corrections; co.Messages != 1 || co.Count != 1 {
		t.Fatalf("corrections = %+v", co)
	}
	if r := ev.Commands.Refused; len(r) != 1 || r[0].Key != "run_command: rm -rf x" {
		t.Fatalf("refused = %+v", r)
	}
}

// ADR-185: the bundle shows each agent's skills at hand and their use.
func TestCollectAgentSkills(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "site"})
	a, err := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "dev", Name: "Dev", ModelTier: "balanced"})
	if err != nil {
		t.Fatal(err)
	}
	st.AgentSkills().Add(ctx, storage.AgentSkill{AgentID: a.ID, Name: "launch-checklist", Source: "setup"})
	st.AgentSkills().Add(ctx, storage.AgentSkill{AgentID: a.ID, Name: "design-polish", Source: "agent"})
	st.AgentSkills().Used(ctx, a.ID, "design-polish")
	ev, err := harness.Collect(ctx, st, p, 30)
	if err != nil {
		t.Fatal(err)
	}
	got := ev.Learning.AgentSkills
	if len(got) != 2 || !strings.HasPrefix(got[0], "Dev: launch-checklist (setup, never used") || !strings.HasPrefix(got[1], "Dev: design-polish (agent, used 1×") {
		t.Fatalf("agent skills = %q", got)
	}
}

// ADR-228: the same request in several chats is a repeat, wording, accents
// and case aside; one chat's back-and-forth, short replies and office's own
// messages are not. Enabled automations are listed so a covered one is not
// proposed again.
func TestCollectRepeats(t *testing.T) {
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
	say := func(content string) string {
		c, err := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Author: "human:a@x.io", Content: content}); err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	say("kiểm tra log lỗi production hôm nay rồi báo lại")
	say("Kiem tra log loi production hom qua roi bao lai")
	say("kiểm tra log lỗi production sáng nay và báo lại giúp")
	say("ok tiếp tục")
	say("@Thực thi làm đi")
	say("@Thực thi làm đi")
	say("@Thực thi làm đi")
	say("[office] kiểm tra log lỗi production hôm nay rồi báo lại")
	one := say("viết test cho hàm tính giá ship")
	for range 3 {
		st.Chat().AddMessage(ctx, storage.Message{ConversationID: one, Role: "user", Author: "human:a@x.io", Content: "viết test cho hàm tính giá ship"})
	}
	st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "Báo cáo sáng", Enabled: true, Source: "schedule", Action: "script"})
	st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "Cũ", Source: "schedule", Action: "chat"})
	ev, err := harness.Collect(ctx, st, p, 30)
	if err != nil {
		t.Fatal(err)
	}
	r := ev.Repeats
	if len(r) != 1 || r[0].Count != 3 || r[0].Chats != 3 || !strings.Contains(r[0].Sample, "production") {
		t.Fatalf("repeats = %+v", r)
	}
	if a := ev.Assets.Automations; len(a) != 1 || a[0] != "Báo cáo sáng: schedule → script" {
		t.Fatalf("automations = %q", a)
	}
}
