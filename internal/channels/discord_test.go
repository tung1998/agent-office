package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDiscord(t *testing.T) {
	gw := wsServe(t, func(send func(string)) {
		send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
	}, func(msg string, send func(string)) {
		var p struct {
			Op int `json:"op"`
			D  struct {
				Token   string `json:"token"`
				Intents int    `json:"intents"`
			} `json:"d"`
		}
		json.Unmarshal([]byte(msg), &p)
		if p.Op != 2 || p.D.Token != "TOK" || p.D.Intents&(1<<15) == 0 {
			return
		}
		send(`{"op":0,"s":1,"t":"READY","d":{"user":{"id":"99","username":"shopbot"}}}`)
		send(`{"op":0,"s":2,"t":"MESSAGE_CREATE","d":{"channel_id":"c1","content":"xin chào","author":{"id":"7","username":"an"},"mentions":[]}}`)
		send(`{"op":0,"s":3,"t":"MESSAGE_CREATE","d":{"channel_id":"c2","guild_id":"g","content":"chuyện riêng","author":{"id":"8"},"mentions":[]}}`)
		send(`{"op":0,"s":4,"t":"MESSAGE_CREATE","d":{"channel_id":"c2","guild_id":"g","content":"<@99> đơn 123","author":{"id":"8","username":"binh"},"mentions":[{"id":"99"}]}}`)
		send(`{"op":0,"s":5,"t":"MESSAGE_CREATE","d":{"channel_id":"c2","guild_id":"g","content":"<@99> bot","author":{"id":"5","bot":true},"mentions":[{"id":"99"}]}}`)
		send(`{"op":0,"s":6,"t":"MESSAGE_CREATE","d":{"channel_id":"c2","guild_id":"g","content":"còn đơn 456?","author":{"id":"8","username":"binh"},"mentions":[],"referenced_message":{"author":{"id":"99"}}}}`)
	})
	defer gw.Close()
	var mu sync.Mutex
	var posted []string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot TOK" {
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/messages") {
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			mu.Lock()
			posted = append(posted, r.URL.Path+"|"+b["content"])
			mu.Unlock()
		}
		w.Write([]byte(`{}`))
	}))
	defer rest.Close()
	d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http"), APIBase: rest.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 10)
	bot := make(chan string, 1)
	go d.Run(ctx, func(name string) { bot <- name }, func(m Incoming) { got <- m })
	var msgs []Incoming
	for len(msgs) < 4 {
		select {
		case m := <-got:
			msgs = append(msgs, m)
		case <-ctx.Done():
			t.Fatalf("got %+v", msgs)
		}
	}
	// every message comes up; Addressed says whether it is for the bot (a
	// kept conversation listens to the rest of its chat, ADR-049)
	if <-bot != "shopbot" || !msgs[0].Private || !msgs[0].Addressed || msgs[0].ChatID != "c1" {
		t.Fatalf("dm = %+v", msgs[0])
	}
	if msgs[1].Text != "chuyện riêng" || msgs[1].Addressed {
		t.Fatalf("untagged = %+v", msgs[1])
	}
	if msgs[2].Text != "đơn 123" || !msgs[2].Addressed || msgs[2].UserName != "binh" {
		t.Fatalf("tagged = %+v", msgs[2])
	}
	if msgs[3].Text != "còn đơn 456?" || !msgs[3].Addressed {
		t.Fatalf("a reply to the bot = %+v", msgs[3])
	}
	select {
	case m := <-got:
		t.Fatalf("a bot's own message came up: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}
	if _, err := d.Send(ctx, "c2", strings.Repeat("b", 2500)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posted) != 2 || !strings.HasPrefix(posted[0], "/channels/c2/messages|") {
		t.Fatalf("posted = %d %v", len(posted), posted)
	}
}

// A gateway close that retrying cannot heal (bad token, intents not enabled)
// stops the bot with a reason instead of reconnecting forever.
func TestDiscordStopsOnFatalClose(t *testing.T) {
	for code, want := range map[string]string{"4004": "token", "4014": "Message Content"} {
		gw := wsServe(t, func(send func(string)) {
			send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
		}, func(msg string, send func(string)) { send("CLOSE:" + code) })
		d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http")}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := d.Run(ctx, func(string) {}, func(Incoming) {})
		if ctx.Err() != nil || err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("close %s: err = %v (timed out: %v)", code, err, ctx.Err() != nil)
		}
		cancel()
		gw.Close()
	}
}

