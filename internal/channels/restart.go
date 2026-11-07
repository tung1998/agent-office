package channels

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// cutNote is a bot chat whose answer office's restart cut off and that does
// not go on (put back too often, or its automation stops on a restart): the
// person who asked is told once the bot is connected again (ADR-258).
type cutNote struct {
	chat string
	text string
}

// CutNotice is what such a chat is told.
const CutNotice = "⚠️ Office vừa khởi động lại khi đang trả lời tin này nên chưa trả lời tiếp được. Bạn gửi lại tin nhé."

// CutNoticeOver: the same, for an answer cut off too many times in a row.
const CutNoticeOver = "⚠️ Câu trả lời cho tin này bị cắt vì office khởi động lại nhiều lần liên tiếp nên đã dừng. Bạn gửi lại tin (có thể ngắn hơn) nhé."

// TellCut keeps a notice for each bot run office restarted under that does
// not go on; it is sent when its bot is connected (now, if it already is).
func (m *Manager) TellCut(jobs []storage.Job) int {
	n := 0
	for _, j := range jobs {
		var p trigger.ChannelPayload
		if !trigger.IsChannel(j.Trigger) || json.Unmarshal([]byte(j.Payload), &p) != nil || p.ChannelID == "" || p.ChatID == "" {
			continue
		}
		text := CutNotice
		if j.Resumes-j.RetryN >= storage.MaxJobResumes {
			text = CutNoticeOver
		}
		m.mu.Lock()
		if m.cut == nil {
			m.cut = map[string][]cutNote{}
		}
		m.cut[p.ChannelID] = append(m.cut[p.ChannelID], cutNote{chat: p.ChatID, text: text})
		ad, ready := m.adapters[p.ChannelID], m.ready[p.ChannelID]
		root := m.root
		m.mu.Unlock()
		n++
		if ad != nil && ready && root != nil {
			m.sendCutNotes(root, p.ChannelID, ad)
		}
	}
	return n
}

// sendCutNotes sends a connected bot the notices kept for it, once each.
func (m *Manager) sendCutNotes(ctx context.Context, channelID string, ad Adapter) {
	m.mu.Lock()
	notes := m.cut[channelID]
	delete(m.cut, channelID)
	m.mu.Unlock()
	var failed []cutNote
	for _, n := range notes {
		if _, err := ad.Send(ctx, n.chat, n.text); err != nil {
			slog.Warn("channels: cut notice not sent, will retry", "channel", channelID, "chat", n.chat, "err", err)
			failed = append(failed, n)
		}
	}
	if len(failed) == 0 {
		return
	}
	m.mu.Lock()
	m.cut[channelID] = append(failed, m.cut[channelID]...)
	m.mu.Unlock()
}
