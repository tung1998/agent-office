package trigger_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// replies collects what the runner hands back to a channel (ADR-049).
type replies struct {
	mu  sync.Mutex
	got []reply
}

type reply struct {
	chat, text string
	err        error
	final      bool
}

func (rs *replies) hook(_ context.Context, origin storage.Job, text string, err error, final bool) {
	var p struct {
		ChatID string `json:"chat_id"`
	}
	_ = json.Unmarshal([]byte(origin.Payload), &p)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.got = append(rs.got, reply{p.ChatID, text, err, final})
}

func channelPayload(conv string) string {
	raw, _ := json.Marshal(map[string]string{"message": "đơn 123 đâu rồi?", "user": "an", "user_id": "7", "chat_id": "42", "channel_id": "chn_1", "conversation_id": conv})
	return string(raw)
}

func runChannel(t *testing.T, r *trigger.Runner, st storage.Store, a storage.Automation, payload string) {
	t.Helper()
	if _, _, err := r.Enqueue(context.Background(), a, "telegram", payload, "", ""); err != nil {
		t.Fatal(err)
	}
	for range 3 { // the job, then an agent a script called in
		r.Tick(context.Background(), time.Now().UTC().Add(time.Second))
		r.Wait()
	}
}

// An agent answers in the outside chat's own conversation, and its answer
// goes back to that chat.
func TestChannelChatReplies(t *testing.T) {
	st, p := openStore(t)
	ex := &recExec{reply: "Đơn 123 đang giao"}
	r := trigger.New(st, ex)
	rs := &replies{}
	r.SetOnReply(rs.hook)
	a, _ := st.Automations().Create(context.Background(), storage.Automation{ProjectID: p.ID, Name: "Trả lời", Source: "telegram", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: "chn_1"}})
	runChannel(t, r, st, a, channelPayload("cnv_thread"))
	if len(ex.convs) != 1 || ex.convs[0] != "cnv_thread" || ex.prompts[0] != "đơn 123 đâu rồi?" {
		t.Fatalf("convs = %q prompts = %q", ex.convs, ex.prompts)
	}
	if len(rs.got) != 1 || rs.got[0] != (reply{"42", "Đơn 123 đang giao", nil, true}) {
		t.Fatalf("replies = %+v", rs.got)
	}
}

// A script's output is the answer (no AI); it calls no agent in (ADR-057).
func TestChannelScriptReplies(t *testing.T) {
	st, p := openStore(t)
	ex := &recExec{reply: "Mình kiểm tra thêm rồi báo nhé"}
	r := trigger.New(st, ex)
	rs := &replies{}
	r.SetOnReply(rs.hook)
	a, _ := st.Automations().Create(context.Background(), storage.Automation{ProjectID: p.ID, Name: "Tra đơn", Source: "telegram", Action: "script", Enabled: true,
		Config:   storage.AutomationConfig{ChannelID: "chn_1"},
		Script:   storage.AutomationScript{Lang: "bash", Body: "echo 'Đơn 123: đang giao'; echo '@@agent: khách hỏi gấp'", TimeoutS: 30},
		Escalate: storage.AutomationEscalate{When: "signal", Action: "chat"}})
	runChannel(t, r, st, a, channelPayload(""))
	if len(rs.got) != 1 || rs.got[0] != (reply{"42", "Đơn 123: đang giao", nil, true}) {
		t.Fatalf("replies = %+v", rs.got)
	}
}

// A rule's prompt is the admin's instruction, framed as one; the person's
// message comes apart as what they said, and a command without text is just
// "they used /x" (not a message "/x" to answer).
func TestChannelPromptIsAnInstruction(t *testing.T) {
	st, p := openStore(t)
	ex := &recExec{reply: "tôi yêu bạn"}
	r := trigger.New(st, ex)
	a, _ := st.Automations().Create(context.Background(), storage.Automation{ProjectID: p.ID, Name: "/kiem-tra", Source: "discord", Action: "chat", Enabled: true,
		Prompt: `chỉ trả về "tôi yêu bạn"`, Config: storage.AutomationConfig{ChannelID: "chn_1", Command: "kiem-tra"}})
	raw, _ := json.Marshal(map[string]string{"message": "/kiem-tra", "user": "an", "chat_id": "42", "channel_id": "chn_1", "conversation_id": "cnv_1"})
	if _, _, err := r.Enqueue(context.Background(), a, "discord", string(raw), "", ""); err != nil {
		t.Fatal(err)
	}
	r.Tick(context.Background(), time.Now().UTC().Add(time.Second))
	r.Wait()
	if len(ex.prompts) != 1 {
		t.Fatalf("prompts = %q", ex.prompts)
	}
	// a reply: the person's message is what the agent gets, the instruction goes apart (the system prompt)
	if ex.prompts[0] != "an gọi lệnh /kiem-tra." || !strings.Contains(ex.instrs[0], `chỉ trả về "tôi yêu bạn"`) || !strings.Contains(ex.instrs[0], "/kiem-tra") {
		t.Fatalf("prompt = %q instructions = %q", ex.prompts[0], ex.instrs[0])
	}
}

// A command made from a project skill calls it: the chat is sent "/skill text"
// (the engine expands the skill), the command's name being the skill's.
func TestChannelSkillCommand(t *testing.T) {
	st, p := openStore(t)
	ex := &recExec{reply: "ok"}
	r := trigger.New(st, ex)
	a, _ := st.Automations().Create(context.Background(), storage.Automation{ProjectID: p.ID, Name: "/review", Source: "discord", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{ChannelID: "chn_1", Command: "review", Skill: "superpowers:review"}})
	raw, _ := json.Marshal(map[string]string{"message": "nhánh fix/checkout", "user": "an", "chat_id": "42", "channel_id": "chn_1", "conversation_id": "cnv_1"})
	r.Enqueue(context.Background(), a, "discord", string(raw), "", "")
	r.Tick(context.Background(), time.Now().UTC().Add(time.Second))
	r.Wait()
	if len(ex.prompts) != 1 || ex.prompts[0] != "/superpowers:review nhánh fix/checkout" {
		t.Fatalf("prompts = %q", ex.prompts)
	}
}
