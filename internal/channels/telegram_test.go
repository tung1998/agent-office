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

// Review C1: a tag after characters that grow when lowercased never panics.
func TestTelegramMentionUnicode(t *testing.T) {
	tg := &Telegram{bot: "shop_bot"}
	for _, text := range []string{"ȺȺȺ @shop_bot đơn 1", "@SHOP_BOT ẞ hỏi", "İİİİ @Shop_Bot"} {
		m := &tgMessage{Text: text}
		m.Chat.Type = "group"
		in, ok := tg.addressed(m)
		if !ok || strings.Contains(strings.ToLower(in.Text), "@shop_bot") {
			t.Errorf("%q → %q %v", text, in.Text, ok)
		}
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

// A network hiccup when the bot starts is retried; only a wrong token stops it.
func TestTelegramRetriesGetMe(t *testing.T) {
	var mu sync.Mutex
	tries := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			mu.Lock()
			tries++
			n := tries
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(502)
				return
			}
			w.Write([]byte(`{"ok":true,"result":{"username":"shop_bot"}}`))
			return
		}
		time.Sleep(20 * time.Millisecond)
		w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()
	tg := &Telegram{Token: "TOK", BaseURL: srv.URL, retry: 10 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ready := make(chan string, 1)
	go tg.Run(ctx, func(n string) { ready <- n }, func(Incoming) {})
	select {
	case n := <-ready:
		if n != "shop_bot" {
			t.Fatalf("bot = %q", n)
		}
	case <-ctx.Done():
		t.Fatal("a failed getMe was not retried")
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
