package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Discord is a bot on the Discord Gateway: it answers direct messages and
// messages that tag it.
type Discord struct {
	Token      string
	GatewayURL string // "" = wss://gateway.discord.gg/?v=10&encoding=json
	APIBase    string // "" = https://discord.com/api/v10
	client     http.Client
	idMu       sync.RWMutex // guards botID/appID: set from the read loop, read from goroutines a reconnect may outlive (SetCommands, interaction replies)
	botID      string
	appID      string // the application: its commands, its interactions' replies
	menu       menu
	threads    sync.Map // thread ids it knows of (made, open when it connected): their messages are InThread
	botRoles   sync.Map // the bot's own roles (managed, one per server): tagging one tags the bot
}

// setIdentity records who the bot is (from READY), replacing a reconnect's
// stale value atomically with respect to identity() readers.
func (d *Discord) setIdentity(botID, appID string) {
	d.idMu.Lock()
	d.botID, d.appID = botID, appID
	d.idMu.Unlock()
}

// identity is who the bot is, as last set by setIdentity.
func (d *Discord) identity() (botID, appID string) {
	d.idMu.RLock()
	defer d.idMu.RUnlock()
	return d.botID, d.appID
}

// intents: guilds (threads made), guild messages, direct messages, message content
const discordIntents = 1<<0 | 1<<9 | 1<<12 | 1<<15

type gwPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int64          `json:"s"`
	T  string          `json:"t"`
}

// Run keeps a gateway session up (reconnecting) until ctx ends.
func (d *Discord) Run(ctx context.Context, onReady func(string), onMessage func(Incoming)) error {
	backoff := time.Second
	for ctx.Err() == nil {
		ready, err := d.session(ctx, onReady, onMessage)
		if ctx.Err() != nil {
			return nil
		}
		var ce *CloseError
		if errors.As(err, &ce) {
			if why, fatal := discordFatal[ce.Code]; fatal {
				return errors.New("discord: " + why) // retrying cannot heal these
			}
		}
		if ready {
			backoff = time.Second // a session that worked: a quick reconnect
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
	return nil
}

// discordFatal are the gateway close codes a reconnect does not fix.
var discordFatal = map[int]string{
	4004: "token không hợp lệ",
	4010: "shard không hợp lệ",
	4011: "bot cần sharding",
	4012: "phiên bản gateway không hợp lệ",
	4013: "intents không hợp lệ",
	4014: "bot chưa được bật Message Content Intent (Developer Portal → Bot → Privileged Gateway Intents)",
}

func (d *Discord) session(ctx context.Context, onReady func(string), onMessage func(Incoming)) (ready bool, err error) {
	url := d.GatewayURL
	if url == "" {
		url = "wss://gateway.discord.gg/?v=10&encoding=json"
	}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	c, err := dialWS(dctx, url)
	cancel()
	if err != nil {
		return false, err
	}
	sctx, end := context.WithCancel(ctx)
	defer end()
	go func() { <-sctx.Done(); c.Close() }() // ctx ending unblocks Read; the session ending frees this
	var seq atomic.Int64
	seq.Store(-1)
	next := func() (gwPayload, error) {
		raw, err := c.Read()
		if err != nil {
			return gwPayload{}, err
		}
		var p gwPayload
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return gwPayload{}, err
		}
		if p.S != nil {
			seq.Store(*p.S)
		}
		return p, nil
	}
	hello, err := next()
	if err != nil {
		return false, err
	}
	if hello.Op != 10 {
		return false, fmt.Errorf("discord: expected hello, got op %d", hello.Op)
	}
	var h struct {
		Interval int `json:"heartbeat_interval"`
	}
	_ = json.Unmarshal(hello.D, &h)
	if h.Interval <= 0 {
		h.Interval = 41250
	}
	hbCtx, stop := context.WithCancel(sctx)
	defer stop()
	go func() {
		tick := time.NewTicker(time.Duration(h.Interval) * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-tick.C:
				s := seq.Load()
				beat := `{"op":1,"d":null}`
				if s >= 0 {
					beat = fmt.Sprintf(`{"op":1,"d":%d}`, s)
				}
				if c.WriteText(beat) != nil {
					return
				}
			}
		}
	}()
	identify, _ := json.Marshal(map[string]any{"op": 2, "d": map[string]any{"token": d.Token, "intents": discordIntents,
		"properties": map[string]string{"os": "linux", "browser": "agent-office", "device": "agent-office"}}})
	if err := c.WriteText(string(identify)); err != nil {
		return false, err
	}
	for {
		p, err := next()
		if err != nil {
			return ready, err
		}
		switch p.Op {
		case 7, 9: // reconnect, invalid session
			return ready, errors.New("discord: gateway asked to reconnect")
		case 1:
			_ = c.WriteText(fmt.Sprintf(`{"op":1,"d":%d}`, seq.Load()))
		case 0:
			switch p.T {
			case "READY":
				var r struct {
					User struct {
						ID       string `json:"id"`
						Username string `json:"username"`
					} `json:"user"`
					Application struct {
						ID string `json:"id"`
					} `json:"application"`
				}
				_ = json.Unmarshal(p.D, &r)
				d.setIdentity(r.User.ID, r.Application.ID)
				ready = true
				onReady(r.User.Username)
			case "INTERACTION_CREATE":
				if m, ok := d.interaction(ctx, p.D); ok {
					onMessage(m)
				}
			case "MESSAGE_CREATE":
				if m, ok := d.addressed(p.D); ok {
					onMessage(m)
				}
			case "THREAD_CREATE":
				if m, ok := d.threadMade(ctx, p.D); ok {
					onMessage(m)
				}
			case "THREAD_UPDATE": // an archived thread back in use
				var th struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(p.D, &th) == nil && th.ID != "" {
					d.threads.Store(th.ID, true)
				}
			case "GUILD_CREATE": // the open threads: joined, so what is said there is heard
				var g struct {
					Threads []struct {
						ID string `json:"id"`
					} `json:"threads"`
					Roles []struct {
						ID   string `json:"id"`
						Tags *struct {
							BotID string `json:"bot_id"`
						} `json:"tags"`
					} `json:"roles"`
				}
				if json.Unmarshal(p.D, &g) == nil { // known before the next message is read
					botID, _ := d.identity()
					for _, th := range g.Threads {
						d.threads.Store(th.ID, true)
					}
					for _, r := range g.Roles {
						if r.Tags != nil && r.Tags.BotID != "" && r.Tags.BotID == botID {
							d.botRoles.Store(r.ID, true)
						}
					}
				}
				go d.joinOpen(context.WithoutCancel(ctx), p.D, onMessage)
			}
		}
	}
}

