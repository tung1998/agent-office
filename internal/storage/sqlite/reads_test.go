package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// A chat the person wrote in is unread once an agent answers after they last
// looked; looking makes it read; marking it unread brings it back; a chat they
// never wrote in (a bot's) is not theirs to read.
func TestUnread(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	chat := st.Chat()
	mine, _ := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "mine"})
	bots, _ := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "bot"})
	chat.AddMessage(ctx, storage.Message{ConversationID: mine.ID, Role: "user", Author: "human:a@x.io", Content: "làm X"})
	chat.AddMessage(ctx, storage.Message{ConversationID: bots.ID, Role: "user", Author: "discord:an", Content: "hi"})
	chat.AddMessage(ctx, storage.Message{ConversationID: bots.ID, Role: "assistant", Author: "Dev", Content: "hello"})
	if ids, _ := chat.Unread(ctx, "usr_a", "human:a@x.io"); len(ids) != 0 {
		t.Fatalf("no answer yet: %v", ids)
	}
	chat.AddMessage(ctx, storage.Message{ConversationID: mine.ID, Role: "assistant", Author: "Dev", Content: "xong"})
	if ids, _ := chat.Unread(ctx, "usr_a", "human:a@x.io"); len(ids) != 1 || ids[0] != mine.ID {
		t.Fatalf("answered = %v", ids)
	}
	if err := chat.MarkSeen(ctx, "usr_a", mine.ID, true); err != nil {
		t.Fatal(err)
	}
	if ids, _ := chat.Unread(ctx, "usr_a", "human:a@x.io"); len(ids) != 0 {
		t.Fatalf("seen = %v", ids)
	}
	if ids, _ := chat.Unread(ctx, "usr_b", "human:b@x.io"); len(ids) != 0 {
		t.Fatalf("someone else's chat = %v", ids)
	}
	chat.MarkSeen(ctx, "usr_a", mine.ID, false)
	if ids, _ := chat.Unread(ctx, "usr_a", "human:a@x.io"); len(ids) != 1 {
		t.Fatalf("marked unread = %v", ids)
	}
	time.Sleep(2 * time.Millisecond)
	chat.MarkSeen(ctx, "usr_a", mine.ID, true)
	chat.AddMessage(ctx, storage.Message{ConversationID: mine.ID, Role: "assistant", Author: "Dev", Content: "thêm"})
	if ids, _ := chat.Unread(ctx, "usr_a", "human:a@x.io"); len(ids) != 1 {
		t.Fatalf("a new answer after looking = %v", ids)
	}
}

// Seen all: every chat of theirs answered since they looked, in one go; a
// chat they never wrote in stays as it is.
func TestMarkAllSeen(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	chat := st.Chat()
	for _, title := range []string{"a", "b", "c"} {
		c, _ := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: title})
		chat.AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Author: "human:a@x.io", Content: "làm"})
		chat.AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Author: "Dev", Content: "xong"})
	}
	theirs, _ := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "b's"})
	chat.AddMessage(ctx, storage.Message{ConversationID: theirs.ID, Role: "user", Author: "human:b@x.io", Content: "làm"})
	chat.AddMessage(ctx, storage.Message{ConversationID: theirs.ID, Role: "assistant", Author: "Dev", Content: "xong"})
	n, err := chat.MarkAllSeen(ctx, "usr_a", "human:a@x.io")
	if err != nil || n != 3 {
		t.Fatalf("marked = %d %v", n, err)
	}
	if ids, _ := chat.Unread(ctx, "usr_a", "human:a@x.io"); len(ids) != 0 {
		t.Fatalf("left unread = %v", ids)
	}
	if ids, _ := chat.Unread(ctx, "usr_b", "human:b@x.io"); len(ids) != 1 {
		t.Fatalf("someone else's chat = %v", ids)
	}
}
