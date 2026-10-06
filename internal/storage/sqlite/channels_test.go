package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// What ties a bot's chats to office's conversations is let go once those
// conversations have been quiet for a while.
func TestChannelPrune(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: p.ID, Kind: "discord", Name: "Bot", Enabled: true})
	old, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Purpose: "channel"})
	fresh, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Purpose: "channel"})
	st.Channels().SetThread(ctx, ch.ID, "msg:42:1", old.ID)
	st.Channels().SetThread(ctx, ch.ID, "msg:42:2", fresh.ID)
	st.Settings().Set(ctx, "channel_rule/"+ch.ID+"/42/1", "aut_1")
	// later: only the fresh one was active since the cut-off
	cut := time.Now().Add(time.Second)
	time.Sleep(1100 * time.Millisecond)
	st.Chat().AddMessage(ctx, storage.Message{ConversationID: fresh.ID, Role: "user", Content: "vẫn đang nói"})
	n, err := st.Channels().Prune(ctx, cut)
	if err != nil || n < 2 {
		t.Fatalf("pruned %d %v", n, err)
	}
	if id, _ := st.Channels().Thread(ctx, ch.ID, "msg:42:1"); id != "" {
		t.Fatal("the quiet chat's link stayed")
	}
	if id, _ := st.Channels().Thread(ctx, ch.ID, "msg:42:2"); id != fresh.ID {
		t.Fatal("the active chat's link was dropped")
	}
	var rule string
	if ok, _ := st.Settings().Get(ctx, "channel_rule/"+ch.ID+"/42/1", &rule); ok {
		t.Fatal("an old reply rule stayed")
	}
}
