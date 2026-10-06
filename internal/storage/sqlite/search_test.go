package sqlite_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-086: the chats are searched by their words, accents ignored, newest
// messages kept in the index; another person's private chat never shows.
func TestSearchMessages(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	chat := st.Chat()
	pay, _ := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "Thanh toán", CreatedBy: "human:a@x.io"})
	chat.AddMessage(ctx, storage.Message{ConversationID: pay.ID, Role: "user", Author: "human:a@x.io", Content: "sửa giúp lỗi thanh toán bằng PayPal"})
	chat.AddMessage(ctx, storage.Message{ConversationID: pay.ID, Role: "assistant", Author: "Dev", Content: "đã sửa checkout, thêm test"})
	other, _ := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "riêng", CreatedBy: "human:b@x.io"})
	chat.AddMessage(ctx, storage.Message{ConversationID: other.ID, Role: "user", Author: "human:b@x.io", Content: "thanh toán lương tháng này"})
	all := storage.SearchFilter{ProjectIDs: []string{p.ID}}
	hits, err := chat.SearchMessages(ctx, "thanh toan", all)
	if err != nil || len(hits) != 2 {
		t.Fatalf("accents ignored = %d %v", len(hits), err)
	}
	if !strings.Contains(hits[0].Snippet, "«") || hits[0].Title == "" {
		t.Fatalf("hit = %+v", hits[0])
	}
	private := all
	private.HideOthersIn, private.Me = p.ID, "human:a@x.io"
	if hits, _ := chat.SearchMessages(ctx, "thanh toán", private); len(hits) != 1 || hits[0].ConversationID != pay.ID {
		t.Fatalf("someone else's private chat showed: %+v", hits)
	}
	byDev := all
	byDev.Author = "Dev"
	if hits, _ := chat.SearchMessages(ctx, "checkout", byDev); len(hits) != 1 {
		t.Fatalf("by author = %+v", hits)
	}
	if hits, _ := chat.SearchMessages(ctx, `") OR (`, all); len(hits) != 0 {
		t.Fatalf("FTS syntax got through: %+v", hits)
	}
	chat.DeleteConversation(ctx, other.ID)
	if hits, _ := chat.SearchMessages(ctx, "lương", all); len(hits) != 0 {
		t.Fatalf("a deleted chat still found: %+v", hits)
	}
}
