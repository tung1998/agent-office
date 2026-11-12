package chat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/team"
)

// send_to_chat: the message goes into the other chat as its person wrote it,
// marked as relayed (so what it starts does not relay on by itself).
func TestRelayToAnotherChat(t *testing.T) {
	t.Parallel()
	bin, _ := fakeClaude(t, "B", true)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := actor.With(context.Background(), "human:a@b.c")
	from, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	to, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	now := time.Now().UTC()
	job, _ := f.st.Jobs().Create(ctx, storage.Job{ProjectID: f.project.ID, Kind: "chat_turn", Origin: "user", CreatedBy: "human:a@b.c", ConversationID: from.ID, Status: "running", StartedAt: &now})
	a := storage.Action{ProjectID: f.project.ID, ConversationID: from.ID, JobID: job.ID, Kind: "send_message", TargetID: to.ID, ProposedBy: "Trợ lý",
		Args: storage.ActionArgs{Message: "Sửa giúp lỗi giỏ hàng"}}
	if _, _, err := f.engine.Relay(ctx, a); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(f.engine.Running(to.ID)) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, to.ID)
	if len(msgs) < 1 || msgs[0].Role != "user" || msgs[0].Author != "human:a@b.c" || msgs[0].Content != "Sửa giúp lỗi giỏ hàng" ||
		!strings.Contains(msgs[0].Context, from.ID) {
		t.Fatalf("messages = %+v", msgs)
	}
	if !f.engine.Relayed(ctx, to.ID) || f.engine.Relayed(ctx, from.ID) {
		t.Fatal("relayed mark wrong")
	}
	// shown with the message: the agent, its project and the chat it came from
	if r := chat.RelayOf(msgs[0]); r == nil || r.Agent != "Trợ lý" || r.Project != "demo" || r.ProjectID != f.project.ID || r.ConversationID != from.ID || r.Report {
		t.Fatalf("relay = %+v", r)
	}
	if hist, _ := f.engine.History(ctx, to.ID); len(hist) < 1 || hist[0].Relay == nil || hist[0].Relay.Agent != "Trợ lý" {
		t.Fatalf("history relay = %+v", hist)
	}

	// a bot's or an automation's run cannot speak for a person
	bot, _ := f.st.Jobs().Create(ctx, storage.Job{ProjectID: f.project.ID, Kind: "chat_turn", Origin: "automation", CreatedBy: "discord:1", ConversationID: from.ID, Status: "running", StartedAt: &now})
	a.JobID = bot.ID
	if _, _, err := f.engine.Relay(ctx, a); err == nil {
		t.Fatal("a bot's run relayed in a person's name")
	}
}

// send_to_chat with a linked project and no chat: a new chat opens there in
// the person's name, its agent answers, and where it went comes back.
func TestRelayNewChatInLinkedProject(t *testing.T) {
	t.Parallel()
	bin, _ := fakeClaude(t, "B", true)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := actor.With(context.Background(), "human:a@b.c")
	admin, _ := f.st.Repos().Create(ctx, storage.Repo{Name: "admin v3", Path: t.TempDir()})
	team.NewService(f.st, nil).AddFromLibrary(ctx, admin.ID, team.StarterAgent)
	from, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	f.engine.SetMode(ctx, from.ID, perm.Operate)
	now := time.Now().UTC()
	job, _ := f.st.Jobs().Create(ctx, storage.Job{ProjectID: f.project.ID, Kind: "chat_turn", Origin: "user", CreatedBy: "human:a@b.c", ConversationID: from.ID, Status: "running", StartedAt: &now})
	a := storage.Action{ProjectID: f.project.ID, ConversationID: from.ID, JobID: job.ID, Kind: "send_message", ProposedBy: "Lead",
		Args: storage.ActionArgs{Message: "Sửa trang admin như đã bàn", Project: admin.ID}}
	detail, to, err := f.engine.Relay(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	convs, _ := f.st.Chat().ListConversations(ctx, admin.ID, 10)
	// ADR-207: the new chat runs at the mode of the chat it came from
	if len(convs) != 1 || convs[0].ID != to || convs[0].CreatedBy != "human:a@b.c" || convs[0].Mode != perm.Operate || !strings.Contains(detail, "/projects/"+admin.ID+"?tab=chat&c="+to) {
		t.Fatalf("detail %q, chats %+v", detail, convs)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(f.engine.Running(to)) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, to)
	if len(msgs) < 2 || msgs[0].Content != "Sửa trang admin như đã bàn" || !strings.Contains(msgs[0].Context, `"relayed_from_project":"demo"`) ||
		!strings.Contains(msgs[0].Context, `"relay_hops":1`) {
		t.Fatalf("messages = %+v", msgs)
	}

	// its answer comes back to the chat that asked, as a report in the chain
	var back []storage.Message
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		back, _ = f.st.Chat().ListMessages(ctx, from.ID)
		if len(back) > 0 {
			break
		}
	}
	if len(back) < 1 || back[0].Role != "user" || !strings.HasPrefix(back[0].Content, "↩ Báo lại từ **admin v3**") ||
		!strings.Contains(back[0].Content, msgs[1].Content) || !strings.Contains(back[0].Context, `"relay_hops":2`) || !strings.Contains(back[0].Context, `"relay_report":true`) {
		t.Fatalf("report back = %+v", back)
	}
	if r := chat.RelayOf(back[0]); r == nil || !r.Report || r.Project != "admin v3" || r.ProjectID != admin.ID || r.ConversationID != to || r.Agent == "" {
		t.Fatalf("report = %+v", r)
	}
	// the hand-off: no report, but who sent it, from which project and chat
	if r := chat.RelayOf(msgs[0]); r == nil || r.Report || r.Agent != "Lead" || r.Project != "demo" || r.ProjectID != f.project.ID || r.ConversationID != from.ID {
		t.Fatalf("hand-off relay = %+v", r)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if back, _ = f.st.Chat().ListMessages(ctx, from.ID); len(back) >= 2 && len(f.engine.Running(from.ID)) == 0 {
			break
		}
	}
	time.Sleep(300 * time.Millisecond)
	// the asking agent's answer to the report does not bounce back by itself
	if again, _ := f.st.Chat().ListMessages(ctx, to); len(again) != len(msgs) {
		t.Fatalf("a report's answer was sent back: %+v", again)
	}
}

