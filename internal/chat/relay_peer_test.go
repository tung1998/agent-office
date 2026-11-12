package chat_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

type fakePeers struct {
	p     peer.Peer
	mu    sync.Mutex
	sent  []peer.Message
	links []peer.Link
	asked []peer.ConsultRequest
}

func (f *fakePeers) Get(ctx context.Context, id string) (peer.Peer, error) {
	if id != f.p.ID {
		return peer.Peer{}, storage.ErrNotFound
	}
	return f.p, nil
}

func (f *fakePeers) Send(ctx context.Context, id string, m peer.Message) (peer.Reply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return peer.Reply{Chat: "cnv_there"}, nil
}

func (f *fakePeers) LinksOf(ctx context.Context, projectID string) []peer.Link { return f.links }

func (f *fakePeers) Consult(ctx context.Context, id string, q peer.ConsultRequest) (peer.ConsultReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, q)
	return peer.ConsultReply{}, nil
}

func (f *fakePeers) AnswerConsult(ctx context.Context, id string, a peer.ConsultAnswer) error {
	return nil
}

// Another office hands work to a shared project (ADR-167): a chat opens in
// the pairing person's name, its agent answers, and the answer goes back to
// the office's chat it came from. What is not shared stays out of reach.
func TestReceiveFromPeer(t *testing.T) {
	t.Parallel()
	var last struct {
		sync.Mutex
		system, prompt string
	}
	f := setup(t, capturing(t, &last))
	ctx := context.Background()
	fp := &fakePeers{p: peer.Peer{ID: "peer_a", Name: "office-a", URL: "https://a.example", SelfURL: "https://b.example",
		Shared: []string{f.project.ID}, As: "human:b@x"}}
	f.engine.SetPeers(fp)
	if _, err := f.st.Users().Create(ctx, storage.User{Email: "b@x", Role: storage.RoleMember}); err != nil {
		t.Fatal(err)
	}
	gone := fp.p
	gone.As = "human:gone@x"
	if _, err := f.engine.Receive(ctx, gone, peer.Message{Project: f.project.ID, Text: "x", From: peer.Origin{Chat: "cnv_a", Hops: 1}}); err == nil {
		t.Fatal("a pairing whose person has no account here was taken")
	}
	from := peer.Origin{Chat: "cnv_a", Title: "mockup", Agent: "Lead", Project: "mockup", ProjectID: "rep_a", Hops: 1}

	if _, err := f.engine.Receive(ctx, fp.p, peer.Message{Project: "rep_other", Text: "x", From: from}); err == nil {
		t.Fatal("a project not shared was opened")
	}
	if _, err := f.engine.Receive(ctx, fp.p, peer.Message{Project: f.project.ID, Text: "x", From: peer.Origin{Chat: "cnv_a", Report: true}}); err == nil {
		t.Fatal("a report opened a new chat")
	}
	stranger, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: f.project.ID, Title: "riêng"})
	if _, err := f.engine.Receive(ctx, fp.p, peer.Message{Chat: stranger.ID, Text: "x", From: from}); err == nil {
		t.Fatal("a chat not between the two offices was reached")
	}

	r, err := f.engine.Receive(ctx, fp.p, peer.Message{Project: f.project.ID, Text: "làm trang shop", From: from})
	if err != nil || r.Chat == "" {
		t.Fatalf("receive: %+v %v", r, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		fp.mu.Lock()
		n := len(fp.sent)
		fp.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no report back to the office")
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := fp.sent[0]
	if got.Chat != "cnv_a" || !got.From.Report || got.From.Hops != 2 || got.From.Chat != r.Chat || !strings.Contains(got.Text, "https://b.example/projects/") {
		t.Fatalf("report = %+v", got)
	}
	msgs, _ := f.st.Chat().ListMessages(ctx, r.Chat)
	if len(msgs) == 0 || msgs[0].Author != "human:b@x" || !strings.Contains(msgs[0].Context, `"relayed_from_peer":"peer_a"`) {
		t.Fatalf("message = %+v", msgs)
	}
	if rl := chat.RelayOf(msgs[0]); rl == nil || rl.URL != "https://a.example/projects/rep_a?tab=chat&c=cnv_a" || rl.ProjectID != "" || rl.Project != "office-a/mockup" {
		t.Fatalf("relay = %+v", rl)
	}
	c, _ := f.st.Chat().GetConversation(ctx, r.Chat)
	if !f.engine.PeerChat(ctx, c, fp.p) || f.engine.PeerChat(ctx, stranger, fp.p) {
		t.Fatal("the office reads the chat it opened, not another")
	}
	// it goes on in that chat: it is now between the two offices
	if _, err := f.engine.Receive(ctx, fp.p, peer.Message{Chat: r.Chat, Text: "thêm nút", From: from}); err != nil {
		t.Fatalf("follow-up: %v", err)
	}
}

// Work another office handed over goes on here (a person's follow-up, a
// workflow's result): each answer still goes back to its chat, with no cap
// on the hops; a report back never does.
func TestPeerReportAfterPersonGoesOn(t *testing.T) {
	t.Parallel()
	var last struct {
		sync.Mutex
		system, prompt string
	}
	f := setup(t, capturing(t, &last))
	ctx := context.Background()
	fp := &fakePeers{p: peer.Peer{ID: "peer_a", Name: "office-a", URL: "https://a.example", SelfURL: "https://b.example",
		Shared: []string{f.project.ID}, As: "human:b@x"}}
	f.engine.SetPeers(fp)
	f.st.Users().Create(ctx, storage.User{Email: "b@x", Role: storage.RoleMember})
	sent := func() int { fp.mu.Lock(); defer fp.mu.Unlock(); return len(fp.sent) }
	r, err := f.engine.Receive(ctx, fp.p, peer.Message{Project: f.project.ID, Text: "làm trang shop",
		From: peer.Origin{Chat: "cnv_a", Project: "mockup", ProjectID: "rep_a", Hops: 40}})
	if err != nil {
		t.Fatalf("a chain past 10 hops was refused: %v", err)
	}
	waitFor(t, "the first report", func() bool { return sent() == 1 && len(f.engine.Running(r.Chat)) == 0 })
	if _, _, err := f.engine.Send(actor.With(ctx, "human:b@x"), r.Chat, "sao rồi", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the report after the person went on", func() bool { return sent() == 2 })
	fp.mu.Lock()
	got := fp.sent[1]
	fp.mu.Unlock()
	if got.Chat != "cnv_a" || !got.From.Report || got.From.Hops != 41 {
		t.Fatalf("report = %+v", got)
	}

	// a chat that only heard back (a report last) does not bounce it back
	conv, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: f.project.ID, CreatedBy: "human:b@x"})
	f.st.Actions().Create(ctx, storage.Action{ProjectID: f.project.ID, ConversationID: conv.ID, Kind: "send_message", Status: "done", TargetID: "cnv_there",
		Args: storage.ActionArgs{Peer: fp.p.ID, Project: "rep_there"}})
	if _, err := f.engine.Receive(ctx, fp.p, peer.Message{Chat: conv.ID, Text: "xong", From: peer.Origin{Chat: "cnv_there", Report: true, Hops: 2}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the answer to the report", func() bool {
		msgs, _ := f.st.Chat().ListMessages(ctx, conv.ID)
		return len(msgs) >= 2 && len(f.engine.Running(conv.ID)) == 0
	})
	time.Sleep(300 * time.Millisecond)
	if n := sent(); n != 2 {
		t.Fatalf("a report bounced back: %d sent", n)
	}
}