// Slash commands (ADR-049): registered on connect, they come down the
// gateway (no public URL); the answer edits the "thinking…" reply.
func TestDiscordSlashCommands(t *testing.T) {
	gw := wsServe(t, func(send func(string)) {
		send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
	}, func(msg string, send func(string)) {
		if !strings.Contains(msg, `"op":2`) {
			return
		}
		send(`{"op":0,"s":1,"t":"READY","d":{"user":{"id":"99","username":"shopbot"},"application":{"id":"app1"}}}`)
		send(`{"op":0,"s":2,"t":"INTERACTION_CREATE","d":{"id":"int1","token":"itok","type":2,"channel_id":"c2","guild_id":"g","data":{"name":"create-conversation"},"member":{"user":{"id":"8","username":"binh"}}}}`)
		send(`{"op":0,"s":3,"t":"MESSAGE_CREATE","d":{"channel_id":"c2","guild_id":"g","content":"/close-conversation","author":{"id":"8","username":"binh"},"mentions":[]}}`)
		send(`{"op":0,"s":4,"t":"INTERACTION_CREATE","d":{"id":"int2","token":"itok2","type":2,"channel_id":"c2","guild_id":"g","data":{"name":"job","options":[{"name":"viec","type":3,"value":"sửa lỗi thanh toán"}]},"member":{"user":{"id":"8","username":"binh"}}}}`)
	})
	defer gw.Close()
	var mu sync.Mutex
	var calls []string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+" "+string(raw))
		mu.Unlock()
		w.Write([]byte(`{}`))
	}))
	defer rest.Close()
	d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http"), APIBase: rest.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 10)
	go d.Run(ctx, func(string) {
		d.SetCommands(ctx, append(Builtins(), Command{Name: "tra-don", Description: "Tra đơn", Arg: "mã đơn"}))
	}, func(m Incoming) { got <- m })
	var msgs []Incoming
	for len(msgs) < 3 {
		select {
		case m := <-got:
			msgs = append(msgs, m)
		case <-ctx.Done():
			t.Fatalf("got %+v", msgs)
		}
	}
	if msgs[2].Text != "/job sửa lỗi thanh toán" {
		t.Fatalf("job command = %+v", msgs[2])
	}
	if msgs[0].Text != "/create-conversation" || msgs[0].ChatID != "c2" || msgs[0].UserID != "8" || msgs[0].Respond == nil || !msgs[0].Addressed {
		t.Fatalf("command = %+v", msgs[0])
	}
	if msgs[1].Text != "/close-conversation" { // typed without tagging the bot
		t.Fatalf("typed command = %+v", msgs[1])
	}
	if _, err := msgs[0].Respond(ctx, "Đã bắt đầu"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(calls, "\n")
	for _, want := range []string{"PUT /applications/app1/commands", "create-conversation", `"required":true`, `"name":"tra-don"`, `"description":"mã đơn"`, "POST /interactions/int1/itok/callback", `"type":5`,
		"PATCH /webhooks/app1/itok/messages/@original", "Đã bắt đầu"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in:\n%s", want, all)
		}
	}
	// /create-thread takes the thread's name after it, which may be left out;
	// /mode is gone (ADR-081)
	if !strings.Contains(all, `"description":"tên thread","name":`) && !strings.Contains(all, `"description":"tên thread"`) || !strings.Contains(all, `"required":false`) || strings.Contains(all, `"name":"mode"`) {
		t.Errorf("create-thread name / mode in:\n%s", all)
	}
}

