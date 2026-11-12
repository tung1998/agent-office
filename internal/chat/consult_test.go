package chat_test

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// consultFixture: project demo (the fixture's) links to "support", whose
// agent CSKH it may ask; a chat of demo where a person wrote.
type consultFixture struct {
	fixture
	support storage.Repo
	expert  storage.Agent
	conv    storage.Conversation
	asker   storage.Agent
}

func newConsult(t *testing.T, f fixture) consultFixture {
	t.Helper()
	ctx := actor.With(context.Background(), "human:a@x")
	f.st.Users().Create(ctx, storage.User{Email: "a@x", Role: storage.RoleMember, PasswordHash: "h"})
	sup, _ := f.st.Repos().Create(ctx, storage.Repo{Name: "support", Path: t.TempDir()})
	ex, err := f.st.Agents().Create(ctx, storage.Agent{ProjectID: sup.ID, Key: "cskh", Name: "CSKH", Role: "Trả lời khách", ModelTier: storage.TierBalanced})
	if err != nil {
		t.Fatal(err)
	}
	sup.DefaultAgentID = ex.ID
	f.st.Repos().Update(ctx, sup)
	pol := perm.LoadPolicy(ctx, f.st, f.project.ID)
	pol.LinkedProjects = []string{sup.ID}
	perm.SavePolicy(ctx, f.st, f.project.ID, pol)
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	turn, _, err := f.engine.Send(ctx, conv.ID, "khách đòi hoàn tiền thì trả lời sao?", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	asker, _ := f.st.Agents().Get(ctx, conv.AgentID)
	return consultFixture{fixture: f, support: sup, expert: ex, conv: conv, asker: asker}
}

func (c consultFixture) ask(t *testing.T, run, expert string) (string, error) {
	c.engine.TestTurn(run, c.conv.ID, "human:a@x", c.asker.ID, c.asker.Name)
	return c.engine.AskExpert(context.Background(), officetools.Scope{ProjectID: c.project.ID, ConversationID: c.conv.ID, RunRef: run, Agent: c.asker.Name}, expert, "Khách đòi hoàn tiền đơn đã giao, trả lời thế nào?")
}

// waitFor polls cond for up to 15 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// An agent asks an expert of a linked project (ADR-177): the expert answers
// in a hidden, read-only chat of its own project, its answer comes into the
// asking chat as its message, and the asking agent is called again.
func TestAskExpertLocal(t *testing.T) {
	t.Parallel()
	var last struct {
		sync.Mutex
		system, prompt string
	}
	c := newConsult(t, setup(t, capturing(t, &last)))
	ctx := context.Background()
	if ex := c.engine.Experts(ctx, c.project.ID); len(ex) != 1 || ex[0].Label != "support/CSKH" {
		t.Fatalf("experts = %+v", ex)
	}
	if _, err := c.ask(t, "run-x", "nobody"); err == nil {
		t.Fatal("an unknown expert was asked")
	}
	out, err := c.ask(t, "run-1", "cskh")
	if err != nil || !strings.Contains(out, "support/CSKH") {
		t.Fatalf("ask: %q %v", out, err)
	}
	var answer storage.Message
	waitFor(t, "the expert's answer", func() bool {
		msgs, _ := c.st.Chat().ListMessages(ctx, c.conv.ID)
		for _, m := range msgs {
			if chat.ConsultOf(m) != nil {
				answer = m
				return true
			}
		}
		return false
	})
	if x := chat.ConsultOf(answer); answer.Content != "ok" || x.Agent != "CSKH" || x.Project != "support" || !strings.HasPrefix(x.Link, "/projects/"+c.support.ID) || answer.Author != "CSKH (support)" {
		t.Fatalf("answer = %+v %+v", answer, x)
	}
	// the expert's chat: hidden, read only, the question only
	list, _ := c.st.Chat().ListConversations(ctx, c.support.ID, 50)
	if len(list) != 0 {
		t.Fatalf("the expert's chat is listed: %+v", list)
	}
	exConv, err := c.st.Chat().GetConversation(ctx, strings.TrimPrefix(chat.ConsultOf(answer).Link, "/projects/"+c.support.ID+"?tab=chat&c="))
	if err != nil || exConv.Purpose != "consult" || exConv.Mode != perm.Read || exConv.AgentID != c.expert.ID {
		t.Fatalf("expert chat = %+v %v", exConv, err)
	}
	q, _ := c.st.Chat().ListMessages(ctx, exConv.ID)
	if len(q) == 0 || !strings.Contains(q[0].Content, "Khách đòi hoàn tiền") || strings.Contains(q[0].Content, "trả lời sao?") {
		t.Fatalf("expert saw = %+v", q)
	}
	// the agent that asked goes on from the answer
	waitFor(t, "the asker called again", func() bool {
		last.Lock()
		defer last.Unlock()
		return strings.Contains(last.prompt, "The expert CSKH (support) answered")
	})
}

// Who may be asked, and how often: an expert of another office (its answer
// comes back over the pairing), the caps, a project lending no one.
func TestAskExpertPeerAndLimits(t *testing.T) {
	t.Parallel()
	var last struct {
		sync.Mutex
		system, prompt string
	}
	c := newConsult(t, setup(t, capturing(t, &last)))
	ctx := context.Background()
	p := peer.Peer{ID: "peer_b", Name: "office-b", URL: "https://b.example"}
	fp := &fakePeers{p: p, links: []peer.Link{{Peer: p, Project: peer.RemoteProject{ID: "rep_kb", Name: "kb", Experts: []peer.RemoteExpert{{ID: "agt_kb", Name: "Kiến thức"}}}}}}
	c.engine.SetPeers(fp)
	for i := 0; i < 3; i++ {
		if _, err := c.ask(t, "run-"+string(rune('a'+i)), "office-b/kb/Kiến thức"); err != nil {
			t.Fatalf("ask %d: %v", i, err)
		}
	}
	if len(fp.asked) != 3 || fp.asked[0].Project != "rep_kb" || fp.asked[0].Agent != "agt_kb" || fp.asked[0].From.Chat != c.conv.ID {
		t.Fatalf("asked = %+v", fp.asked)
	}
	if _, err := c.ask(t, "run-d", "support/CSKH"); err == nil {
		t.Fatal("a fourth question while three wait")
	}
	// the answer from that office, once, shown as the expert's
	id := fp.asked[0].ID
	if err := c.engine.ConsultAnswered(ctx, peer.Peer{ID: "peer_other"}, peer.ConsultAnswer{ID: id, Answer: "x"}); err == nil {
		t.Fatal("another office answered a question it was not asked")
	}
	if err := c.engine.ConsultAnswered(ctx, p, peer.ConsultAnswer{ID: id, Answer: "Phí ship 30k"}); err != nil {
		t.Fatal(err)
	}
	c.engine.ConsultAnswered(ctx, p, peer.ConsultAnswer{ID: id, Answer: "again"})
	msgs, _ := c.st.Chat().ListMessages(ctx, c.conv.ID)
	n := 0
	for _, m := range msgs {
		if x := chat.ConsultOf(m); x != nil {
			n++
			if m.Content != "Phí ship 30k" || x.Office != "office-b" || x.Link != "" {
				t.Fatalf("answer = %+v %+v", m, x)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d answers shown", n)
	}

	none := []string{}
	pol := perm.LoadPolicy(ctx, c.st, c.support.ID)
	pol.Experts = &none
	perm.SavePolicy(ctx, c.st, c.support.ID, pol)
	fp.links = nil
	if ex := c.engine.Experts(ctx, c.project.ID); len(ex) != 0 {
		t.Fatalf("a project lending no one still lends: %+v", ex)
	}
}

// An expert whose agent runs as administrator elsewhere still only reads.
func TestAskExpertNeverFullAccess(t *testing.T) {
	t.Parallel()
	bin, argsLog := fullAccessFakeBin(t, t.TempDir())
	c := newConsult(t, setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	}))
	ctx := context.Background()
	c.st.Users().Create(ctx, storage.User{Email: "admin@x.io", Role: storage.RoleAdmin, PasswordHash: "h"})
	c.expert.Permissions.Level, c.expert.Permissions.FullAccess, c.expert.Permissions.FullAccessBy = perm.Operate, true, "admin@x.io"
	c.st.Agents().Update(ctx, c.expert)
	os.Remove(argsLog)
	if _, err := c.ask(t, "run-1", "CSKH"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the expert's run", func() bool {
		list, _ := c.st.Consults().Asked(ctx, c.conv.ID)
		return len(list) == 0
	})
	b, _ := os.ReadFile(argsLog)
	if len(b) == 0 || strings.Contains(string(b), "bypassPermissions") {
		t.Fatalf("expert args = %s", b)
	}
}

