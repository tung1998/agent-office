package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/attach"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// Discord threads: a thread made from a message goes on with that message's
// conversation (the thread's id is the message's). As anywhere, the bot
// answers a tag; /create-conversation in the thread makes it hear the rest. The conversation links to where it
// is on Discord: its first message, then its thread.

func msgKey(messageID string) string { return "thread:" + messageID } // a message → its conversation
func inKey(chatID string) string     { return "in:" + chatID }        // a thread → its conversation

const linkKey = "conv_link/"

// lastKey: the bot's latest answer in a chat (/create-thread from the "/" menu grows from it).
func lastKey(channelID, chatID string) string { return "channel_last/" + channelID + "/" + chatID }

// ConversationLink is where a conversation is on Discord or Telegram ("" = nowhere).
func ConversationLink(ctx context.Context, st storage.Store, convID string) string {
	var v string
	_, _ = st.Settings().Get(ctx, linkKey+convID, &v)
	return v
}

func discordURL(guild, chat, msg string) string {
	if guild == "" {
		guild = "@me"
	}
	u := "https://discord.com/channels/" + guild + "/" + chat
	if msg != "" {
		u += "/" + msg
	}
	return u
}

// threadOf is the conversation a thread chat goes on with ("" = none).
func (m *Manager) threadOf(ctx context.Context, ch storage.Channel, chatID string) string {
	id, err := m.store.Channels().Thread(ctx, ch.ID, inKey(chatID))
	if err != nil {
		return ""
	}
	return id
}

// placed: a message of a conversation (the person's or the bot's) is known by
// its id, so a thread made from it finds the conversation; the conversation
// links to its first message, until it has a thread.
func (m *Manager) placed(ctx context.Context, ch storage.Channel, convID, guild, chat, msgID string) {
	if convID == "" || msgID == "" {
		return
	}
	url := ""
	switch ch.Kind {
	case "discord":
		_ = m.store.Channels().SetThread(ctx, ch.ID, msgKey(msgID), convID)
		url = discordURL(guild, chat, msgID)
	case "telegram":
		url = telegramURL(ch.BotName, chat, msgID)
	}
	if url != "" && ConversationLink(ctx, m.store, convID) == "" {
		_ = m.store.Settings().Set(ctx, linkKey+convID, url)
	}
}

// telegramURL: a supergroup's message (t.me/c/…), or the private chat with
// the bot; a basic group has no link.
func telegramURL(bot, chat, msg string) string {
	switch {
	case strings.HasPrefix(chat, "-100"):
		return "https://t.me/c/" + strings.TrimPrefix(chat, "-100") + "/" + msg
	case !strings.HasPrefix(chat, "-") && bot != "":
		return "https://t.me/" + bot
	}
	return ""
}

// threadMade binds a new thread to the conversation of the message it grew from.
func (m *Manager) threadMade(ctx context.Context, ch storage.Channel, in Incoming) {
	conv, err := m.store.Channels().Thread(ctx, ch.ID, msgKey(in.ThreadOf))
	if (err != nil || conv == "") && in.ParentID != "" { // a bot answer remembered before threads were (its reply key)
		conv, err = m.store.Channels().Thread(ctx, ch.ID, "msg:"+in.ParentID+":"+in.ThreadOf)
	}
	if err != nil || conv == "" {
		return // a thread of a message office did not take part in
	}
	if cur, _ := m.store.Channels().Thread(ctx, ch.ID, inKey(in.ChatID)); cur == conv {
		return // bound already (an open thread seen again on connect)
	}
	_ = m.store.Channels().SetThread(ctx, ch.ID, inKey(in.ChatID), conv)
	_ = m.store.Settings().Set(ctx, linkKey+conv, discordURL(in.GuildID, in.ChatID, ""))
}

// ThreadMaker is an adapter that can open a thread (Discord): from a message
// (fromMsg; the thread takes its id) or on its own; it returns the thread.
type ThreadMaker interface {
	MakeThread(ctx context.Context, chatID, fromMsg, name string) (string, error)
}

