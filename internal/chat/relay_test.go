package chat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// send_to_chat: the message goes into the other chat as its person wrote it,
// marked as relayed (so what it starts does not relay on by itself).
func TestRelayToAnotherChat(t *testing.T) {
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
	if _, err := f.engine.Relay(ctx, a); err != nil {
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

	// a bot's or an automation's run cannot speak for a person
	bot, _ := f.st.Jobs().Create(ctx, storage.Job{ProjectID: f.project.ID, Kind: "chat_turn", Origin: "automation", CreatedBy: "discord:1", ConversationID: from.ID, Status: "running", StartedAt: &now})
	a.JobID = bot.ID
	if _, err := f.engine.Relay(ctx, a); err == nil {
		t.Fatal("a bot's run relayed in a person's name")
	}
}
