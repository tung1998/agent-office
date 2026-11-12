package chat_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/chat"
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
	t.Parallel()
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

// A bot's chat (Discord/Telegram) whose rule agent is paused: the notice is
// the answer (the executor sends it back to the bot), no AI runs; no other
// agent takes it over (the rule chose the agent and its rights).
func TestChannelChatOfPausedAgent(t *testing.T) {
	t.Parallel()
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