// makeThread answers /create-thread: a thread from the bot's answer replied to
// (its conversation goes on there), else from the command's own message (or,
// a slash command, on its own), which keeps one conversation.
func (m *Manager) makeThread(ctx context.Context, ch storage.Channel, ad Adapter, in Incoming, name string) string {
	tm, ok := ad.(ThreadMaker)
	if !ok || in.Private {
		return "Ở đây không tạo thread được."
	}
	if in.InThread { // Discord has no thread in a thread
		return "Đang ở trong thread rồi: nhắn tiếp ở đây. Muốn thread mới thì gõ /create-thread ở kênh ngoài."
	}
	conv := ""
	from := in.MessageID
	if in.ReplyTo == "" && in.Respond != nil { // from the "/" menu (no message it replies to): the latest answer here
		var last string
		// …unless it has its thread already (Discord gives a message one): then
		// a new thread on its own, with a new conversation
		if ok, _ := m.store.Settings().Get(ctx, lastKey(ch.ID, in.ChatID), &last); ok && last != "" && !m.threaded(ctx, ch, last) {
			in.ReplyTo = last
		}
	}
	if in.ReplyTo != "" {
		from = in.ReplyTo
		if id, err := m.store.Channels().Thread(ctx, ch.ID, msgKey(in.ReplyTo)); err == nil {
			conv = id
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Hội thoại"
		if conv != "" {
			if c, err := m.store.Chat().GetConversation(ctx, conv); err == nil && c.Title != "" {
				name = c.Title
			}
		}
	}
	if r := []rune(name); len(r) > 100 {
		name = string(r[:99]) + "…"
	}
	thread, err := tm.MakeThread(ctx, in.ChatID, from, name)
	if err != nil {
		switch discordCode(err) {
		case 50001, 50013: // no access / missing permission
			return "Chưa tạo được thread (bot cần quyền Create Public Threads): " + err.Error()
		case 50024: // not a channel that has threads (a thread, a voice channel…)
			return "Kênh này không tạo thread được: " + err.Error()
		}
		return "Chưa tạo được thread: " + err.Error()
	}
	if from != "" {
		_ = m.store.Settings().Set(ctx, threadedKey(ch.ID, from), thread)
	}
	if conv != "" {
		_ = m.store.Channels().SetThread(ctx, ch.ID, inKey(thread), conv)
		_ = m.store.Settings().Set(ctx, linkKey+conv, discordURL(in.GuildID, thread, ""))
	}
	m.setKeep(ctx, ch.ID, thread, true) // asked for: a conversation from the start, no tag needed there
	hint := "Đã mở thread: nhắn tiếp ở đây, không cần tag. Gõ /close-conversation nếu muốn phải tag mới trả lời."
	if conv != "" {
		hint = "Đã mở thread, tiếp tục hội thoại ở trên: nhắn tiếp ở đây, không cần tag. Gõ /close-conversation nếu muốn phải tag mới trả lời."
	}
	_, _ = ad.Send(ctx, thread, hint)
	if in.Respond != nil {
		return "Đã tạo thread <#" + thread + ">."
	}
	return ""
}

// threadedKey: a message a thread was made from (its thread).
func threadedKey(channelID, msgID string) string {
	return "channel_threaded/" + channelID + "/" + msgID
}

// threaded: a thread grew from the message already (a thread's id is that
// of the message it grew from).
func (m *Manager) threaded(ctx context.Context, ch storage.Channel, msgID string) bool {
	var t string
	if ok, _ := m.store.Settings().Get(ctx, threadedKey(ch.ID, msgID), &t); ok && t != "" {
		return true
	}
	return m.threadOf(ctx, ch, msgID) != ""
}

func closedKey(channelID, chatID string) string { return "channel_closed/" + channelID + "/" + chatID }

// startKeep turns on a kept conversation: it goes on with the conversation of
// the bot's latest answer here (tagging, then keeping: nothing lost), unless
// that one was closed with /close-conversation (then a fresh one).
func (m *Manager) startKeep(ctx context.Context, ch storage.Channel, chatID string) string {
	var last, closed string
	_, _ = m.store.Settings().Get(ctx, lastKey(ch.ID, chatID), &last)
	_, _ = m.store.Settings().Get(ctx, closedKey(ch.ID, chatID), &closed)
	msg := m.setKeep(ctx, ch.ID, chatID, true)
	if last == "" || last == closed {
		return msg
	}
	conv, _ := m.store.Channels().Thread(ctx, ch.ID, msgKey(last))
	if conv == "" {
		conv, _ = m.store.Channels().Thread(ctx, ch.ID, "msg:"+chatID+":"+last)
	}
	c, err := m.store.Chat().GetConversation(ctx, conv)
	if conv == "" || err != nil {
		return msg
	}
	if err := m.store.Channels().SetThread(ctx, ch.ID, chatID+"#"+c.AgentID+"#"+m.keep(ctx, ch.ID, chatID), conv); err != nil {
		return msg
	}
	return "Đã bật hội thoại, tiếp tục từ câu trả lời gần nhất của mình: từ giờ không cần tag, mình nhớ những gì đã nói. Gửi /close-conversation để kết thúc."
}

// stopKeep ends it; the next /create-conversation starts afresh.
func (m *Manager) stopKeep(ctx context.Context, ch storage.Channel, chatID string) string {
	var last string
	_, _ = m.store.Settings().Get(ctx, lastKey(ch.ID, chatID), &last)
	_ = m.store.Settings().Set(ctx, closedKey(ch.ID, chatID), last)
	return m.setKeep(ctx, ch.ID, chatID, false)
}

// saveFiles downloads what was sent with a message into the office's
// attachments (images, PDFs, text; the chat's limits). It returns their ids,
// and why the others were left out.
func (m *Manager) saveFiles(ctx context.Context, ch storage.Channel, files []InFile, by string) (ids, skipped []string) {
	store := m.engine.Attachments()
	for i, f := range files {
		if i >= attach.MaxPerSend {
			skipped = append(skipped, f.Name+" (tối đa "+strconv.Itoa(attach.MaxPerSend)+" file)")
			continue
		}
		if f.Size > attach.MaxSize {
			skipped = append(skipped, f.Name+": "+attach.ErrTooBig.Error())
			continue
		}
		data, err := f.Fetch(ctx)
		if err == nil {
			var meta attach.Meta
			if meta, err = store.Save(ch.ProjectID, by, f.Name, data); err == nil {
				ids = append(ids, meta.ID)
				continue
			}
		}
		skipped = append(skipped, f.Name+": "+err.Error())
	}
	return ids, skipped
}

// Reactor is an adapter that can put an emoji on a message (and take it off).
type Reactor interface {
	React(ctx context.Context, chatID, msgID, emoji string, on bool) error
}

// Editor is an adapter that can change and remove the bot's own messages.
type Editor interface {
	Edit(ctx context.Context, chatID, msgID, text string) error
	Delete(ctx context.Context, chatID, msgID string) error
}

// Progress shows what a run for a message is doing (the runner's OnProgress):
// after ProgressAfter, one status message, edited at most every 8 seconds
// (well under Discord's and Telegram's edit limits, several runs at once too).
func (m *Manager) Progress(ctx context.Context, origin storage.Job, step string) {
	var p trigger.ChannelPayload
	if json.Unmarshal([]byte(origin.Payload), &p) != nil {
		return
	}
	m.mu.Lock()
	w, ok := m.waiting[origin.ID]
	ad := m.adapters[p.ChannelID]
	if !ok || ad == nil {
		m.mu.Unlock()
		return
	}
	w.steps++
	if w.verbose {
		m.step(ctx, origin.ID, ad, w, step) // unlocks
		return
	}
	now := time.Now()
	if now.Sub(w.started) < m.ProgressAfter || (w.status != "" && now.Sub(w.edited) < 8*time.Second && m.ProgressAfter > 0) {
		m.waiting[origin.ID] = w
		m.mu.Unlock()
		return
	}
	w.edited = now
	m.waiting[origin.ID] = w
	status, chatID := w.status, w.chat
	m.mu.Unlock()
	text := fmt.Sprintf("⏳ Đang làm… (%d bước)\n%s", w.steps, truncate(step, 300))
	ed, canEdit := ad.(Editor)
	if status != "" && canEdit {
		_ = ed.Edit(ctx, chatID, status, text)
		return
	}
	if status != "" || !canEdit {
		return // no editing: one status line is enough
	}
	if ids, err := ad.Send(ctx, chatID, text); err == nil && len(ids) > 0 {
		m.mu.Lock()
		if cur, ok := m.waiting[origin.ID]; ok {
			cur.status = ids[0]
			m.waiting[origin.ID] = cur
		} else { // answered meanwhile: gone at once
			go func() { _ = ed.Delete(context.WithoutCancel(ctx), chatID, ids[0]) }()
		}
		m.mu.Unlock()
	}
}

// Reply mode steps: each step a line of a status message that stays (a new
// one when it is full), edited at most every stepsEvery; the last edit, once
// answered, shows them all.
const (
	stepsEvery = 3 * time.Second
	stepsMax   = 1800 // under Discord's 2000 and Telegram's 4096
)

func stepsText(lines []string, done bool) string {
	head := "🛠 Các bước:"
	if done {
		head = "✅ Các bước:"
	}
	return head + "\n" + strings.Join(lines, "\n")
}

// step adds one line to a verbose run's steps; called with m.mu held, it unlocks.
func (m *Manager) step(ctx context.Context, jobID string, ad Adapter, w waiter, step string) {
	w.lines = append(w.lines, "• "+truncate(strings.TrimSpace(step), 300))
	ed, canEdit := ad.(Editor)
	if !canEdit { // no editing: they all go once answered
		m.waiting[jobID] = w
		m.mu.Unlock()
		return
	}
	var full, full2 string // a full message: kept as it is, the step opens the next
	if w.status != "" && len(stepsText(w.lines, false)) > stepsMax {
		full, full2 = w.status, stepsText(w.lines[:len(w.lines)-1], true)
		w.status, w.lines = "", w.lines[len(w.lines)-1:]
	}
	now := time.Now()
	if w.status != "" && now.Sub(w.edited) < stepsEvery {
		m.waiting[jobID] = w // shown at the next edit, or once answered
		m.mu.Unlock()
		return
	}
	w.edited = now
	m.waiting[jobID] = w
	status, chatID, text := w.status, w.chat, stepsText(w.lines, false)
	m.mu.Unlock()
	if full != "" {
		_ = ed.Edit(ctx, chatID, full, full2)
	}
	if status != "" {
		_ = ed.Edit(ctx, chatID, status, text)
		return
	}
	ids, err := ad.Send(ctx, chatID, text)
	if err != nil || len(ids) == 0 {
		return
	}
	m.mu.Lock()
	cur, ok := m.waiting[jobID]
	if ok {
		cur.status = ids[0]
		m.waiting[jobID] = cur
	}
	m.mu.Unlock()
	if !ok { // answered meanwhile: its last steps were not in it
		_ = ed.Edit(context.WithoutCancel(ctx), chatID, ids[0], stepsText(w.lines, true))
	}
}

// settle clears a message's marks once it is answered.
func (m *Manager) settle(ctx context.Context, ad Adapter, w waiter) {
	ctx = context.WithoutCancel(ctx)
	if r, ok := ad.(Reactor); ok && w.msg != "" {
		go func() { _ = r.React(ctx, w.chat, w.msg, "👀", false) }()
	}
	if w.verbose { // the steps stay, all of them, before the answer
		if len(w.lines) == 0 {
			return
		}
		if ed, ok := ad.(Editor); ok && w.status != "" {
			_ = ed.Edit(ctx, w.chat, w.status, stepsText(w.lines, true))
		} else if !ok {
			_, _ = ad.Send(ctx, w.chat, stepsText(w.lines, true))
		}
		return
	}
	if ed, ok := ad.(Editor); ok && w.status != "" {
		go func() { _ = ed.Delete(ctx, w.chat, w.status) }()
	}
}

// Notify posts text to one of a bot's chats (the limit alerts), if it runs.
func (m *Manager) Notify(ctx context.Context, channelID, chatID, text string) error {
	m.mu.Lock()
	ad := m.adapters[channelID]
	m.mu.Unlock()
	if ad == nil {
		return errors.New("bot này đang tắt hoặc chưa kết nối")
	}
	_, err := ad.Send(ctx, chatID, text)
	return err
}

func noteKey(chatID, msgID string) string { return "note:" + chatID + ":" + msgID } // an automation's notice → its chat

// NotifyConversation posts an automation's answer; a reply to it goes on in
// the chat the run talked in (convID), whichever agent the bot has.
func (m *Manager) NotifyConversation(ctx context.Context, channelID, chatID, text, convID string) error {
	m.mu.Lock()
	ad := m.adapters[channelID]
	m.mu.Unlock()
	if ad == nil {
		return errors.New("bot này đang tắt hoặc chưa kết nối")
	}
	ids, err := ad.Send(ctx, chatID, text)
	if convID == "" {
		return err
	}
	for _, id := range ids {
		_ = m.store.Channels().SetThread(ctx, channelID, noteKey(chatID, id), convID)
		_ = m.store.Channels().SetThread(ctx, channelID, msgKey(id), convID) // a thread from it goes on there too
	}
	if len(ids) > 0 {
		_ = m.store.Settings().Set(ctx, lastKey(channelID, chatID), ids[len(ids)-1])
	}
	return err
}
