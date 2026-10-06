package chat_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/storage"
)

// cliCalls is how many times the fake Claude CLI ran.
func cliCalls(t *testing.T, dir string) int {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".args") {
			n++
		}
	}
	return n
}

func (g group) jobs(t *testing.T) int {
	t.Helper()
	list, err := g.f.st.Jobs().List(g.context, storage.JobFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func (g group) pause(t *testing.T, id string) {
	t.Helper()
	if err := g.f.st.Agents().SetEnabled(g.context, id, false); err != nil {
		t.Fatal(err)
	}
}

func TestTagPausedAgentGetsNotice(t *testing.T) {
	g := newGroup(t)
	g.pause(t, g.dev.ID)
	jobs := g.jobs(t)
	turn, msg, err := g.engine.Send(g.context, g.conv.ID, "@Dev xem lỗi này", nil)
	var off *chat.OffError
	if !errors.As(err, &off) || !errors.Is(err, storage.ErrAgentOff) || turn != nil {
		t.Fatalf("turn=%v err=%v", turn, err)
	}
	if msg.ID == "" || off.Message.ID == "" || off.Notice != storage.OffNotice("Dev") {
		t.Fatalf("msg=%+v off=%+v", msg, off)
	}
	if n := cliCalls(t, g.dir); n != 0 {
		t.Fatalf("AI ran %d times for a paused agent", n)
	}
	if g.jobs(t) != jobs {
		t.Fatal("a job was created for a paused agent")
	}
	msgs, _ := g.f.st.Chat().ListMessages(g.context, g.conv.ID)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "error" || !strings.Contains(msgs[1].Content, "Dev đang tạm nghỉ") {
		t.Fatalf("messages = %+v", msgs)
	}
	// on again: as before
	if err := g.f.st.Agents().SetEnabled(g.context, g.dev.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := g.sendAll(t, "@Dev xem lại"); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("authors = %v", got)
	}
}

func TestTagPausedAndOnAgents(t *testing.T) {
	g := newGroup(t)
	g.pause(t, g.dev.ID)
	got := g.sendAll(t, "@"+g.leadNm+" @Dev cùng xem")
	if len(got) != 1 || got[0] != g.leadNm {
		t.Fatalf("only the lead should answer, got %v", got)
	}
	if n := cliCalls(t, g.dir); n != 1 {
		t.Fatalf("AI ran %d times, want 1", n)
	}
	if args, _ := call(t, g.dir, 1); strings.Contains(args, "@Dev:") {
		t.Fatalf("the prompt offers the paused agent:\n%s", args)
	}
	out := g.waitAuthors(t, 2)
	if strings.Join(out, ",") != "!,"+g.leadNm {
		t.Fatalf("chat = %v, want the notice then the lead", out)
	}
}

// The chat's own agent is paused and nobody is tagged: the agent that is on
// and answered there last takes the chat over (before another lead).
func TestPausedDefaultAgentHandsOver(t *testing.T) {
	g := newGroup(t)
	m, _ := g.f.st.OrgModels().GetForRepo(g.context, g.f.project.ID)
	if _, err := g.f.st.Agents().Create(g.context, storage.Agent{OrgModelID: m.ID, Key: "lead2", Name: "Lead2", Tier: storage.TierLead, ModelTier: "fast"}); err != nil {
		t.Fatal(err)
	}
	if got := g.sendAll(t, "@Dev giúp với"); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("authors = %v", got)
	}
	g.pause(t, g.lead)
	if got := g.sendAll(t, "chào"); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("authors = %v, want Dev (answered here last)", got)
	}
	conv, _ := g.f.st.Chat().GetConversation(g.context, g.conv.ID)
	if conv.AgentID != g.dev.ID || conv.AgentName != "Dev" {
		t.Fatalf("chat's agent = %s/%s, want Dev", conv.AgentID, conv.AgentName)
	}
}

