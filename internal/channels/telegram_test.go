package channels

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTelegram(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	calls := 0
	commands := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/botTOK/") {
			http.NotFound(w, r)
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/botTOK/") {
		case "getMe":
			w.Write([]byte(`{"ok":true,"result":{"username":"shop_bot"}}`))
		case "getUpdates":
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n > 1 {
				time.Sleep(50 * time.Millisecond)
				w.Write([]byte(`{"ok":true,"result":[]}`))
				return
			}
			w.Write([]byte(`{"ok":true,"result":[
				{"update_id":1,"message":{"text":"xin chào","chat":{"id":42,"type":"private"},"from":{"id":7,"username":"an"}}},
				{"update_id":2,"message":{"text":"nói chuyện riêng","chat":{"id":-5,"type":"group"},"from":{"id":8}}},
				{"update_id":3,"message":{"text":"@shop_bot đơn 123 đâu","chat":{"id":-5,"type":"group"},"from":{"id":8,"username":"binh"}}},
				{"update_id":4,"message":{"text":"còn đơn 456?","chat":{"id":-5,"type":"supergroup"},"from":{"id":9},"reply_to_message":{"from":{"username":"shop_bot"}}}},
				{"update_id":5,"message":{"text":"/create_conversation","chat":{"id":-5,"type":"group"},"from":{"id":9}}}
			]}`))
		case "setMyCommands":
			mu.Lock()
			commands = true
			mu.Unlock()
			w.Write([]byte(`{"ok":true,"result":true}`))
		case "sendMessage", "sendChatAction":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if r.URL.Path == "/botTOK/sendMessage" {
				mu.Lock()
				sent = append(sent, body["text"].(string))
				mu.Unlock()
			}
			w.Write([]byte(`{"ok":true,"result":{}}`))
		}
	}))
	defer srv.Close()
	tg := &Telegram{Token: "TOK", BaseURL: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 10)
	bot := ""
	go tg.Run(ctx, func(name string) { bot = name; tg.SetCommands(ctx, Builtins()) }, func(m Incoming) { got <- m })
	var msgs []Incoming
	for len(msgs) < 5 {
		select {
		case m := <-got:
			msgs = append(msgs, m)
		case <-ctx.Done():
			t.Fatalf("got %+v", msgs)
		}
	}
	// every message comes up; Addressed = for the bot (private, tagged, a reply to it, a command)
	for i, want := range []bool{true, false, true, true, true} {
		if msgs[i].Addressed != want {
			t.Fatalf("msg %d addressed = %v: %+v", i, msgs[i].Addressed, msgs[i])
		}
	}
	if bot != "shop_bot" || msgs[0].ChatID != "42" || !msgs[0].Private || msgs[1].Text != "nói chuyện riêng" || msgs[2].Text != "đơn 123 đâu" || msgs[3].Text != "còn đơn 456?" || msgs[2].UserName != "binh" || msgs[4].Text != "/create_conversation" {
		t.Fatalf("bot %q msgs %+v", bot, msgs)
	}
	if _, err := tg.Send(ctx, "42", strings.Repeat("a", 5000)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !commands {
		t.Fatal("the commands menu was not set")
	}
	if len(sent) != 2 || len(sent[0]) > 4096 {
		t.Fatalf("sent %d parts", len(sent))
	}
}

// Review I4: an error never carries the bot token (it is in the URL).
func TestTelegramErrorHidesToken(t *testing.T) {
	tg := &Telegram{Token: "123:SECRET", BaseURL: "http://127.0.0.1:1"}
	err := tg.call(context.Background(), "getMe", map[string]any{}, nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

// In a group, "/cmd@other_bot" is for another bot; "/cmd@shop_bot" and "/cmd" are ours.
func TestTelegramCommandForAnotherBot(t *testing.T) {
	tg := &Telegram{bot: "shop_bot"}
	tg.menu.set([]Command{{Name: "pending"}})
	for text, want := range map[string]bool{"/pending@other_bot": false, "/pending@Shop_Bot": true, "/pending": true} {
		m := &tgMessage{Text: text}
		m.Chat.Type = "group"
		in, ok := tg.addressed(m)
		if in.Addressed != want || ok != want {
			t.Errorf("%q addressed = %v, want %v", text, in.Addressed, want)
		}
	}
}

// Telegram: inline buttons; a press (callback_query) comes up as its command.
func TestTelegramButtons(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.URL.Path+" "+string(raw))
		mu.Unlock()
		switch r.URL.Path {
		case "/botTOK/getMe":
			w.Write([]byte(`{"ok":true,"result":{"username":"shop_bot"}}`))
		case "/botTOK/getUpdates":
			if strings.Contains(string(raw), `"offset":0`) {
				w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"callback_query":{"id":"cb1","from":{"id":7,"username":"an"},"data":"/approve 1","message":{"message_id":3,"chat":{"id":-100,"type":"supergroup"}}}}]}`))
				return
			}
			time.Sleep(50 * time.Millisecond)
			w.Write([]byte(`{"ok":true,"result":[]}`))
		case "/botTOK/sendMessage":
			w.Write([]byte(`{"ok":true,"result":{"message_id":9}}`))
		default:
			w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	defer srv.Close()
	tg := &Telegram{Token: "TOK", BaseURL: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 2)
	go tg.Run(ctx, func(string) {}, func(m Incoming) { got <- m })
	var m Incoming
	select {
	case m = <-got:
	case <-ctx.Done():
		t.Fatal("no press")
	}
	if m.Text != "/approve 1" || m.ChatID != "-100" || m.UserID != "7" || !m.Addressed || m.ButtonMsg != "3" {
		t.Fatalf("press = %+v", m)
	}
	if _, err := tg.SendButtons(ctx, "-100", "Chờ duyệt", [][]Button{{{Label: "Duyệt 1", Data: "/approve 1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := tg.EditButtons(ctx, "-100", "3", "✅ Đã duyệt 1", nil); err != nil { // decided: its buttons off
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) { // the press is answered in the background
		mu.Lock()
		answered := strings.Contains(strings.Join(calls, "\n"), "answerCallbackQuery")
		mu.Unlock()
		if answered {
			break
		}
	}
	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(calls, "\n")
	for _, want := range []string{"answerCallbackQuery", `"callback_data":"/approve 1"`, `"inline_keyboard"`, "callback_query", `/botTOK/editMessageText {"chat_id":-100,"message_id":3,"reply_markup":{"inline_keyboard":[]},"text":"✅ Đã duyệt 1"}`} {
		if !strings.Contains(all, want) {
			t.Errorf("no %s in\n%s", want, all)
		}
	}
}

func TestChunksKeepsFenceBalanced(t *testing.T) {
	n := 100
	// A code fence that straddles the natural cut point (around n/2..n).
	body := strings.Repeat("line of code here\n", 10)
	text := "intro text\n```go\n" + body + "```\nafter the fence"
	if len(text) <= n {
		t.Fatalf("test text too short to force a split: %d bytes", len(text))
	}

	parts := chunks(text, n)
	if len(parts) < 2 {
		t.Fatalf("expected the fence to force a split, got %d chunk(s)", len(parts))
	}

	for i, p := range parts {
		if strings.Count(p, "```")%2 != 0 {
			t.Fatalf("chunk %d has an unbalanced fence:\n%s", i, p)
		}
	}

	// Reassembling (minus the synthetic close/reopen markers) must still
	// contain every original line of code.
	var rejoined strings.Builder
	for _, p := range parts {
		rejoined.WriteString(p)
	}
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if !strings.Contains(rejoined.String(), line) {
			t.Fatalf("lost content %q across split", line)
		}
	}
}