// What a linked chat this one handed work to waits on shows here; its own
// project's chats and decided cards do not.
func TestLinkedPending(t *testing.T) {
	t.Parallel()
	f := setup(t, func(provs *provider.Service) storage.Provider { return storage.Provider{} })
	ctx := context.Background()
	admin, _ := f.st.Repos().Create(ctx, storage.Repo{Name: "admin v3", Path: t.TempDir()})
	from, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: f.project.ID})
	there, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: admin.ID, Title: "Sửa trang admin"})
	same, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: f.project.ID})
	f.st.Actions().Create(ctx, storage.Action{ProjectID: f.project.ID, ConversationID: from.ID, Kind: "send_message", Status: "done", TargetID: there.ID, Args: storage.ActionArgs{Project: admin.ID}})
	f.st.Actions().Create(ctx, storage.Action{ProjectID: f.project.ID, ConversationID: from.ID, Kind: "send_message", Status: "done", TargetID: same.ID})
	f.st.Actions().Create(ctx, storage.Action{ProjectID: f.project.ID, ConversationID: same.ID, Kind: "run_command", Status: "pending", Target: "ls"})
	if got := f.engine.LinkedPending(ctx, from.ID); len(got) != 0 {
		t.Fatalf("nothing waits there yet: %+v", got)
	}
	f.st.Actions().Create(ctx, storage.Action{ProjectID: admin.ID, ConversationID: there.ID, Kind: "run_command", Status: "pending", Target: "git fetch"})
	f.st.Actions().Create(ctx, storage.Action{ProjectID: admin.ID, ConversationID: there.ID, Kind: "run_command", Status: "done", Target: "git status"})
	got := f.engine.LinkedPending(ctx, from.ID)
	if len(got) != 1 || got[0].Project != "admin v3" || got[0].ConversationID != there.ID || len(got[0].Actions) != 1 || got[0].Actions[0].Target != "git fetch" {
		t.Fatalf("linked pending = %+v", got)
	}
	// a chat known only from its report back here (its target never kept)
	old, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: admin.ID, Title: "Trang mockup"})
	f.st.Chat().AddMessage(ctx, storage.Message{ConversationID: from.ID, Role: "user", Content: "↩ Báo lại",
		Context: `{"relayed_from":"` + old.ID + `","relayed_from_project":"admin v3","relay_hops":2,"relay_report":true}`})
	f.st.Actions().Create(ctx, storage.Action{ProjectID: admin.ID, ConversationID: old.ID, Kind: "run_command", Status: "pending", Target: "pnpm test"})
	if got = f.engine.LinkedPending(ctx, from.ID); len(got) != 2 || got[1].ConversationID != old.ID {
		t.Fatalf("a chat that reported back = %+v", got)
	}
}

// A message relayed before its project was kept still shows where it came
// from: office looks up the chat it names.
func TestHistoryFillsOlderRelay(t *testing.T) {
	t.Parallel()
	f := setup(t, func(provs *provider.Service) storage.Provider { return storage.Provider{} })
	ctx := context.Background()
	src, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: f.project.ID, Title: "Trang mockup"})
	dst, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: f.project.ID})
	f.st.Chat().AddMessage(ctx, storage.Message{ConversationID: dst.ID, Role: "user", Content: "làm giúp",
		Context: `{"relayed_from":"` + src.ID + `","relayed_by_agent":"Lead"}`})
	hist, err := f.engine.History(ctx, dst.ID)
	if err != nil || len(hist) != 1 {
		t.Fatal(err, hist)
	}
	if r := hist[0].Relay; r == nil || r.Project != "demo" || r.ProjectID != f.project.ID || r.Title != "Trang mockup" || r.Agent != "Lead" {
		t.Fatalf("relay = %+v", r)
	}
}
