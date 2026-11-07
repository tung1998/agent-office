package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/team"
)

// Two offices pair through the dashboard API (ADR-167): B invites sharing
// one project, A joins with the code; A then reaches only that project, and
// the peer endpoints refuse a call without its key.
func TestPeerPairing(t *testing.T) {
	ctx := context.Background()
	a, b := setup(t), setup(t)
	shop, _ := b.st.Repos().Create(ctx, storage.Repo{Name: "shop", GitRemote: "https://bot:ghp_x@git.example/acme/shop.git", Path: t.TempDir()})
	private, _ := b.st.Repos().Create(ctx, storage.Repo{Name: "private", Path: t.TempDir()})
	team.NewService(b.st, nil).AddFromLibrary(ctx, shop.ID, team.StarterAgent)
	cb, ca := b.client(t), a.client(t)
	login(t, b, cb, "admin@x.io", "admin-password")
	login(t, a, ca, "admin@x.io", "admin-password")

	// members do not pair
	cm := b.client(t)
	login(t, b, cm, "member@x.io", "member-password")
	if resp, _ := do(t, cm, "POST", b.srv.URL+"/api/peers/invite", map[string]any{"name": "b", "url": b.srv.URL}, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member invite = %d", resp.StatusCode)
	}

	resp, out := do(t, cb, "POST", b.srv.URL+"/api/peers/invite", map[string]any{"name": "office-b", "url": b.srv.URL, "shared": []string{shop.ID}}, nil)
	code, _ := out["code"].(string)
	if resp.StatusCode != http.StatusCreated || !strings.HasPrefix(code, "ofi_") {
		t.Fatalf("invite = %d %v", resp.StatusCode, out)
	}
	resp, out = do(t, ca, "POST", a.srv.URL+"/api/peers/join", map[string]any{"code": code, "name": "office-a", "url": a.srv.URL}, nil)
	if resp.StatusCode != http.StatusCreated || out["name"] != "office-b" {
		t.Fatalf("join = %d %v", resp.StatusCode, out)
	}
	pa, _ := out["id"].(string)
	if _, list := do(t, cb, "GET", b.srv.URL+"/api/peers", nil, nil); len(list["peers"].([]any)) != 1 {
		t.Fatalf("B's peers = %v", list)
	}

	// a project of A links to B's shop: the policy takes the ref
	mine, _ := a.st.Repos().Create(ctx, storage.Repo{Name: "mine", Path: t.TempDir()})
	ref := peer.RefPrefix + pa + "/" + shop.ID
	if resp, out := do(t, ca, "GET", a.srv.URL+"/api/projects/"+mine.ID+"/policy", nil, nil); resp.StatusCode != 200 || !strings.Contains(toJSON(out["offices"]), ref) {
		t.Fatalf("policy offices = %v", out["offices"])
	}
	if resp, out := do(t, ca, "PUT", a.srv.URL+"/api/projects/"+mine.ID+"/policy", map[string]any{"linked_projects": []string{ref}}, nil); resp.StatusCode != 200 {
		t.Fatalf("link = %d %v", resp.StatusCode, out)
	}
	if resp, _ := do(t, ca, "PUT", a.srv.URL+"/api/projects/"+mine.ID+"/policy", map[string]any{"linked_projects": []string{peer.RefPrefix + "peer_nope/x"}}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an unknown office was linked: %d", resp.StatusCode)
	}

	// A sends: a new chat in the shared project, none in the other
	from := peer.Origin{Chat: "cnv_a", Project: "mine", ProjectID: mine.ID, Hops: 1}
	r, err := a.peers.Send(ctx, pa, peer.Message{Project: shop.ID, Text: "làm trang shop", From: from})
	if err != nil || r.Chat == "" {
		t.Fatalf("send: %+v %v", r, err)
	}
	if c, err := b.st.Chat().GetConversation(ctx, r.Chat); err != nil || c.ProjectID != shop.ID {
		t.Fatalf("B's chat: %+v %v", c, err)
	}
	if _, err := a.peers.Send(ctx, pa, peer.Message{Project: private.ID, Text: "x", From: from}); err == nil {
		t.Fatal("a project not shared was reached")
	}

	// A reads and searches that chat (read_link, search_history); not B's others
	deadline := time.Now().Add(5 * time.Second)
	for {
		if msgs, _ := b.st.Chat().ListMessages(ctx, r.Chat); len(msgs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the message never reached B's chat")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if text, of, err := a.peers.ReadChat(ctx, pa, "", r.Chat, ""); err != nil || !strings.Contains(text, "làm trang shop") || of != shop.ID {
		t.Fatalf("read: %q %q %v", text, of, err)
	}
	other, _ := b.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: shop.ID, Title: "riêng"})
	b.st.Chat().AddMessage(ctx, storage.Message{ConversationID: other.ID, Role: "user", Content: "trang shop bí mật"})
	if _, _, err := a.peers.ReadChat(ctx, pa, "", other.ID, ""); err == nil {
		t.Fatal("a chat not between the offices was read")
	}
	hits, err := a.peers.SearchChats(ctx, pa, peer.Search{Query: "shop", Project: shop.ID})
	if err != nil || len(hits) != 1 || hits[0].Chat != r.Chat {
		t.Fatalf("search: %+v %v", hits, err)
	}
	if _, err := a.peers.SearchChats(ctx, pa, peer.Search{Query: "shop", Project: private.ID}); err == nil {
		t.Fatal("a project not shared was searched")
	}
	// through the MCP other_office (ADR-169), by the names agents see
	sc := officetools.Scope{ProjectID: mine.ID, Level: perm.Operate}
	if out, isErr := a.office.CallOther(ctx, sc, "offices", nil); isErr || !strings.Contains(out, "office-b") || !strings.Contains(out, "shop") ||
		!strings.Contains(out, "git: https://git.example/acme/shop.git") || strings.Contains(out, "ghp_x") {
		t.Fatalf("offices (with the git remote, no credentials): %v %s", isErr, out)
	}
	if out, isErr := a.office.CallOther(ctx, sc, "search", json.RawMessage(`{"office":"OFFICE-B","project":"shop","query":"shop"}`)); isErr || !strings.Contains(out, b.srv.URL+"/projects/"+shop.ID) {
		t.Fatalf("search: %v %s", isErr, out)
	}
	link := b.srv.URL + "/projects/" + shop.ID + "?tab=chat&c=" + r.Chat
	if out, isErr := a.office.CallOther(ctx, sc, "read_chat", json.RawMessage(`{"chat":"`+link+`"}`)); isErr || !strings.Contains(out, "làm trang shop") {
		t.Fatalf("read_chat: %v %s", isErr, out)
	}
	// chats lists what A may read: the chat between the two; every chat of shop
	// once B makes A administrator (ADR-211)
	chats := json.RawMessage(`{"office":"office-b","project":"shop"}`)
	if out, isErr := a.office.CallOther(ctx, sc, "chats", chats); isErr || !strings.Contains(out, link) || strings.Contains(out, other.ID) {
		t.Fatalf("chats: %v %s", isErr, out)
	}
	bpeer, _ := b.peers.List(ctx)
	b.peers.SetAdmin(ctx, bpeer[0].ID, true)
	if out, isErr := a.office.CallOther(ctx, sc, "chats", chats); isErr || !strings.Contains(out, other.ID) {
		t.Fatalf("chats as administrator: %v %s", isErr, out)
	}
	if text, _, err := a.peers.ReadChat(ctx, pa, shop.ID, other.ID, ""); err != nil || !strings.Contains(text, "bí mật") {
		t.Fatalf("read as administrator: %q %v", text, err)
	}
	if _, err := a.peers.ListChats(ctx, pa, private.ID, 7); err == nil {
		t.Fatal("chats of a project not shared were listed")
	}
	// tools and call (ADR-212): as administrator, A reads and changes shop's settings
	if out, isErr := a.office.CallOther(ctx, sc, "tools", chats); isErr || !strings.Contains(out, "as administrator") || !strings.Contains(out, "propose_change") {
		t.Fatalf("tools as administrator: %v %s", isErr, out)
	}
	agents, _ := b.st.Agents().List(ctx, shop.ID)
	change := `{"office":"office-b","project":"shop","tool":"propose_change","args":{"resource":"agent","op":"update","id":"` + agents[0].ID + `","patch":{"instructions":"Làm cẩn thận."},"reason":"x"}}`
	if out, isErr := a.office.CallOther(ctx, sc, "call", json.RawMessage(change)); isErr {
		t.Fatalf("propose_change as administrator: %s", out)
	}
	if x, _ := b.st.Agents().Get(ctx, agents[0].ID); x.Instructions != "Làm cẩn thận." {
		t.Fatalf("the change was not done: %q", x.Instructions)
	}
	// an agent's notes: written and read for the agent the call names
	note := `{"office":"office-b","project":"shop","tool":"remember","args":{"agent":"` + agents[0].Name + `","note":"Chạy npm test trước khi báo xong","reason":"x"}}`
	if out, isErr := a.office.CallOther(ctx, sc, "call", json.RawMessage(note)); isErr {
		t.Fatalf("remember: %s", out)
	}
	recall := json.RawMessage(`{"office":"office-b","project":"shop","tool":"recall","args":{"agent":"` + agents[0].Name + `","query":"npm test"}}`)
	if out, isErr := a.office.CallOther(ctx, sc, "call", recall); isErr || !strings.Contains(out, "npm test") {
		t.Fatalf("recall: %v %s", isErr, out)
	}
	b.peers.SetAdmin(ctx, bpeer[0].ID, false)
	// not administrator: read only
	if out, isErr := a.office.CallOther(ctx, sc, "tools", chats); isErr || strings.Contains(out, "propose_change") || !strings.Contains(out, "list") {
		t.Fatalf("tools read only: %v %s", isErr, out)
	}
	if out, isErr := a.office.CallOther(ctx, sc, "call", json.RawMessage(`{"office":"office-b","project":"shop","tool":"list","args":{"resource":"agent"}}`)); isErr || !strings.Contains(out, agents[0].Name) {
		t.Fatalf("list read only: %v %s", isErr, out)
	}
	if _, isErr := a.office.CallOther(ctx, sc, "call", json.RawMessage(change)); !isErr {
		t.Fatal("a change was made without administrator")
	}
	// the old tools point to it
	if out, isErr := a.office.Call(ctx, sc, "search_history", json.RawMessage(`{"query":"shop","project":"office-b/shop"}`)); !isErr || !strings.Contains(out, "other_office") {
		t.Fatalf("search_history: %v %s", isErr, out)
	}
	if out, isErr := a.office.Call(ctx, sc, "read_link", json.RawMessage(`{"url":"`+link+`"}`)); !isErr || !strings.Contains(out, "other_office") {
		t.Fatalf("read_link: %v %s", isErr, out)
	}
	// a project that links to none gets no other_office; the assistant gets every office
	free, _ := a.st.Repos().Create(ctx, storage.Repo{Name: "free", Path: t.TempDir()})
	if tools := a.office.OtherTools(ctx, officetools.Scope{ProjectID: free.ID, Level: perm.Operate}); len(tools) != 0 {
		t.Fatalf("an unlinked project got %d tools", len(tools))
	}
	if _, isErr := a.office.CallOther(ctx, officetools.Scope{ProjectID: free.ID, Level: perm.Operate}, "read_chat", json.RawMessage(`{"chat":"`+link+`"}`)); !isErr {
		t.Fatal("an unlinked project read the other office")
	}
	asst := officetools.Scope{ProjectID: free.ID, Office: true, Level: perm.Operate, ConversationID: "cnv_x"}
	if out, isErr := a.office.CallOther(ctx, asst, "offices", nil); isErr || !strings.Contains(out, "shop") {
		t.Fatalf("assistant offices: %v %s", isErr, out)
	}
	if !a.office.HasOther(ctx, asst, "send") {
		t.Fatal("send: the assistant gets it")
	}
	// one that only answers sees send, which says how to turn it on and sends nothing
	answer := officetools.Scope{ProjectID: free.ID, Office: true, AnswerOnly: true, Level: perm.Read, ConversationID: "cnv_x"}
	if out, isErr := a.office.CallOther(ctx, answer, "send", json.RawMessage(`{"office":"office-b","project":"shop","text":"x","reason":"y"}`)); !isErr || out != officetools.OtherAnswerOnly {
		t.Fatalf("send while answering only: %v %s", isErr, out)
	}

	// the assistant copies shop here as a card (ADR-170); a project's agent cannot
	if a.office.HasOther(ctx, sc, "import") {
		t.Fatal("a project's agent got import")
	}
	aconv, _ := a.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: free.ID})
	asstChat := asst
	asstChat.ConversationID = aconv.ID
	imp, isErr := a.office.CallOther(ctx, asstChat, "import", json.RawMessage(`{"office":"office-b","project":"shop","include":["config","chats","memory"],"path":"`+t.TempDir()+`","reason":"chép về"}`))
	if isErr || !strings.Contains(imp, "approval card") {
		t.Fatalf("import: %v %s", isErr, imp)
	}
	cards, _ := a.st.Actions().List(ctx, aconv.ID, "", "")
	if len(cards) != 1 || cards[0].Kind != "import_project" || cards[0].Args.NewProject == nil || cards[0].Args.NewProject.Name != "shop" {
		t.Fatalf("cards: %+v", cards)
	}
	done, err := a.acts.Decide(ctx, cards[0].ID, true, "admin@x.io")
	if err != nil || done.Status != "done" {
		t.Fatalf("approve import: %+v %v", done, err)
	}
	if list, _ := a.st.Repos().List(ctx); !slices.ContainsFunc(list, func(r storage.Repo) bool { return r.Name == "shop" }) {
		t.Fatal("no shop project here after the import")
	}
	if out, isErr := a.office.CallOther(ctx, answer, "import", json.RawMessage(`{"office":"office-b","project":"shop","reason":"x"}`)); !isErr || !strings.Contains(out, "Chỉ trả lời") {
		t.Fatalf("import while answering only: %v %s", isErr, out)
	}

	// a chat of another project B shares is not read through a link naming shop
	blog, _ := b.st.Repos().Create(ctx, storage.Repo{Name: "blog", Path: t.TempDir()})
	team.NewService(b.st, nil).AddFromLibrary(ctx, blog.ID, team.StarterAgent)
	bp, _ := b.peers.List(ctx)
	if err := b.peers.SetShared(ctx, bp[0].ID, []string{shop.ID, blog.ID}); err != nil {
		t.Fatal(err)
	}
	rb, err := a.peers.Send(ctx, pa, peer.Message{Project: blog.ID, Text: "viết bài blog", From: peer.Origin{Chat: "cnv_a2", Project: "other", Hops: 1}})
	if err != nil {
		t.Fatalf("send to blog: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		if msgs, _ := b.st.Chat().ListMessages(ctx, rb.Chat); len(msgs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the message never reached B's blog chat")
		}
		time.Sleep(20 * time.Millisecond)
	}
	forged := b.srv.URL + "/projects/" + shop.ID + "?tab=chat&c=" + rb.Chat
	if out, isErr := a.office.CallOther(ctx, sc, "read_chat", json.RawMessage(`{"chat":"`+forged+`"}`)); !isErr {
		t.Fatalf("a blog chat was read as shop's: %s", out)
	}
	if _, err := a.peers.Send(ctx, pa, peer.Message{Project: shop.ID, Chat: rb.Chat, Text: "x", From: from}); err == nil {
		t.Fatal("a blog chat took a message sent as shop's")
	}
	// a chat this conversation handed work to: read_chat and send stay after the link is gone
	conv, _ := a.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: free.ID})
	a.st.Actions().Create(ctx, storage.Action{Kind: "send_message", ProjectID: free.ID, ConversationID: conv.ID, Status: "done", TargetID: r.Chat, Args: storage.ActionArgs{Peer: pa, Project: shop.ID}})
	fsc := officetools.Scope{ProjectID: free.ID, ConversationID: conv.ID, Level: perm.Operate}
	if !a.office.HasOther(ctx, fsc, "read_chat") || !a.office.HasOther(ctx, fsc, "send") || a.office.HasOther(ctx, fsc, "search") {
		t.Fatal("tools of a chat that handed work over: read_chat and send only")
	}
	if out, isErr := a.office.CallOther(ctx, fsc, "read_chat", json.RawMessage(`{"office":"office-b","chat":"`+r.Chat+`"}`)); isErr || !strings.Contains(out, "làm trang shop") {
		t.Fatalf("read_chat by id after hand-over: %v %s", isErr, out)
	}

	// no key, a wrong one, a used invite (its reply already got through, so
	// this isn't the lost-reply retry internal/peer's own tests cover; a
	// fresh key Accept() never answered to is correctly denied)
	if resp, _ := do(t, http.DefaultClient, "POST", b.srv.URL+"/api/peer/relay", map[string]any{"project": shop.ID, "text": "x"}, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("relay without key = %d", resp.StatusCode)
	}
	if resp, _ := do(t, http.DefaultClient, "GET", b.srv.URL+"/api/peer/projects", nil, map[string]string{"Authorization": "Bearer ofp_0000"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong key = %d", resp.StatusCode)
	}
	if resp, _ := do(t, ca, "POST", a.srv.URL+"/api/peers/join", map[string]any{"code": code, "name": "office-a", "url": a.srv.URL}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an invite used twice = %d", resp.StatusCode)
	}

	// B unpairs: A's key stops working
	bl, _ := b.peers.List(ctx)
	if resp, _ := do(t, cb, "DELETE", b.srv.URL+"/api/peers/"+bl[0].ID, nil, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove = %d", resp.StatusCode)
	}
	if _, err := a.peers.Send(ctx, pa, peer.Message{Project: shop.ID, Text: "x", From: from}); err == nil {
		t.Fatal("an unpaired office still sends")
	}
}