// addressed: a direct message, or one that tags the bot; never a bot's own.
func (d *Discord) addressed(raw json.RawMessage) (Incoming, bool) {
	var m struct {
		ID        string `json:"id"`
		Type      int    `json:"type"`
		ChannelID string `json:"channel_id"`
		GuildID   string `json:"guild_id"`
		Content   string `json:"content"`
		Author    struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Bot      bool   `json:"bot"`
		} `json:"author"`
		Mentions []struct {
			ID string `json:"id"`
		} `json:"mentions"`
		MentionRoles []string `json:"mention_roles"`
		Attachments  []struct {
			Filename string `json:"filename"`
			Size     int64  `json:"size"`
			URL      string `json:"url"`
		} `json:"attachments"`
		Replied *struct {
			ID     string `json:"id"`
			Author struct {
				ID string `json:"id"`
			} `json:"author"`
		} `json:"referenced_message"`
	}
	botID, _ := d.identity()
	if json.Unmarshal(raw, &m) != nil || m.Author.Bot || m.Author.ID == botID {
		return Incoming{}, false
	}
	if m.Type != 0 && m.Type != 19 { // a person's message or reply; not "X started a thread", a pin, a join…
		return Incoming{}, false
	}
	in := Incoming{ChatID: m.ChannelID, UserID: m.Author.ID, UserName: m.Author.Username, Private: m.GuildID == "", MessageID: m.ID, GuildID: m.GuildID}
	_, in.InThread = d.threads.Load(m.ChannelID)
	for _, a := range m.Attachments {
		url := a.URL
		in.Files = append(in.Files, InFile{Name: a.Filename, Size: a.Size, Fetch: func(ctx context.Context) ([]byte, error) {
			return fetchURL(ctx, &d.client, url)
		}})
	}
	tagged := false
	for _, x := range m.Mentions {
		if x.ID == botID && botID != "" {
			tagged = true
		}
	}
	var roles []string
	for _, r := range m.MentionRoles {
		if _, mine := d.botRoles.Load(r); mine { // its own role: Discord offers it by the bot's name
			tagged = true
			roles = append(roles, "<@&"+r+">", "")
		}
	}
	replied := m.Replied != nil && botID != "" && m.Replied.Author.ID == botID
	if replied {
		in.ReplyTo = m.Replied.ID
	}
	// every message comes up: Addressed = for the bot (a DM, a tag, a reply to
	// it, a command typed without a tag); a kept conversation hears the rest
	in.Addressed = in.Private || tagged || replied || d.menu.has(m.Content)
	text := m.Content
	if botID != "" {
		text = strings.NewReplacer(append([]string{"<@" + botID + ">", "", "<@!" + botID + ">", ""}, roles...)...).Replace(text)
	}
	in.Text = strings.TrimSpace(text)
	return in, in.Text != "" || len(in.Files) > 0
}

