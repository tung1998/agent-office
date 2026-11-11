package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// A bot's /btw and /goal (ADR-273).

// runningConv is the conversation of the answer running for the chat key
// ("" = none runs).
func (m *Manager) runningConv(ctx context.Context, key string) string {
	conv, _ := m.runningAnswer(ctx, key)
	return conv
}

// runningAnswer is that conversation and the running answer's goal (nil = none).
func (m *Manager) runningAnswer(ctx context.Context, key string) (string, *storage.AutomationGoal) {
	m.mu.Lock()
	var ids []string
	for id, w := range m.waiting {
		if w.key == key && !w.stopped {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		j, err := m.store.Jobs().Get(ctx, id)
		if err != nil || j.Status != "running" {
			continue
		}
		var p trigger.ChannelPayload
		if json.Unmarshal([]byte(j.Payload), &p) == nil && p.ConversationID != "" {
			return p.ConversationID, p.Goal
		}
	}
	return "", nil
}

// btw answers a side question about the running answer, in the chat it was
// asked (the answer goes on).
func (m *Manager) btw(ctx context.Context, ch storage.Channel, in Incoming, conv, question string, say func(string)) {
	if m.engine == nil {
		say("Bot chưa sẵn sàng.")
		return
	}
	actx := actor.With(ctx, fmt.Sprintf("%s:%s", ch.Kind, firstNonEmpty(in.UserName, in.UserID)))
	msg, err := m.engine.Btw(actx, conv, question)
	if err != nil {
		say("Chưa trả lời được /btw: " + err.Error())
		return
	}
	say(msg.Content)
}

// goalCommand reads a /goal: status and clear are answered (reply), a goal
// is returned with the message to send.
func (m *Manager) goalCommand(ctx context.Context, ch storage.Channel, in Incoming, key, arg string) (*storage.AutomationGoal, string, string) {
	s, _ := chat.ParseSlash("/goal " + arg)
	switch s.GoalOp() {
	case "status":
		if _, g := m.runningAnswer(ctx, key); g != nil {
			return nil, "", "🎯 Đang làm theo mục tiêu: " + g.Text + "\nGửi /goal clear (hoặc /stop) để dừng."
		}
		return nil, "", "Chưa có mục tiêu nào đang chạy. Gửi /goal <mục tiêu> để bắt đầu."
	case "clear":
		if n, _ := m.stopChat(ctx, key); n > 0 {
			return nil, "", "🎯 Đã dừng việc đang làm theo mục tiêu."
		}
		return nil, "", "Không có mục tiêu nào đang chạy."
	}
	text := strings.TrimSpace(s.Arg)
	switch {
	case text == "":
		return nil, "", "Hãy nhập mục tiêu sau lệnh, ví dụ: /goal sửa cho test xanh"
	case s.Check != "" && !MayDecide(ch, in.UserID):
		return nil, "", "Chỉ Admin của bot mới đặt được lệnh kiểm tra (--check)."
	}
	g, err := trigger.CleanGoal(&storage.AutomationGoal{Text: text, Check: s.Check, Judge: true, MaxRounds: s.Rounds}, "chat")
	if err != nil {
		return nil, "", err.Error()
	}
	return g, text, ""
}
