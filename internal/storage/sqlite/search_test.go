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

// A message written with a Vietnamese abbreviation (or spelled out in full)
// is found either way: searching "vd" must find "ví dụ" and searching
// "ví dụ" must find "vd" (textrank.Expand).
func TestSearchMessagesAbbreviationBothWays(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	chat := st.Chat()
	abbr, _ := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "abbr", CreatedBy: "human:a@x.io"})
	chat.AddMessage(ctx, storage.Message{ConversationID: abbr.ID, Role: "user", Author: "human:a@x.io", Content: "vd thêm field mới vào form"})
	full, _ := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "full", CreatedBy: "human:a@x.io"})
	chat.AddMessage(ctx, storage.Message{ConversationID: full.ID, Role: "user", Author: "human:a@x.io", Content: "ví dụ về cách dùng API"})
	all := storage.SearchFilter{ProjectIDs: []string{p.ID}}
	if hits, err := chat.SearchMessages(ctx, "vd", all); err != nil || len(hits) != 2 {
		t.Fatalf("searching abbreviation should find both forms: %d %v", len(hits), err)
	}
	if hits, err := chat.SearchMessages(ctx, "ví dụ", all); err != nil || len(hits) != 2 {
		t.Fatalf("searching full form should find both forms: %d %v", len(hits), err)
	}
}

// A note abbreviation stuck to punctuation ("vd:", "ko,") still matches,
// and a single abbreviated word ("ko") is found by its full form ("không").
func TestSearchMessagesAbbreviationWithPunctuation(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	chat := st.Chat()
	conv, _ := chat.CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "note", CreatedBy: "human:a@x.io"})
	chat.AddMessage(ctx, storage.Message{ConversationID: conv.ID, Role: "user", Author: "human:a@x.io", Content: "vd: thêm field mới vào form"})
	chat.AddMessage(ctx, storage.Message{ConversationID: conv.ID, Role: "assistant", Author: "Dev", Content: "ko, chưa làm xong việc này"})
	all := storage.SearchFilter{ProjectIDs: []string{p.ID}}
	if hits, err := chat.SearchMessages(ctx, "vd", all); err != nil || len(hits) != 1 {
		t.Fatalf("'vd:' not found by 'vd': %d %v", len(hits), err)
	}
	if hits, err := chat.SearchMessages(ctx, "ví dụ", all); err != nil || len(hits) != 1 {
		t.Fatalf("'vd:' not found by 'ví dụ': %d %v", len(hits), err)
	}
	if hits, err := chat.SearchMessages(ctx, "ko", all); err != nil || len(hits) != 1 {
		t.Fatalf("'ko,' not found by 'ko': %d %v", len(hits), err)
	}
	if hits, err := chat.SearchMessages(ctx, "không", all); err != nil || len(hits) != 1 {
		t.Fatalf("'ko,' not found by full word 'không': %d %v", len(hits), err)
	}
}