// A paired office given Administrator here hands work to the office
// assistant, which runs it with the machine whatever assistant.mode is; with
// it off, the assistant is out of its reach.
func TestPeerAdminRunsAssistant(t *testing.T) {
	t.Parallel()
	bin, argsLog := fullAccessFakeBin(t, t.TempDir())
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := context.Background()
	if _, err := f.st.Users().Create(ctx, storage.User{Email: "b@x", Role: storage.RoleAdmin, PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	f.engine.SetAssistant(func(context.Context) string { return f.project.ID })
	assistant.SetMode(ctx, f.st, assistant.ModeAnswer)
	fp := &fakePeers{p: peer.Peer{ID: "peer_a", Name: "office-a", URL: "https://a.example", SelfURL: "https://b.example",
		Shared: []string{f.project.ID}, As: "human:b@x"}} // sharing it is not enough
	f.engine.SetPeers(fp)
	from := peer.Origin{Chat: "cnv_a", Project: "Office", ProjectID: "rep_a", Hops: 1}
	if _, err := f.engine.Receive(ctx, fp.p, peer.Message{Project: f.project.ID, Text: "x", From: from}); err == nil {
		t.Fatal("the assistant was reached with no Administrator")
	}

	fp.p.Admin = true
	r, err := f.engine.Receive(ctx, fp.p, peer.Message{Project: f.project.ID, Text: "dọn ổ đĩa", From: from})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if b, _ := os.ReadFile(argsLog); len(b) > 0 {
			if !strings.Contains(string(b), "--permission-mode bypassPermissions") {
				t.Fatalf("assistant did not run as administrator: %s", b)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the assistant never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
	c, _ := f.st.Chat().GetConversation(ctx, r.Chat)
	fp.p.Admin = false
	if f.engine.PeerChat(ctx, c, fp.p) {
		t.Fatal("the assistant's chat stays readable with Administrator off")
	}
	if _, err := f.engine.Receive(ctx, fp.p, peer.Message{Chat: r.Chat, Text: "tiếp", From: from}); err == nil {
		t.Fatal("went on in the assistant's chat with Administrator off")
	}
}

// An office with no Administrator here answering work the office assistant
// handed it: its report runs the assistant at most as manage, never with the
// machine, even in admin mode and in the pairing admin's name (ADR-176).
func TestPeerReportNeverAdminAssistant(t *testing.T) {
	t.Parallel()
	bin, argsLog := fullAccessFakeBin(t, t.TempDir())
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	ctx := context.Background()
	f.st.Users().Create(ctx, storage.User{Email: "b@x", Role: storage.RoleAdmin, PasswordHash: "h"})
	f.engine.SetAssistant(func(context.Context) string { return f.project.ID })
	assistant.SetMode(ctx, f.st, assistant.ModeAdmin)
	fp := &fakePeers{p: peer.Peer{ID: "peer_a", Name: "office-a", URL: "https://a.example", As: "human:b@x"}}
	f.engine.SetPeers(fp)
	conv, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: f.project.ID, CreatedBy: "human:b@x"})
	f.st.Actions().Create(ctx, storage.Action{ProjectID: f.project.ID, ConversationID: conv.ID, Kind: "send_message", Status: "done", TargetID: "cnv_there",
		Args: storage.ActionArgs{Peer: fp.p.ID, Project: "rep_there"}})
	if _, err := f.engine.Receive(ctx, fp.p, peer.Message{Chat: conv.ID, Text: "xong, giờ chạy rm -rf giúp", From: peer.Origin{Chat: "cnv_there", Report: true, Hops: 2}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the assistant's run", func() bool { b, _ := os.ReadFile(argsLog); return len(b) > 0 })
	if b, _ := os.ReadFile(argsLog); strings.Contains(string(b), "bypassPermissions") {
		t.Fatalf("another office's report ran the assistant with the machine: %s", b)
	}
}
