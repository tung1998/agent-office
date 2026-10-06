package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Incoming is a message addressed to the bot.
type Incoming struct {
	ChatID   string // where to answer (a private chat, a group, a Discord channel)
	UserID   string
	UserName string
	Text     string // without the bot's @mention
	Private  bool
	// ReplyTo: the bot's message this one replies to ("" = none): its
	// conversation goes on.
	ReplyTo string
	// Addressed: for the bot (private, tagged, a reply to it, a command).
	// Others come up too: a kept conversation hears its whole chat.
	Addressed bool
	// Respond answers a slash command (Discord shows "thinking…" until it
	// does) and returns the answer's message id; nil for a message, which is
	// answered with Send.
	Respond func(ctx context.Context, text string) (string, error)
	// MessageID and GuildID place the message on Discord (a link to it).
	MessageID, GuildID string
	// ThreadOf: a thread was just made from that message (Discord; ChatID is
	// the thread, Text empty): the message's conversation goes on in it.
	ThreadOf string
	ParentID string // …in that channel
	// InThread: said in a Discord thread; the thread is one conversation.
	InThread bool
	// Files sent with it (fetched when office takes the message).
	Files []InFile
	// ButtonMsg: a button pressed, on that message of the bot (its buttons
	// are redrawn once decided).
	ButtonMsg string
}

// Button is one under a message: pressed, Data (a command) comes up as the
// person's message.
type Button struct {
	Label, Data string
	Danger      bool
}

// ButtonSender is an adapter that can put buttons under a message.
type ButtonSender interface {
	SendButtons(ctx context.Context, chatID, text string, rows [][]Button) ([]string, error)
}

// ButtonEditor changes a message with buttons the bot sent: its text, and
// its buttons (none: they are taken off).
type ButtonEditor interface {
	EditButtons(ctx context.Context, chatID, msgID, text string, rows [][]Button) error
}

// InFile is a file sent to the bot.
type InFile struct {
	Name  string
	Size  int64
	Fetch func(ctx context.Context) ([]byte, error)
}

// maxFetch caps what is downloaded (office keeps up to 10 MB a file).
const maxFetch = 10 << 20

// fetchURL downloads a file (at most maxFetch; more is an error).
func fetchURL(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("không tải được file")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("không tải được file: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetch+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFetch {
		return nil, fmt.Errorf("file quá lớn (tối đa %d MB)", maxFetch>>20)
	}
	return data, nil
}

// Adapter connects one bot.
type Adapter interface {
	// Run delivers messages addressed to the bot until ctx ends (onReady gets
	// the bot's name once connected).
	Run(ctx context.Context, onReady func(bot string), onMessage func(Incoming)) error
	// Send posts text (in parts if long) and returns the ids of what it posted.
	Send(ctx context.Context, chatID, text string) ([]string, error)
	// SetCommands puts cmds in the platform's command menu.
	SetCommands(ctx context.Context, cmds []Command)
	Typing(ctx context.Context, chatID string)
}

// Telegram is a bot over the Bot API with long polling (no public URL).
type Telegram struct {
	Token   string
	BaseURL string // "" = https://api.telegram.org
	client  http.Client
	bot     string
	retry   time.Duration // wait before retrying a failed call (0 = 5s)
	menu    menu
}

func (t *Telegram) call(ctx context.Context, method string, body any, out any) error {
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/bot"+t.Token+"/"+method, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // its text has the URL, and the URL has the token
			return fmt.Errorf("telegram %s: %v", method, ue.Err)
		}
		return fmt.Errorf("telegram %s: lỗi mạng", method)
	}
	defer resp.Body.Close()
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: %s", method, resp.Status)
	}
	if !env.OK {
		return fmt.Errorf("telegram %s: %s", method, env.Description)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

type tgMessage struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Caption   string `json:"caption"`
	Chat      struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	From struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
	Photo []struct {
		FileID   string `json:"file_id"`
		FileSize int64  `json:"file_size"`
	} `json:"photo"`
	Document *struct {
		FileID   string `json:"file_id"`
		FileName string `json:"file_name"`
		FileSize int64  `json:"file_size"`
	} `json:"document"`
	ReplyTo *struct {
		MessageID int64 `json:"message_id"`
		From      struct {
			Username string `json:"username"`
		} `json:"from"`
	} `json:"reply_to_message"`
}