// threadMade: a new thread. The bot joins it (to hear what is said there);
// made from a message, its id is that message's.
func (d *Discord) threadMade(ctx context.Context, raw json.RawMessage) (Incoming, bool) {
	var th struct {
		ID           string `json:"id"`
		GuildID      string `json:"guild_id"`
		ParentID     string `json:"parent_id"`
		NewlyCreated bool   `json:"newly_created"`
	}
	if json.Unmarshal(raw, &th) != nil || th.ID == "" {
		return Incoming{}, false
	}
	d.threads.Store(th.ID, true)
	if !th.NewlyCreated {
		return Incoming{}, false
	}
	slog.Info("discord: thread made", "thread", th.ID, "parent", th.ParentID)
	go func() {
		if err := d.do(context.WithoutCancel(ctx), "PUT", "/channels/"+th.ID+"/thread-members/@me", nil); err != nil {
			slog.Warn("discord: not in the thread", "thread", th.ID, "err", err)
		}
	}()
	return Incoming{ChatID: th.ID, GuildID: th.GuildID, ThreadOf: th.ID, ParentID: th.ParentID}, true
}

// MakeThread opens a public thread: from a message (the thread takes its id),
// or on its own in chatID; a day without messages archives it.
func (d *Discord) MakeThread(ctx context.Context, chatID, fromMsg, name string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	body := map[string]any{"name": name, "auto_archive_duration": 1440}
	path := "/channels/" + chatID + "/threads"
	if fromMsg != "" {
		path = "/channels/" + chatID + "/messages/" + fromMsg + "/threads"
	} else {
		body["type"] = 11 // public thread
	}
	if err := d.do(ctx, "POST", path, body, &out); err != nil {
		if fromMsg != "" && discordCode(err) == 160004 { // the message has a thread already: it is that one
			return fromMsg, nil
		}
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("discord: no thread id")
	}
	return out.ID, nil
}

// threadMenu is the entry of a message's "Apps" menu that makes a thread from it.
const threadMenu = "Create thread"