// Nobody answered there yet: the first lead that is on.
func TestPausedDefaultAgentGoesToLead(t *testing.T) {
	g := newGroup(t)
	m, _ := g.f.st.OrgModels().GetForRepo(g.context, g.f.project.ID)
	if _, err := g.f.st.Agents().Create(g.context, storage.Agent{OrgModelID: m.ID, Key: "lead2", Name: "Lead2", Tier: storage.TierLead, ModelTier: "fast"}); err != nil {
		t.Fatal(err)
	}
	g.pause(t, g.lead)
	if got := g.sendAll(t, "chào"); len(got) != 1 || got[0] != "Lead2" {
		t.Fatalf("authors = %v, want Lead2", got)
	}
}

func TestPausedDefaultAgentGetsNotice(t *testing.T) {
	g := newGroup(t)
	g.pause(t, g.lead)
	g.pause(t, g.dev.ID)
	_, _, err := g.engine.Send(g.context, g.conv.ID, "chào", nil)
	if !errors.Is(err, storage.ErrAgentOff) || !strings.Contains(err.Error(), g.leadNm+" đang tạm nghỉ") {
		t.Fatalf("err = %v", err)
	}
	if n := cliCalls(t, g.dir); n != 0 {
		t.Fatalf("AI ran %d times", n)
	}
	if err := g.f.st.Agents().SetEnabled(g.context, g.dev.ID, true); err != nil {
		t.Fatal(err)
	}
	// tagging an agent that is on still works in that chat
	if got := g.sendAll(t, "@Dev giúp với"); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("authors = %v", got)
	}
	if err := g.engine.SetAgent(g.context, g.conv.ID, g.lead); err != nil {
		t.Fatal(err) // already its agent: nothing changes
	}
	if err := g.engine.SetAgent(g.context, g.conv.ID, g.dev.ID); err != nil {
		t.Fatal(err)
	}
	if err := g.engine.SetAgent(g.context, g.conv.ID, g.lead); !errors.Is(err, storage.ErrAgentOff) {
		t.Fatalf("switching to a paused agent: %v", err)
	}
}

// A bot's chat (Discord/Telegram) whose rule agent is paused: the notice is
// the answer (the executor sends it back to the bot), no AI runs; no other
// agent takes it over (the rule chose the agent and its rights).
func TestChannelChatOfPausedAgent(t *testing.T) {
	g := newGroup(t)
	conv, err := g.engine.StartConversationPurpose(g.context, g.f.project.ID, g.dev.ID, "channel")
	if err != nil {
		t.Fatal(err)
	}
	g.pause(t, g.dev.ID)
	var off *chat.OffError
	if _, _, err := g.engine.Send(g.context, conv.ID, "lỗi gì đây", nil); !errors.As(err, &off) || off.Notice != storage.OffNotice("Dev") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := g.engine.Send(g.context, conv.ID, "@Dev lỗi gì đây", nil); !errors.As(err, &off) {
		t.Fatalf("tagged: err = %v", err)
	}
	if n := cliCalls(t, g.dir); n != 0 {
		t.Fatalf("AI ran %d times", n)
	}
	// a new chat with no agent named goes to the lead that is on
	g.pause(t, g.lead)
	if _, err := g.engine.StartConversationFor(g.context, g.f.project.ID, ""); err != nil {
		t.Fatal(err) // the only lead is paused: still picked, its first message gets the notice
	}
}

func TestDelegateToPausedAgent(t *testing.T) {
	g := newGroup(t)
	g.pause(t, g.dev.ID)
	sc := officetools.Scope{ProjectID: g.f.project.ID, ConversationID: g.conv.ID, RunRef: "r1", Agent: g.leadNm}
	_, err := g.engine.Delegate(g.context, sc, "Dev", "sửa lỗi")
	if !errors.Is(err, storage.ErrAgentOff) || !strings.Contains(err.Error(), "Dev đang tạm nghỉ") {
		t.Fatalf("err = %v", err)
	}
}