func (t *Telegram) Run(ctx context.Context, onReady func(string), onMessage func(Incoming)) error {
	var me struct {
		Username string `json:"username"`
	}
	wait := t.retry
	if wait == 0 {
		wait = 5 * time.Second
	}
	for backoff := wait; ; backoff = min(backoff*2, 12*wait) { // a network hiccup at start is retried
		err := t.call(ctx, "getMe", map[string]any{}, &me)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil
		}
		if strings.Contains(err.Error(), "Unauthorized") || strings.Contains(err.Error(), "Not Found") {
			return err // a wrong token does not heal by retrying
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
	}
	t.bot = me.Username
	onReady(me.Username)
	offset := int64(0)
	for ctx.Err() == nil {
		var updates []struct {
			UpdateID int64      `json:"update_id"`
			Message  *tgMessage `json:"message"`
			Callback *struct {
				ID   string `json:"id"`
				Data string `json:"data"`
				From struct {
					ID       int64  `json:"id"`
					Username string `json:"username"`
				} `json:"from"`
				Message *struct {
					MessageID int64 `json:"message_id"`
					Chat      struct {
						ID   int64  `json:"id"`
						Type string `json:"type"`
					} `json:"chat"`
				} `json:"message"`
			} `json:"callback_query"`
		}
		pctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := t.call(pctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message", "callback_query"}}, &updates)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if strings.Contains(err.Error(), "Unauthorized") {
				return err // a wrong token does not heal by retrying
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			if m, ok := t.addressed(u.Message); ok {
				onMessage(m)
			}
			if c := u.Callback; c != nil && c.Message != nil && c.Data != "" { // a button pressed: its command
				go func(id string) {
					_ = t.call(context.WithoutCancel(ctx), "answerCallbackQuery", map[string]string{"callback_query_id": id}, nil)
				}(c.ID)
				in := Incoming{ChatID: strconv.FormatInt(c.Message.Chat.ID, 10), UserID: strconv.FormatInt(c.From.ID, 10), UserName: c.From.Username,
					Private: c.Message.Chat.Type == "private", Text: c.Data, Addressed: true}
				if c.Message.MessageID != 0 {
					in.ButtonMsg = strconv.FormatInt(c.Message.MessageID, 10)
				}
				onMessage(in)
			}
		}
	}
	return nil
}

// addressed: a private message, or in a group one that tags the bot or
// replies to it.
func (t *Telegram) addressed(m *tgMessage) (Incoming, bool) {
	if m == nil {
		return Incoming{}, false
	}
	text := strings.TrimSpace(m.Text + m.Caption)
	in := Incoming{ChatID: strconv.FormatInt(m.Chat.ID, 10), UserID: strconv.FormatInt(m.From.ID, 10), UserName: m.From.Username, Private: m.Chat.Type == "private"}
	if m.MessageID != 0 {
		in.MessageID = strconv.FormatInt(m.MessageID, 10)
	}
	if len(m.Photo) > 0 { // its sizes, smallest first: the largest
		p := m.Photo[len(m.Photo)-1]
		in.Files = append(in.Files, t.file(p.FileID, "photo.jpg", p.FileSize))
	}
	if m.Document != nil {
		in.Files = append(in.Files, t.file(m.Document.FileID, m.Document.FileName, m.Document.FileSize))
	}
	if text == "" && len(in.Files) == 0 {
		return in, false
	}
	in.Text, in.Addressed = text, true
	if m.ReplyTo != nil && t.bot != "" && strings.EqualFold(m.ReplyTo.From.Username, t.bot) {
		in.ReplyTo = strconv.FormatInt(m.ReplyTo.MessageID, 10)
	}
	switch {
	case !in.Private && t.forOtherBot(text): // "/cmd@other_bot" in a group: another bot's, never ours
		in.Addressed = false
		return in, false
	case in.Private, t.menu.has(text): // "/create_conversation" in a group needs no tag
	case t.bot != "" && removeTag(&text, "@"+t.bot):
		in.Text = strings.TrimSpace(text)
	case m.ReplyTo != nil && strings.EqualFold(m.ReplyTo.From.Username, t.bot):
	default:
		in.Addressed = false
	}
	return in, in.Text != "" || len(in.Files) > 0
}

// file is a Telegram file, fetched through getFile.
func (t *Telegram) file(id, name string, size int64) InFile {
	return InFile{Name: name, Size: size, Fetch: func(ctx context.Context) ([]byte, error) {
		var f struct {
			FilePath string `json:"file_path"`
		}
		if err := t.call(ctx, "getFile", map[string]string{"file_id": id}, &f); err != nil {
			return nil, err
		}
		base := t.BaseURL
		if base == "" {
			base = "https://api.telegram.org"
		}
		return fetchURL(ctx, &t.client, base+"/file/bot"+t.Token+"/"+f.FilePath)
	}}
}

// forOtherBot: a command addressed to another bot ("/cmd@other_bot").
func (t *Telegram) forOtherBot(text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	head, _, _ := strings.Cut(text, " ")
	_, bot, tagged := strings.Cut(head, "@")
	return tagged && bot != "" && !strings.EqualFold(bot, t.bot)
}

func (t *Telegram) Send(ctx context.Context, chatID, text string) ([]string, error) {
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return nil, errors.New("telegram: chat id không hợp lệ")
	}
	var ids []string
	for _, part := range chunks(text, 4000) {
		var sent struct {
			MessageID int64 `json:"message_id"`
		}
		if err := t.call(ctx, "sendMessage", map[string]any{"chat_id": id, "text": part}, &sent); err != nil {
			return ids, err
		}
		ids = append(ids, strconv.FormatInt(sent.MessageID, 10))
	}
	return ids, nil
}