// joinOpen joins the guild's open threads the bot is not in (made before it
// connected, or while it was down), one at a time: Discord's rate limits.
func (d *Discord) joinOpen(ctx context.Context, raw json.RawMessage, onMessage func(Incoming)) {
	var g struct {
		ID      string `json:"id"`
		Threads []struct {
			ID       string `json:"id"`
			ParentID string `json:"parent_id"`
			Member   *struct {
				UserID string `json:"user_id"`
			} `json:"member"`
		} `json:"threads"`
	}
	if json.Unmarshal(raw, &g) != nil {
		return
	}
	slog.Info("discord: open threads", "guild", g.ID, "threads", len(g.Threads))
	for _, th := range g.Threads {
		if th.ID == "" {
			continue
		}
		d.threads.Store(th.ID, true)
		// each goes on with the conversation of the message it grew from, if office knows it
		onMessage(Incoming{ChatID: th.ID, GuildID: g.ID, ThreadOf: th.ID, ParentID: th.ParentID})
		if th.Member != nil { // in it already
			continue
		}
		if err := d.do(ctx, "PUT", "/channels/"+th.ID+"/thread-members/@me", nil); err != nil {
			slog.Warn("discord: not in the thread", "thread", th.ID, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// SetCommands puts the bot's slash commands in Discord's "/" menu (after
// READY: it needs the application's id).
func (d *Discord) SetCommands(ctx context.Context, cmds []Command) {
	cmds = capped(cmds)
	d.menu.set(cmds)
	_, appID := d.identity()
	if appID == "" {
		return
	}
	var list []map[string]any
	for _, c := range cmds {
		cmd := map[string]any{"name": c.Name, "description": clip(firstNonEmpty(c.Description, c.Name), 100), "type": 1, "contexts": []int{0, 1, 2}}
		if c.Arg != "" { // the text after the command (a string; required unless Optional)
			cmd["options"] = []map[string]any{{"type": 3, "name": argName(c.Arg), "description": clip(c.Arg, 100), "required": !c.Optional}}
		}
		list = append(list, cmd)
	}
	for _, c := range cmds {
		if c.Name == "create-thread" { // and in a message's Apps menu: the thread grows from that message
			list = append(list, map[string]any{"name": threadMenu, "type": 3, "contexts": []int{0}})
		}
	}
	if err := d.do(ctx, "PUT", "/applications/"+appID+"/commands", list); err != nil {
		slog.Warn("discord: slash commands not registered", "err", err)
	}
}

// interaction is a slash command: acknowledged at once ("thinking…"), its
// answer edits that reply.
func (d *Discord) interaction(ctx context.Context, raw json.RawMessage) (Incoming, bool) {
	type user struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	var x struct {
		ID        string `json:"id"`
		Token     string `json:"token"`
		Type      int    `json:"type"`
		ChannelID string `json:"channel_id"`
		GuildID   string `json:"guild_id"`
		Data      struct {
			Name     string `json:"name"`
			Type     int    `json:"type"`      // 3: a message's "Apps" menu
			TargetID string `json:"target_id"` // …on that message
			CustomID string `json:"custom_id"` // a button pressed (interaction type 3)
			Options  []struct {
				Value any `json:"value"`
			} `json:"options"`
		} `json:"data"`
		Member *struct {
			User user `json:"user"`
		} `json:"member"`
		User    *user `json:"user"`
		Message *struct {
			ID string `json:"id"`
		} `json:"message"` // the message whose button was pressed
	}
	if json.Unmarshal(raw, &x) != nil || (x.Type != 2 && x.Type != 3) || x.Token == "" {
		return Incoming{}, false
	}
	if x.Type == 3 && !strings.HasPrefix(x.Data.CustomID, buttonPrefix) { // a button, not ours
		return Incoming{}, false
	}
	if err := d.do(ctx, "POST", "/interactions/"+x.ID+"/"+x.Token+"/callback", map[string]any{"type": 5}); err != nil {
		return Incoming{}, false
	}
	in := Incoming{ChatID: x.ChannelID, Text: "/" + x.Data.Name, Private: x.GuildID == "", Addressed: true, GuildID: x.GuildID}
	_, in.InThread = d.threads.Load(x.ChannelID)
	if x.Data.Type == 3 && x.Data.Name == threadMenu { // Apps → Create thread, on one message
		in.Text, in.ReplyTo = "/create-thread", x.Data.TargetID
	}
	if x.Type == 3 { // a button: the command it stands for
		in.Text = strings.TrimPrefix(x.Data.CustomID, buttonPrefix)
		if x.Message != nil {
			in.ButtonMsg = x.Message.ID
		}
	}
	for _, o := range x.Data.Options {
		if s, ok := o.Value.(string); ok {
			in.Text += " " + s
		}
	}
	if x.Member != nil {
		in.UserID, in.UserName = x.Member.User.ID, x.Member.User.Username
	} else if x.User != nil {
		in.UserID, in.UserName = x.User.ID, x.User.Username
	}
	_, app := d.identity()
	token := x.Token
	in.Respond = func(ctx context.Context, text string) (string, error) {
		var sent struct {
			ID string `json:"id"`
		}
		err := d.do(ctx, "PATCH", "/webhooks/"+app+"/"+token+"/messages/@original", map[string]string{"content": text}, &sent)
		return sent.ID, err
	}
	return in, true
}

func (d *Discord) rest(ctx context.Context, path string, body any) error {
	return d.do(ctx, "POST", path, body, nil)
}

func (d *Discord) do(ctx context.Context, method, path string, body any, out ...any) error {
	base := d.APIBase
	if base == "" {
		base = "https://discord.com/api/v10"
	}
	var raw []byte
	if body != nil { // none: no "null" (joining a thread takes no body)
		raw, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+d.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		e := &discordError{Path: path, Status: resp.Status, HTTP: resp.StatusCode}
		_ = json.NewDecoder(resp.Body).Decode(e)
		return e
	}
	if len(out) > 0 && out[0] != nil {
		_ = json.NewDecoder(resp.Body).Decode(out[0])
	}
	return nil
}

// discordError is a refused API call, with Discord's own code and reason
// (160004: the message has a thread already; 50001/50013: no access/permission).
type discordError struct {
	Path    string `json:"-"`
	Status  string `json:"-"`
	HTTP    int    `json:"-"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *discordError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("discord %s: %s", e.Path, e.Status)
	}
	return fmt.Sprintf("discord %s: %s (%d %s)", e.Path, e.Status, e.Code, e.Message)
}

// discordCode is the Discord error code of err (0 = none).
func discordCode(err error) int {
	var e *discordError
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func (d *Discord) Send(ctx context.Context, chatID, text string) ([]string, error) {
	var ids []string
	for _, part := range chunks(text, 1900) {
		var sent struct {
			ID string `json:"id"`
		}
		if err := d.do(ctx, "POST", "/channels/"+chatID+"/messages", map[string]string{"content": part}, &sent); err != nil {
			return ids, err
		}
		ids = append(ids, sent.ID)
	}
	return ids, nil
}

// cut keeps the first n characters (lines kept, unlike clip).
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// React puts an emoji on a message (on) or takes the bot's off.
func (d *Discord) React(ctx context.Context, chatID, msgID, emoji string, on bool) error {
	method := "PUT"
	if !on {
		method = "DELETE"
	}
	return d.do(ctx, method, "/channels/"+chatID+"/messages/"+msgID+"/reactions/"+url.PathEscape(emoji)+"/@me", nil)
}

// Edit changes one of the bot's messages.
func (d *Discord) Edit(ctx context.Context, chatID, msgID, text string) error {
	return d.do(ctx, "PATCH", "/channels/"+chatID+"/messages/"+msgID, map[string]string{"content": cut(text, 1900)})
}

// Delete removes one of the bot's messages.
func (d *Discord) Delete(ctx context.Context, chatID, msgID string) error {
	return d.do(ctx, "DELETE", "/channels/"+chatID+"/messages/"+msgID, nil)
}

// buttonPrefix marks office's buttons (their custom_id: the command).
const buttonPrefix = "office:"

// SendButtons posts text with buttons (5 a row, 5 rows at most).
func (d *Discord) SendButtons(ctx context.Context, chatID, text string, rows [][]Button) ([]string, error) {
	var sent struct {
		ID string `json:"id"`
	}
	if err := d.do(ctx, "POST", "/channels/"+chatID+"/messages", map[string]any{"content": cut(text, 1900), "components": components(rows)}, &sent); err != nil {
		return nil, err
	}
	return []string{sent.ID}, nil
}

// EditButtons changes a message with buttons: its text, and its buttons
// (none: an empty list takes them off).
func (d *Discord) EditButtons(ctx context.Context, chatID, msgID, text string, rows [][]Button) error {
	return d.do(ctx, "PATCH", "/channels/"+chatID+"/messages/"+msgID, map[string]any{"content": cut(text, 1900), "components": components(rows)})
}

// components are the rows of buttons ([] when there are none).
func components(rows [][]Button) []map[string]any {
	comps := []map[string]any{}
	for _, r := range rows[:min(len(rows), 5)] {
		var row []map[string]any
		for _, b := range r[:min(len(r), 5)] {
			style := 3 // green
			if b.Danger {
				style = 4
			}
			row = append(row, map[string]any{"type": 2, "style": style, "label": clip(b.Label, 80), "custom_id": clip(buttonPrefix+b.Data, 100)})
		}
		comps = append(comps, map[string]any{"type": 1, "components": row})
	}
	return comps
}

func (d *Discord) Typing(ctx context.Context, chatID string) {
	_ = d.rest(ctx, "/channels/"+chatID+"/typing", map[string]any{})
}