// TestChunksTerminatesOnOverlongFenceLine guards against the infinite loop a
// reviewer caught: a fence-opening line that alone is at or beyond n/2 bytes
// made close+reopen reconstruct the exact same remaining text forever.
func TestChunksTerminatesOnOverlongFenceLine(t *testing.T) {
	n := 100
	text := "```" + strings.Repeat("a", 60) + "\n" + strings.Repeat("b", 60)

	done := make(chan []string, 1)
	go func() { done <- chunks(text, n) }()

	select {
	case parts := <-done:
		if len(parts) == 0 {
			t.Fatal("expected at least one chunk")
		}
		if len(parts) > 10 {
			t.Fatalf("suspiciously many chunks (%d), possible non-termination symptom", len(parts))
		}
		for i, p := range parts {
			if len(p) > n+4 { // +4 = len("\n```"), the synthetic close marker
				t.Fatalf("chunk %d is %d bytes, over the n=%d (+4 overhead) limit:\n%s", i, len(p), n, p)
			}
		}
		var rejoined strings.Builder
		for _, p := range parts {
			rejoined.WriteString(p)
		}
		if !strings.Contains(rejoined.String(), strings.Repeat("a", 60)) || !strings.Contains(rejoined.String(), strings.Repeat("b", 60)) {
			t.Fatalf("lost content across split: %q", parts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("chunks() did not terminate: infinite loop on an overlong fence-opening line")
	}
}

// TestChunksTerminatesOnOverlongInlineFence covers the other no-progress
// shape a reviewer asked for: a single inline ```x``` span whose markers
// themselves are longer than n, so the hard byte-cut fallback must slice
// through it. chunks() must still terminate, keep every chunk within the
// n+4 overhead bound, and not lose content.
func TestChunksTerminatesOnOverlongInlineFence(t *testing.T) {
	n := 60
	text := "```" + strings.Repeat("x", n+20) + "```\n" + strings.Repeat("plain trailing line here\n", 3)

	done := make(chan []string, 1)
	go func() { done <- chunks(text, n) }()

	select {
	case parts := <-done:
		if len(parts) == 0 {
			t.Fatal("expected at least one chunk")
		}
		for i, p := range parts {
			if len(p) > n+4 {
				t.Fatalf("chunk %d is %d bytes, over the n=%d (+4 overhead) limit:\n%s", i, len(p), n, p)
			}
		}
		var rejoined strings.Builder
		for _, p := range parts {
			rejoined.WriteString(p)
		}
		if !strings.Contains(rejoined.String(), strings.Repeat("x", n+20)) {
			t.Fatalf("lost content across split: %q", parts)
		}
		if !strings.Contains(rejoined.String(), "plain trailing line here") {
			t.Fatalf("lost trailing lines across split: %q", parts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("chunks() did not terminate: infinite loop on an overlong inline fence")
	}
}

// TestChunksInlineFenceDoesNotFlipParity checks that a complete, self-closing
// inline ```x``` span (open and close on the same line) that ends up fully
// inside one piece doesn't get mistaken for an unterminated fence once the
// split lands on a later line.
func TestChunksInlineFenceDoesNotFlipParity(t *testing.T) {
	n := 60
	text := "```inline```\n" + strings.Repeat("plain trailing line here\n", 6)
	if len(text) <= n {
		t.Fatalf("test text too short to force a split: %d bytes", len(text))
	}

	parts := chunks(text, n)
	if len(parts) < 2 {
		t.Fatalf("expected a split, got %d chunk(s)", len(parts))
	}
	for i, p := range parts {
		if strings.Count(p, "```")%2 != 0 {
			t.Fatalf("chunk %d has an unbalanced fence caused by a complete inline ```x``` span:\n%s", i, p)
		}
	}
}