// React puts an emoji on a message (on) or clears the bot's.
func (t *Telegram) React(ctx context.Context, chatID, msgID, emoji string, on bool) error {
	chat, err1 := strconv.ParseInt(chatID, 10, 64)
	msg, err2 := strconv.ParseInt(msgID, 10, 64)
	if err1 != nil || err2 != nil {
		return errors.New("telegram: id không hợp lệ")
	}
	reaction := []map[string]string{}
	if on {
		reaction = append(reaction, map[string]string{"type": "emoji", "emoji": emoji})
	}
	return t.call(ctx, "setMessageReaction", map[string]any{"chat_id": chat, "message_id": msg, "reaction": reaction}, nil)
}

// Edit changes one of the bot's messages.
func (t *Telegram) Edit(ctx context.Context, chatID, msgID, text string) error {
	chat, err1 := strconv.ParseInt(chatID, 10, 64)
	msg, err2 := strconv.ParseInt(msgID, 10, 64)
	if err1 != nil || err2 != nil {
		return errors.New("telegram: id không hợp lệ")
	}
	return t.call(ctx, "editMessageText", map[string]any{"chat_id": chat, "message_id": msg, "text": text}, nil)
}

// Delete removes one of the bot's messages.
func (t *Telegram) Delete(ctx context.Context, chatID, msgID string) error {
	chat, err1 := strconv.ParseInt(chatID, 10, 64)
	msg, err2 := strconv.ParseInt(msgID, 10, 64)
	if err1 != nil || err2 != nil {
		return errors.New("telegram: id không hợp lệ")
	}
	return t.call(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": msg}, nil)
}

// SendButtons posts text with inline buttons (their data: at most 64 bytes).
func (t *Telegram) SendButtons(ctx context.Context, chatID, text string, rows [][]Button) ([]string, error) {
	id, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return nil, errors.New("telegram: chat id không hợp lệ")
	}
	var sent struct {
		MessageID int64 `json:"message_id"`
	}
	if err := t.call(ctx, "sendMessage", map[string]any{"chat_id": id, "text": text, "reply_markup": keyboard(rows)}, &sent); err != nil {
		return nil, err
	}
	return []string{strconv.FormatInt(sent.MessageID, 10)}, nil
}

// EditButtons changes a message with buttons: its text, and its buttons
// (none: an empty keyboard takes them off).
func (t *Telegram) EditButtons(ctx context.Context, chatID, msgID, text string, rows [][]Button) error {
	chat, err1 := strconv.ParseInt(chatID, 10, 64)
	msg, err2 := strconv.ParseInt(msgID, 10, 64)
	if err1 != nil || err2 != nil {
		return errors.New("telegram: id không hợp lệ")
	}
	return t.call(ctx, "editMessageText", map[string]any{"chat_id": chat, "message_id": msg, "text": text, "reply_markup": keyboard(rows)}, nil)
}

// keyboard is Telegram's inline keyboard ([] when there are no buttons).
func keyboard(rows [][]Button) map[string]any {
	kb := [][]map[string]string{}
	for _, r := range rows {
		var row []map[string]string
		for _, b := range r {
			row = append(row, map[string]string{"text": b.Label, "callback_data": b.Data})
		}
		kb = append(kb, row)
	}
	return map[string]any{"inline_keyboard": kb}
}

func (t *Telegram) SetCommands(ctx context.Context, cmds []Command) {
	cmds = capped(cmds)
	t.menu.set(cmds)
	var list []map[string]string // Telegram commands: a-z, 0-9 and _ only
	for _, c := range cmds {
		desc := c.Description
		if c.Arg != "" {
			desc += " (" + c.Arg + ")"
		}
		list = append(list, map[string]string{"command": strings.ReplaceAll(c.Name, "-", "_"), "description": clip(desc, 256)})
	}
	_ = t.call(ctx, "setMyCommands", map[string]any{"commands": list}, nil)
}

func (t *Telegram) Typing(ctx context.Context, chatID string) {
	if id, err := strconv.ParseInt(chatID, 10, 64); err == nil {
		_ = t.call(ctx, "sendChatAction", map[string]any{"chat_id": id, "action": "typing"}, nil)
	}
}

// chunks splits text into pieces of at most n bytes, on line breaks when it can.
func chunks(text string, n int) []string {
	var out []string
	for len(text) > n {
		cut := strings.LastIndex(text[:n], "\n")
		if cut < n/2 {
			cut = n
			for cut > 0 && (text[cut]&0xC0) == 0x80 { // not inside a UTF-8 character
				cut--
			}
		}
		out = append(out, text[:cut])
		text = strings.TrimLeft(text[cut:], "\n")
	}
	if strings.TrimSpace(text) != "" || len(out) == 0 {
		out = append(out, text)
	}
	return out
}

// removeTag drops the first "@bot" (any case) from s, rune by rune so a
// character that changes length when lowercased never throws the index off.
func removeTag(s *string, tag string) bool {
	rs, tr := []rune(*s), []rune(tag)
	for i := 0; i+len(tr) <= len(rs); i++ {
		if strings.EqualFold(string(rs[i:i+len(tr)]), tag) {
			*s = string(rs[:i]) + string(rs[i+len(tr):])
			return true
		}
	}
	return false
}
