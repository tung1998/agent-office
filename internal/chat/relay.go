package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/storage"
)

// A message an agent sends to another chat in the name of the person it works
// for (send_to_chat): it goes in as that person wrote it, with where it came
// from as its page context, so the other chat's agent knows and a relayed
// message never relays on by itself.

// relayKey marks a message relayed from another chat (its page context).
const relayKey = "relayed_from"

type relayInfo struct {
	ConversationID string `json:"relayed_from"`
	Title          string `json:"relayed_from_title,omitempty"`
	Agent          string `json:"relayed_by_agent,omitempty"`
}

// Relay sends an approved send_message action: a.Args.Message to the chat
// a.TargetID, as the person whose message started the run (a.JobID). Only a
// person's run can speak for them, and only in chats they may open.
func (e *Engine) Relay(ctx context.Context, a storage.Action) (string, error) {
	job, err := e.store.Jobs().Get(ctx, a.JobID)
	if err != nil {
		return "", errors.New("không rõ lượt chat gửi tin này")
	}
	as := job.CreatedBy
	if !strings.HasPrefix(as, "human:") {
		return "", errors.New("chỉ gửi thay được khi lượt chat do một người gửi (không phải bot hay tự động hóa)")
	}
	target, err := e.store.Chat().GetConversation(ctx, a.TargetID)
	if err != nil {
		return "", errors.New("không tìm thấy cuộc chat đích")
	}
	if target.ID == a.ConversationID {
		return "", errors.New("đây là chính cuộc chat đang nói, không cần gửi sang")
	}
	if e.isAssistant(ctx, target.ProjectID) && target.CreatedBy != as {
		return "", errors.New("cuộc chat trợ lý office của người khác: không gửi được")
	}
	from := relayInfo{ConversationID: a.ConversationID, Agent: a.ProposedBy}
	if c, err := e.store.Chat().GetConversation(ctx, a.ConversationID); err == nil {
		from.Title = c.Title
	}
	raw, _ := json.Marshal(from)
	_, msg, err := e.SendWithContext(actor.With(context.Background(), as), target.ID, a.Args.Message, string(raw), nil)
	var off *OffError
	if errors.As(err, &off) && msg.ID != "" {
		return "đã gửi (agent của chat đó đang tạm nghỉ, chưa trả lời)", nil
	}
	if err != nil {
		return "", err
	}
	return "đã gửi vào \"" + target.Title + "\"", nil
}

// Relayed: the last message a person sent in the chat was relayed from
// another chat — what it starts must not relay on without a person.
func (e *Engine) Relayed(ctx context.Context, conversationID string) bool {
	msgs, err := e.store.Chat().ListMessages(ctx, conversationID)
	if err != nil {
		return false
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return strings.Contains(msgs[i].Context, `"`+relayKey+`"`)
		}
	}
	return false
}