// Another office's questions: only to an agent a shared project lends,
// never to the office assistant (not even with Administrator), once each;
// an answer only to a question asked of that office.
func TestReceiveConsult(t *testing.T) {
	t.Parallel()
	var last struct {
		sync.Mutex
		system, prompt string
	}
	c := newConsult(t, setup(t, capturing(t, &last)))
	ctx := context.Background()
	c.st.Users().Create(ctx, storage.User{Email: "b@x", Role: storage.RoleAdmin, PasswordHash: "h"})
	c.engine.SetAssistant(func(context.Context) string { return c.project.ID })
	fp := &fakePeers{p: peer.Peer{ID: "peer_a", Name: "office-a", URL: "https://a.example", Shared: []string{c.support.ID, c.project.ID}, As: "human:b@x", Admin: true}}
	c.engine.SetPeers(fp)
	q := peer.ConsultRequest{ID: "cst_there", Project: c.support.ID, Agent: c.expert.ID, Question: "giá ship?", From: peer.ConsultFrom{Chat: "cnv_a", Project: "shop", Agent: "Lead"}}
	bad := []peer.ConsultRequest{
		{ID: "x1", Project: c.project.ID, Agent: c.asker.ID, Question: "q", From: q.From}, // the assistant
		{ID: "x2", Project: c.support.ID, Agent: c.asker.ID, Question: "q", From: q.From}, // not lent
		{ID: "x3", Project: "rep_other", Agent: c.expert.ID, Question: "q", From: q.From}, // not shared
	}
	for _, b := range bad {
		if _, err := c.engine.ReceiveConsult(ctx, fp.p, b); err == nil {
			t.Fatalf("taken: %+v", b)
		}
	}
	r, err := c.engine.ReceiveConsult(ctx, fp.p, q)
	if err != nil || r.Agent != "CSKH" || r.Project != "support" {
		t.Fatalf("receive: %+v %v", r, err)
	}
	if _, err := c.engine.ReceiveConsult(ctx, fp.p, q); err != nil { // a retry: once
		t.Fatal(err)
	}
	waitFor(t, "the expert's answer", func() bool {
		rec, err := c.st.Consults().GetIn(ctx, fp.p.ID, q.ID)
		return err == nil && rec.Status == storage.ConsultAnswered
	})
	if err := c.engine.ConsultAnswered(ctx, fp.p, peer.ConsultAnswer{ID: "cst_unknown", Answer: "x"}); err == nil {
		t.Fatal("an answer to no question was taken")
	}
}
