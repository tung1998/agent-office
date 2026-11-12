package chat_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

// Office going down cuts an answer off without ending its job; the next
// office goes on with it in the agent's session.
func TestRestartGoesOn(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bin, argsLog := filepath.Join(tmp, "claude"), filepath.Join(tmp, "args")
	fakebin.Write(bin, []byte(`#!/bin/sh
echo "$*" >> `+argsLog+`
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"sess-1"}'
case "$*" in
*"--resume sess-1"*) echo '{"type":"result","subtype":"success","is_error":false,"result":"xong phần còn lại","session_id":"sess-1","usage":{"input_tokens":1,"output_tokens":1}}' ;;
*) sleep 30 ;;
esac
`))
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, err := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		if err != nil {
			t.Fatal(err)
		}
		return p
	})
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	first, _, err := f.engine.Send(ctx, conv.ID, "làm việc dài", nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // it started and said its session
	start := time.Now()
	f.engine.Shutdown(5 * time.Second)
	if evs := collect(t, first); evs[len(evs)-1].Type != "error" { // the stream watching it ends
		t.Fatalf("cut turn: %+v", evs[len(evs)-1])
	}
	if f.engine.ActiveTurns() != 0 || time.Since(start) > 4*time.Second {
		t.Fatalf("not ended at once: %d turns, %s", f.engine.ActiveTurns(), time.Since(start))
	}
	if _, _, err := f.engine.Send(ctx, conv.ID, "thêm", nil); err != chat.ErrStopping {
		t.Fatalf("a message while going down: %v", err)
	}
	jobs, _ := f.st.Jobs().List(ctx, storage.JobFilter{Kind: "chat_turn", Status: "running"})
	msgs, _ := f.st.Chat().ListMessages(ctx, conv.ID)
	members, _ := f.st.Chat().Members(ctx, conv.ID)
	if len(jobs) != 1 || len(msgs) != 1 || len(members) != 1 || members[0].SessionID != "sess-1" {
		t.Fatalf("jobs=%d msgs=%+v members=%+v", len(jobs), msgs, members)
	}

	// the next office
	next := chat.NewEngine(f.st, f.provs, usage.New(f.st, time.UTC))
	if n := next.ResumeInterrupted(context.Background(), jobs); n != 1 {
		t.Fatalf("went on in %d chats", n)
	}
	turn, ok := next.Active(conv.ID)
	if !ok {
		t.Fatal("no turn")
	}
	events := collect(t, turn)
	if done := events[len(events)-1]; done.Type != "done" || done.Message.Content != "xong phần còn lại" {
		t.Fatalf("last event: %+v", done)
	}
	b, _ := os.ReadFile(argsLog)
	if calls := strings.Split(string(b), "-p --output-format"); len(calls) != 3 || strings.Contains(calls[1], "--resume") || !strings.Contains(calls[2], "--resume sess-1") {
		t.Fatalf("calls: %q", calls)
	}
	msgs, _ = f.st.Chat().ListMessages(ctx, conv.ID)
	if note := msgs[1]; note.Role != "user" || !strings.HasPrefix(note.Content, "@"+members[0].AgentName) || note.Author != "human:a@b.c" {
		t.Fatalf("note: %+v", note)
	}
}

// An answer cut off by restarts too many times in a row (it may be what
// brings office down) is not gone on with again: the chat is told, and the
// person sends a message to go on (ADR-257). One cut fewer times goes on.
func TestRestartGivesUpAfterCutsInARow(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "claude")
	fakebin.Write(bin, []byte(`#!/bin/sh
cat >/dev/null
echo '{"type":"system","subtype":"init","session_id":"s"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"tiếp","session_id":"s","usage":{"input_tokens":1,"output_tokens":1}}'
`))
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := actor.With(context.Background(), "human:a@b.c")
	agents, _ := f.st.Agents().List(ctx, f.project.ID)
	agent := agents[0]
	cuts := func(conv string, n int) storage.Job {
		var last storage.Job
		for i := range n {
			j := storage.Job{ProjectID: f.project.ID, Kind: "chat_turn", Origin: "user", ConversationID: conv, AgentID: agent.ID, CreatedBy: "human:a@b.c",
				Status: "failed", ErrorCode: "restart", CreatedAt: time.Now().UTC().Add(time.Duration(i-n) * time.Minute)}
			if i == n-1 {
				j.Status, j.ErrorCode = "running", "" // the one just cut
			}
			last, _ = f.st.Jobs().Create(ctx, j)
		}
		return last
	}
	looping, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	fine, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	jobs := []storage.Job{cuts(looping.ID, storage.MaxJobResumes+1), cuts(fine.ID, storage.MaxJobResumes)}
	if n := f.engine.ResumeInterrupted(context.Background(), jobs); n != 1 {
		t.Fatalf("went on in %d chats", n)
	}
	if _, ok := f.engine.Active(looping.ID); ok {
		t.Fatal("went on once more")
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, looping.ID)
	if len(msgs) != 1 || msgs[0].Role != "error" || !strings.Contains(msgs[0].Content, "@"+agent.Name) || !strings.Contains(msgs[0].Content, "3 lần") {
		t.Fatalf("looping chat: %+v", msgs)
	}
	turn, ok := f.engine.Active(fine.ID)
	if !ok {
		t.Fatal("the other did not go on")
	}
	collect(t, turn)
}