// A message with a thread already: that thread (its id is the message's);
// other refusals keep Discord's code and reason.
func TestDiscordThreadExists(t *testing.T) {
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		if strings.Contains(r.URL.Path, "/messages/m1/") {
			w.Write([]byte(`{"code":160004,"message":"A thread has already been created for this message"}`))
			return
		}
		w.Write([]byte(`{"code":50024,"message":"Cannot execute action on this channel type"}`))
	}))
	defer rest.Close()
	d := &Discord{Token: "TOK", APIBase: rest.URL}
	ctx := context.Background()
	if id, err := d.MakeThread(ctx, "c1", "m1", "x"); err != nil || id != "m1" {
		t.Fatalf("id = %q, err = %v", id, err)
	}
	_, err := d.MakeThread(ctx, "c1", "m2", "x")
	if discordCode(err) != 50024 || !strings.Contains(err.Error(), "Cannot execute action") {
		t.Fatalf("err = %v", err)
	}
}

// Discord takes descriptions of at most 100 characters.
func TestDiscordLongDescription(t *testing.T) {
	var body string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Write([]byte(`[]`))
	}))
	defer rest.Close()
	d := &Discord{Token: "TOK", APIBase: rest.URL, appID: "app1"}
	d.SetCommands(context.Background(), []Command{{Name: "review", Description: strings.Repeat("dài ", 60), Arg: strings.Repeat("x", 150)}})
	var cmds []struct {
		Description string `json:"description"`
		Options     []struct {
			Description string `json:"description"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(body), &cmds); err != nil || len(cmds) != 1 {
		t.Fatalf("body = %s", body)
	}
	if n := len([]rune(cmds[0].Description)); n > 100 || n == 0 || len([]rune(cmds[0].Options[0].Description)) > 100 {
		t.Fatalf("description %d runes", n)
	}
}

// Discord takes at most 100 commands: more would lose them all, the office's own first.
func TestDiscordCommandCap(t *testing.T) {
	var body string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Write([]byte(`[]`))
	}))
	defer rest.Close()
	d := &Discord{Token: "TOK", APIBase: rest.URL, appID: "app1"}
	cmds := Builtins()
	for i := range 150 {
		cmds = append(cmds, Command{Name: fmt.Sprintf("s%d", i), Description: "x"})
	}
	d.SetCommands(context.Background(), cmds)
	var all []struct {
		Name string
		Type int
	}
	json.Unmarshal([]byte(body), &all)
	var got []string // the "/" commands (a message menu entry is counted apart by Discord)
	for _, c := range all {
		if c.Type == 1 {
			got = append(got, c.Name)
		}
	}
	if len(got) != 100 || got[0] != "create-conversation" {
		t.Fatalf("registered %d, first %v", len(got), got[:1])
	}
}

// Threads: the bot joins a new one (to hear it) and says which message it
// grew from; "X started a thread" (a system message) is not a message to answer.
func TestDiscordThreads(t *testing.T) {
	gw := wsServe(t, func(send func(string)) {
		send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
	}, func(msg string, send func(string)) {
		var p struct {
			Op int `json:"op"`
			D  struct {
				Intents int `json:"intents"`
			} `json:"d"`
		}
		json.Unmarshal([]byte(msg), &p)
		if p.Op != 2 || p.D.Intents&1 == 0 { // GUILDS: thread events
			return
		}
		send(`{"op":0,"s":1,"t":"READY","d":{"user":{"id":"99","username":"shopbot"}}}`)
		send(`{"op":0,"s":2,"t":"MESSAGE_CREATE","d":{"id":"m1","type":18,"channel_id":"c2","guild_id":"g","content":"đọc giúp tôi repo","author":{"id":"8"},"mentions":[]}}`)
		send(`{"op":0,"s":3,"t":"THREAD_CREATE","d":{"id":"m0","guild_id":"g","parent_id":"c2","newly_created":true,"name":"đọc giúp tôi repo"}}`)
		send(`{"op":0,"s":4,"t":"MESSAGE_CREATE","d":{"id":"m2","type":0,"channel_id":"m0","guild_id":"g","content":"<@99> ở đây đọc được không","author":{"id":"8","username":"binh"},"mentions":[{"id":"99"}]}}`)
	})
	defer gw.Close()
	joined := make(chan string, 2)
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/thread-members/@me") {
			joined <- r.URL.Path
		}
		w.Write([]byte(`{}`))
	}))
	defer rest.Close()
	d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http"), APIBase: rest.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 10)
	go d.Run(ctx, func(string) {}, func(m Incoming) { got <- m })
	var msgs []Incoming
	for len(msgs) < 2 {
		select {
		case m := <-got:
			msgs = append(msgs, m)
		case <-ctx.Done():
			t.Fatalf("got %+v", msgs)
		}
	}
	if msgs[0].ThreadOf != "m0" || msgs[0].ChatID != "m0" || msgs[0].GuildID != "g" || msgs[0].Text != "" {
		t.Fatalf("thread created = %+v", msgs[0])
	}
	if msgs[1].ChatID != "m0" || msgs[1].MessageID != "m2" || msgs[1].GuildID != "g" || !msgs[1].Addressed {
		t.Fatalf("in the thread = %+v", msgs[1])
	}
	select {
	case p := <-joined:
		if p != "/channels/m0/thread-members/@me" {
			t.Fatalf("joined %s", p)
		}
	case <-ctx.Done():
		t.Fatal("the bot did not join the thread")
	}
}

// "Apps → Create thread" on a message: that message is the one the thread is
// made from; the menu entry is registered with the commands.
func TestDiscordThreadMenu(t *testing.T) {
	gw := wsServe(t, func(send func(string)) {
		send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
	}, func(msg string, send func(string)) {
		if !strings.Contains(msg, `"op":2`) {
			return
		}
		send(`{"op":0,"s":1,"t":"READY","d":{"user":{"id":"99","username":"shopbot"},"application":{"id":"app1"}}}`)
		send(`{"op":0,"s":2,"t":"INTERACTION_CREATE","d":{"id":"int1","token":"itok","type":2,"channel_id":"c2","guild_id":"g","data":{"name":"Create thread","type":3,"target_id":"m7"},"member":{"user":{"id":"8","username":"binh"}}}}`)
	})
	defer gw.Close()
	var mu sync.Mutex
	var calls []string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+" "+string(raw))
		mu.Unlock()
		w.Write([]byte(`{}`))
	}))
	defer rest.Close()
	d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http"), APIBase: rest.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 2)
	go d.Run(ctx, func(string) { d.SetCommands(ctx, Builtins()) }, func(m Incoming) { got <- m })
	var m Incoming
	select {
	case m = <-got:
	case <-ctx.Done():
		t.Fatal("no interaction")
	}
	if m.Text != "/create-thread" || m.ReplyTo != "m7" || m.GuildID != "g" || m.Respond == nil {
		t.Fatalf("menu = %+v", m)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if all := strings.Join(calls, "\n"); !strings.Contains(all, `"name":"Create thread"`) || !strings.Contains(all, `"type":3`) {
		t.Fatalf("menu entry not registered:\n%s", all)
	}
}

// Threads open before the bot connected (or made while it was down) are
// joined when the guild comes up, so a tag in them is heard.
func TestDiscordJoinsOpenThreads(t *testing.T) {
	gw := wsServe(t, func(send func(string)) {
		send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
	}, func(msg string, send func(string)) {
		if !strings.Contains(msg, `"op":2`) {
			return
		}
		send(`{"op":0,"s":1,"t":"READY","d":{"user":{"id":"99","username":"shopbot"}}}`)
		send(`{"op":0,"s":2,"t":"GUILD_CREATE","d":{"id":"g","threads":[{"id":"t1","parent_id":"c2"},{"id":"t2","parent_id":"c2","member":{"user_id":"99"}}]}}`)
	})
	defer gw.Close()
	joined := make(chan string, 4)
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/thread-members/@me") {
			joined <- r.URL.Path
		}
		w.Write([]byte(`{}`))
	}))
	defer rest.Close()
	d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http"), APIBase: rest.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go d.Run(ctx, func(string) {}, func(Incoming) {})
	select {
	case p := <-joined:
		if p != "/channels/t1/thread-members/@me" {
			t.Fatalf("joined %s", p)
		}
	case <-ctx.Done():
		t.Fatal("an open thread was not joined")
	}
	select {
	case p := <-joined:
		t.Fatalf("joined a thread it is in already: %s", p)
	case <-time.After(300 * time.Millisecond):
	}
}

// A message in a thread the bot knows of (made, or open when it connected) says so.
func TestDiscordMarksThreadMessages(t *testing.T) {
	gw := wsServe(t, func(send func(string)) {
		send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
	}, func(msg string, send func(string)) {
		if !strings.Contains(msg, `"op":2`) {
			return
		}
		send(`{"op":0,"s":1,"t":"READY","d":{"user":{"id":"99","username":"shopbot"}}}`)
		send(`{"op":0,"s":2,"t":"GUILD_CREATE","d":{"id":"g","threads":[{"id":"t1","parent_id":"c2","member":{"user_id":"99"}}]}}`)
		send(`{"op":0,"s":3,"t":"THREAD_CREATE","d":{"id":"t2","guild_id":"g","parent_id":"c2","newly_created":true}}`)
		send(`{"op":0,"s":4,"t":"MESSAGE_CREATE","d":{"id":"a","channel_id":"t1","guild_id":"g","content":"<@99> một","author":{"id":"8"},"mentions":[{"id":"99"}]}}`)
		send(`{"op":0,"s":5,"t":"MESSAGE_CREATE","d":{"id":"b","channel_id":"t2","guild_id":"g","content":"<@99> hai","author":{"id":"8"},"mentions":[{"id":"99"}]}}`)
		send(`{"op":0,"s":6,"t":"MESSAGE_CREATE","d":{"id":"c","channel_id":"c2","guild_id":"g","content":"<@99> ba","author":{"id":"8"},"mentions":[{"id":"99"}]}}`)
	})
	defer gw.Close()
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer rest.Close()
	d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http"), APIBase: rest.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 10)
	go d.Run(ctx, func(string) {}, func(m Incoming) {
		if m.ThreadOf == "" {
			got <- m
		}
	})
	in := map[string]bool{}
	for len(in) < 3 {
		select {
		case m := <-got:
			in[m.Text] = m.InThread
		case <-ctx.Done():
			t.Fatalf("got %v", in)
		}
	}
	if !in["một"] || !in["hai"] || in["ba"] {
		t.Fatalf("in a thread = %v", in)
	}
}

// Tagging the bot's own role (Discord offers it next to the bot, same name)
// is tagging the bot.
func TestDiscordBotRoleTag(t *testing.T) {
	gw := wsServe(t, func(send func(string)) {
		send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
	}, func(msg string, send func(string)) {
		if !strings.Contains(msg, `"op":2`) {
			return
		}
		send(`{"op":0,"s":1,"t":"READY","d":{"user":{"id":"99","username":"shopbot"}}}`)
		send(`{"op":0,"s":2,"t":"GUILD_CREATE","d":{"id":"g","roles":[{"id":"r1","tags":{"bot_id":"99"}},{"id":"r2"}],"threads":[]}}`)
		send(`{"op":0,"s":3,"t":"MESSAGE_CREATE","d":{"id":"a","channel_id":"c2","guild_id":"g","content":"<@&r1> xin chào","author":{"id":"8"},"mentions":[],"mention_roles":["r1"]}}`)
		send(`{"op":0,"s":4,"t":"MESSAGE_CREATE","d":{"id":"b","channel_id":"c2","guild_id":"g","content":"<@&r2> mọi người","author":{"id":"8"},"mentions":[],"mention_roles":["r2"]}}`)
	})
	defer gw.Close()
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer rest.Close()
	d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http"), APIBase: rest.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 10)
	go d.Run(ctx, func(string) {}, func(m Incoming) { got <- m })
	var msgs []Incoming
	for len(msgs) < 2 {
		select {
		case m := <-got:
			msgs = append(msgs, m)
		case <-ctx.Done():
			t.Fatalf("got %+v", msgs)
		}
	}
	if !msgs[0].Addressed || msgs[0].Text != "xin chào" {
		t.Fatalf("the bot's role = %+v", msgs[0])
	}
	if msgs[1].Addressed {
		t.Fatalf("another role = %+v", msgs[1])
	}
}

// Where a Telegram chat opens: a supergroup's message, a private chat with the
// bot; a basic group has no link.
func TestTelegramURL(t *testing.T) {
	for _, c := range []struct{ bot, chat, msg, want string }{
		{"shop_bot", "-1001234567890", "42", "https://t.me/c/1234567890/42"},
		{"shop_bot", "399661258", "7", "https://t.me/shop_bot"},
		{"shop_bot", "-4567", "7", ""},
	} {
		if got := telegramURL(c.bot, c.chat, c.msg); got != c.want {
			t.Errorf("%v: %q", c, got)
		}
	}
}

// Files sent to the bot come up with the message (a photo alone too), each
// fetched when office needs it.
func TestDiscordAttachments(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("PNGDATA")) }))
	defer cdn.Close()
	gw := wsServe(t, func(send func(string)) {
		send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
	}, func(msg string, send func(string)) {
		if !strings.Contains(msg, `"op":2`) {
			return
		}
		send(`{"op":0,"s":1,"t":"READY","d":{"user":{"id":"99","username":"shopbot"}}}`)
		send(`{"op":0,"s":2,"t":"MESSAGE_CREATE","d":{"id":"a","channel_id":"c1","content":"","author":{"id":"8"},"mentions":[],"attachments":[{"filename":"lỗi.png","size":7,"url":"` + cdn.URL + `/x.png"}]}}`)
	})
	defer gw.Close()
	d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http"), APIBase: cdn.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 2)
	go d.Run(ctx, func(string) {}, func(m Incoming) { got <- m })
	var m Incoming
	select {
	case m = <-got:
	case <-ctx.Done():
		t.Fatal("a file alone did not come up")
	}
	if len(m.Files) != 1 || m.Files[0].Name != "lỗi.png" || m.Files[0].Size != 7 {
		t.Fatalf("files = %+v", m.Files)
	}
	if data, err := m.Files[0].Fetch(ctx); err != nil || string(data) != "PNGDATA" {
		t.Fatalf("fetch = %q %v", data, err)
	}
}

// Telegram: a photo (its largest size) or a document, fetched through getFile.
func TestTelegramAttachments(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botTOK/getMe":
			w.Write([]byte(`{"ok":true,"result":{"username":"shop_bot"}}`))
		case "/botTOK/getUpdates":
			if r.URL.Query().Get("offset") != "" || strings.Contains(r.Header.Get("X-Seen"), "1") {
				time.Sleep(50 * time.Millisecond)
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if off, _ := body["offset"].(float64); off > 0 {
				w.Write([]byte(`{"ok":true,"result":[]}`))
				return
			}
			w.Write([]byte(`{"ok":true,"result":[{"update_id":1,"message":{"message_id":5,"caption":"xem ảnh","chat":{"id":42,"type":"private"},"from":{"id":7},
				"photo":[{"file_id":"small","file_size":10},{"file_id":"big","file_size":90}]}}]}`))
		case "/botTOK/getFile":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			w.Write([]byte(`{"ok":true,"result":{"file_path":"photos/` + body["file_id"] + `.jpg"}}`))
		case "/file/botTOK/photos/big.jpg":
			w.Write([]byte("JPEG"))
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
		t.Fatal("no message")
	}
	if m.Text != "xem ảnh" || len(m.Files) != 1 || m.Files[0].Size != 90 {
		t.Fatalf("message = %+v", m)
	}
	if data, err := m.Files[0].Fetch(ctx); err != nil || string(data) != "JPEG" {
		t.Fatalf("fetch = %q %v", data, err)
	}
}

// Buttons under a message; a click comes up as the command it stands for.
func TestDiscordButtons(t *testing.T) {
	gw := wsServe(t, func(send func(string)) {
		send(`{"op":10,"d":{"heartbeat_interval":45000}}`)
	}, func(msg string, send func(string)) {
		if !strings.Contains(msg, `"op":2`) {
			return
		}
		send(`{"op":0,"s":1,"t":"READY","d":{"user":{"id":"99","username":"shopbot"},"application":{"id":"app1"}}}`)
		send(`{"op":0,"s":2,"t":"INTERACTION_CREATE","d":{"id":"i1","token":"tk","type":3,"channel_id":"c2","guild_id":"g","data":{"custom_id":"office:/approve 2","component_type":2},"message":{"id":"m7"},"member":{"user":{"id":"8","username":"binh"}}}}`)
	})
	defer gw.Close()
	var mu sync.Mutex
	var calls []string
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path+" "+string(raw))
		mu.Unlock()
		w.Write([]byte(`{"id":"m1"}`))
	}))
	defer rest.Close()
	d := &Discord{Token: "TOK", GatewayURL: "ws" + strings.TrimPrefix(gw.URL, "http"), APIBase: rest.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := make(chan Incoming, 2)
	go d.Run(ctx, func(string) {}, func(m Incoming) { got <- m })
	var m Incoming
	select {
	case m = <-got:
	case <-ctx.Done():
		t.Fatal("no click")
	}
	if m.Text != "/approve 2" || m.UserID != "8" || !m.Addressed || m.Respond == nil || m.ButtonMsg != "m7" {
		t.Fatalf("click = %+v", m)
	}
	if _, err := d.SendButtons(ctx, "c2", "Chờ duyệt", [][]Button{{{Label: "Duyệt 1", Data: "/approve 1"}, {Label: "Từ chối 1", Data: "/reject 1", Danger: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := d.EditButtons(ctx, "c2", "m7", "✅ Đã duyệt 2", nil); err != nil { // decided: its buttons off
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(calls, "\n")
	for _, want := range []string{`"custom_id":"office:/approve 1"`, `"style":4`, `"type":1`, `Chờ duyệt`, `PATCH /channels/c2/messages/m7 {"components":[],"content":"✅ Đã duyệt 2"}`} {
		if !strings.Contains(all, want) {
			t.Errorf("no %s in\n%s", want, all)
		}
	}
}

// 👀 on a message, a status message edited then removed: the REST calls.
func TestLiveMarks(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.EscapedPath()+" "+string(raw))
		mu.Unlock()
		w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer srv.Close()
	ctx := context.Background()
	d := &Discord{Token: "TOK", APIBase: srv.URL}
	d.React(ctx, "c2", "m1", "👀", true)
	d.React(ctx, "c2", "m1", "👀", false)
	d.Edit(ctx, "c2", "m9", "⏳ bước 2")
	d.Delete(ctx, "c2", "m9")
	tg := &Telegram{Token: "TOK", BaseURL: srv.URL}
	tg.React(ctx, "42", "5", "👀", true)
	tg.Delete(ctx, "42", "6")
	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(calls, "\n")
	for _, want := range []string{"PUT /channels/c2/messages/m1/reactions/%F0%9F%91%80/@me", "DELETE /channels/c2/messages/m1/reactions/%F0%9F%91%80/@me",
		"PATCH /channels/c2/messages/m9", "DELETE /channels/c2/messages/m9", `setMessageReaction {"chat_id":42,"message_id":5,"reaction":[{"emoji":"👀","type":"emoji"}]}`, "deleteMessage"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
}
