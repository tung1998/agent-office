package peer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

type office struct {
	st  storage.Store
	svc *Service
	srv *httptest.Server
	got []Message // what came in on /api/peer/relay
}

// newOffice serves the peer endpoints as the API does.
func newOffice(t *testing.T) *office {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlite.Open(filepath.Join(dir, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(dir, "k"))
	o := &office{st: st, svc: New(st, box)}
	auth := func(r *http.Request) (Peer, bool) {
		return o.svc.Auth(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/peer/pair", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		out, err := o.svc.Accept(r.Context(), body)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusForbidden)
			return
		}
		w.Write(out)
	})
	mux.HandleFunc("POST /api/peer/relay", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth(r); !ok {
			http.Error(w, `{"error":"denied"}`, http.StatusUnauthorized)
			return
		}
		var m Message
		json.NewDecoder(r.Body).Decode(&m)
		o.got = append(o.got, m)
		json.NewEncoder(w).Encode(Reply{Chat: "cnv_there", Title: "t", Project: m.Project})
	})
	mux.HandleFunc("PUT /api/peer/projects", func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth(r)
		if !ok {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		var in struct{ Projects []RemoteProject }
		json.NewDecoder(r.Body).Decode(&in)
		o.svc.SetRemote(r.Context(), p.ID, in.Projects)
	})
	mux.HandleFunc("GET /api/peer/projects", func(w http.ResponseWriter, r *http.Request) {
		p, ok := auth(r)
		if !ok {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"projects": o.svc.SharedProjects(r.Context(), p)})
	})
	mux.HandleFunc("POST /api/peer/request", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := o.svc.TakeRequest(r.Context(), body, "127.0.0.1", true); err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("POST /api/peer/request/answer", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		out, err := o.svc.Answer(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), body)
		if err != nil {
			http.Error(w, `{"error":"denied"}`, http.StatusForbidden)
			return
		}
		w.Write(out)
	})
	o.srv = httptest.NewServer(mux)
	t.Cleanup(o.srv.Close)
	return o
}

// Two offices pair with a one-time invite; each then calls the other with
// the key it was given, and reaches only the projects shared with it.
func TestPairAndSend(t *testing.T) {
	ctx := context.Background()
	a, b := newOffice(t), newOffice(t)
	shop, _ := b.st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir(), Description: "the store"})
	b.st.Repos().Create(ctx, storage.Repo{Name: "private", Path: t.TempDir()})

	code, _, err := b.svc.Invite(ctx, "human:b@x", "office-b", b.srv.URL, []string{shop.ID})
	if err != nil || !strings.HasPrefix(code, "ofi_") {
		t.Fatalf("invite: %q %v", code, err)
	}
	pa, err := a.svc.Join(ctx, "human:a@x", code, "office-a", a.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if pa.Name != "office-b" || len(pa.Remote) != 1 || pa.Remote[0].Name != "shop" || pa.Remote[0].Description != "the store" {
		t.Fatalf("A sees B as %+v", pa)
	}
	if _, err := a.svc.Join(ctx, "human:a@x", code, "office-a", a.srv.URL); err == nil {
		t.Fatal("an invite works only once")
	}
	// paired again (a new invite): the same office keeps its id, links hold
	code2, _, _ := b.svc.Invite(ctx, "human:b@x", "office-b", b.srv.URL, []string{shop.ID})
	if again, err := a.svc.Join(ctx, "human:a@x", code2, "office-a", a.srv.URL); err != nil || again.ID != pa.ID {
		t.Fatalf("pair again: %v %s != %s", err, again.ID, pa.ID)
	}
	if l, _ := a.svc.List(ctx); len(l) != 1 {
		t.Fatalf("A has %d peers", len(l))
	}
	bl, _ := b.svc.List(ctx)
	if len(bl) != 1 || bl[0].Name != "office-a" || bl[0].As != "human:b@x" || bl[0].Shared[0] != shop.ID {
		t.Fatalf("B sees A as %+v", bl)
	}

	// A links its project to B's shop and sends there
	mine, _ := a.st.Repos().Create(ctx, storage.Repo{Name: "mine", Path: t.TempDir()})
	perm.SavePolicyIf(ctx, a.st, mine.ID, "", perm.Policy{LinkedProjects: []string{RefPrefix + pa.ID + "/" + shop.ID}})
	p, rp, err := Resolve(ctx, a.st, a.svc, mine.ID, "office-B/Shop")
	if err != nil || p.ID != pa.ID || rp.ID != shop.ID {
		t.Fatalf("resolve: %v %+v %+v", err, p, rp)
	}
	if _, _, err := Resolve(ctx, a.st, a.svc, mine.ID, "private"); err == nil {
		t.Fatal("a project not linked resolved")
	}
	r, err := a.svc.Send(ctx, pa.ID, Message{Project: shop.ID, Text: "hi", From: Origin{Chat: "c1", Hops: 1}})
	if err != nil || r.Chat != "cnv_there" || len(b.got) != 1 || b.got[0].Text != "hi" {
		t.Fatalf("send: %v %+v %+v", err, r, b.got)
	}
	// and B answers A with the key A gave it
	if _, err := b.svc.Send(ctx, bl[0].ID, Message{Chat: "c1", Text: "done", From: Origin{Chat: "cnv_there", Report: true}}); err != nil || len(a.got) != 1 {
		t.Fatalf("send back: %v %+v", err, a.got)
	}

	// B shares nothing more: A hears of it
	if err := b.svc.SetShared(ctx, bl[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := a.svc.Get(ctx, pa.ID)
		if len(got.Remote) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("A still sees %+v", got.Remote)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(a.svc.LinksOf(ctx, mine.ID)) != 0 {
		t.Fatal("a project no longer shared is still linked")
	}

	// unpaired: its key stops working
	b.svc.Remove(ctx, bl[0].ID)
	if _, err := a.svc.Send(ctx, pa.ID, Message{Project: shop.ID, Text: "x", From: Origin{Chat: "c1"}}); err == nil {
		t.Fatal("a removed office's key still works")
	}
}

func TestInviteExpires(t *testing.T) {
	ctx := context.Background()
	a, b := newOffice(t), newOffice(t)
	code, _, _ := b.svc.Invite(ctx, "human:b@x", "b", b.srv.URL, nil)
	b.svc.now = func() time.Time { return time.Now().Add(inviteTTL + time.Minute) }
	if _, err := a.svc.Join(ctx, "human:a@x", code, "a", a.srv.URL); err == nil {
		t.Fatal("an expired invite was accepted")
	}
	if _, err := a.svc.Join(ctx, "human:a@x", "ofi_garbage", "a", a.srv.URL); err == nil {
		t.Fatal("a bad code was accepted")
	}
}

// An invite from an office reached over the network never pairs with one
// claiming to be on this machine (its reports would call local services),
// and a refused try leaves the invite for a corrected one.
func TestAcceptLoopbackGuard(t *testing.T) {
	ctx := context.Background()
	b := newOffice(t)
	code, _, _ := b.svc.Invite(ctx, "human:b@x", "b", "https://b.example", nil)
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, "ofi_"))
	var c struct{ I, S string }
	json.Unmarshal(raw, &c)
	body := func(u string) []byte {
		out, _ := json.Marshal(pairRequest{Invite: c.I, Secret: c.S, Name: "a", URL: u, Key: NewKey()})
		return out
	}
	if _, err := b.svc.Accept(ctx, body("http://127.0.0.1:8787")); err == nil {
		t.Fatal("a loopback office was paired with a remote invite")
	}
	if _, err := b.svc.Accept(ctx, body("https://a.example")); err != nil {
		t.Fatalf("the invite was burned by the refused try: %v", err)
	}
}

// What the other office says of its projects is kept short and link-safe.
func TestCleanRemote(t *testing.T) {
	got := cleanRemote([]RemoteProject{{ID: "rep_1", Name: strings.Repeat("x", 200), Description: "a\nb"}, {ID: "../x?y"}, {ID: " "}})
	if len(got) != 1 || len([]rune(got[0].Name)) > 81 || got[0].Description != "a b" {
		t.Fatalf("cleanRemote = %+v", got)
	}
}

// A project's git remote reaches the other office with no credentials.
func TestPublicRemote(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/acme/shop.git":                  "https://github.com/acme/shop.git",
		"https://x-access-token:ghp_abc@github.com/a/b.git": "https://github.com/a/b.git",
		"https://user@bitbucket.org/a/b.git?token=1":        "https://bitbucket.org/a/b.git",
		"ssh://git:pw@host:22/a/b.git":                      "ssh://git@host:22/a/b.git",
		"git@github.com:acme/shop.git":                      "git@github.com:acme/shop.git",
		"user:secret@host:a/b.git":                          "",
		"/Users/me/shop":                                    "",
		"file:///Users/me/shop":                             "",
		`C:\repos\shop`:                                     "",
		"":                                                  "",
	} {
		if got := PublicRemote(in); got != want {
			t.Errorf("PublicRemote(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanRemote([]RemoteProject{{ID: "rep_1", Repo: "https://u:p@h.example/a.git"}}); got[0].Repo != "https://h.example/a.git" {
		t.Fatalf("cleanRemote kept %q", got[0].Repo)
	}
}

func TestCleanURL(t *testing.T) {
	ok := map[string]string{
		"https://office.example.com/": "https://office.example.com",
		"https://office.example/sub/": "https://office.example/sub",
		"http://localhost:3000":       "http://localhost:3000",
		"http://127.0.0.1:8787":       "http://127.0.0.1:8787",
		"http://100.101.1.2:3000":     "http://100.101.1.2:3000",
		"http://box.tail1234.ts.net/": "http://box.tail1234.ts.net",
	}
	for in, want := range ok {
		if got, err := CleanURL(in); err != nil || got != want {
			t.Errorf("CleanURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"http://office.example.com", "http://192.168.1.2", "ftp://x", "https://u:p@x", "https://x/?a=1", "office.example"} {
		if _, err := CleanURL(in); err == nil {
			t.Errorf("CleanURL(%q) accepted", in)
		}
	}
}

// Administrator opens this office's assistant to a paired office: listed to
// it as its assistant (never under a shared id), and never a project's link
// on the other side.
func TestAdminOpensAssistant(t *testing.T) {
	o := newOffice(t)
	ctx := context.Background()
	dir := t.TempDir()
	aid, err := assistant.Ensure(ctx, o.st, dir)
	if err != nil {
		t.Fatal(err)
	}
	p := Peer{ID: "peer_x", Name: "x", URL: "https://x.example", Shared: []string{aid}}
	o.st.Settings().Set(ctx, peersKey, []Peer{p})
	if got := o.svc.SharedProjects(ctx, p); len(got) != 0 || p.Opens(aid, aid) {
		t.Fatalf("shared the assistant with no Administrator: %+v", got)
	}
	if err := o.svc.SetAdmin(ctx, p.ID, true); err != nil {
		t.Fatal(err)
	}
	p, _ = o.svc.Get(ctx, p.ID)
	got := o.svc.SharedProjects(ctx, p)
	if len(got) != 1 || got[0].ID != aid || !got[0].Assistant || !p.Opens(aid, aid) || p.Opens("", aid) {
		t.Fatalf("assistant with Administrator = %+v", got)
	}
	if r := cleanRemote(got); !r[0].Assistant {
		t.Fatal("the other office lost the assistant mark")
	}

	// the other side: only the office assistant reaches it, not a project
	p.Remote = got
	o.st.Settings().Set(ctx, peersKey, []Peer{p})
	proj, _ := o.st.Repos().Create(ctx, storage.Repo{Name: "web", Path: t.TempDir()})
	pol := perm.LoadPolicy(ctx, o.st, proj.ID)
	pol.LinkedProjects = []string{RefPrefix + p.ID + "/" + aid}
	if err := perm.SavePolicy(ctx, o.st, proj.ID, pol); err != nil {
		t.Fatal(err)
	}
	if l := Linked(ctx, o.st, o.svc, proj.ID); len(l) != 0 {
		t.Fatalf("a project linked to the other office's assistant: %+v", l)
	}
	if l := Reach(ctx, o.st, o.svc, "", true, false); len(l) != 1 || !l[0].Project.Assistant {
		t.Fatalf("the office assistant does not reach it: %+v", l)
	}
	if l := Reach(ctx, o.st, o.svc, proj.ID, false, false); len(l) != 0 {
		t.Fatalf("a project's run reaches the other office's assistant: %+v", l)
	}
	if l := Reach(ctx, o.st, o.svc, proj.ID, false, true); len(l) != 1 || !l[0].Project.Assistant {
		t.Fatalf("an administrator's run does not reach the other office's assistant: %+v", l)
	}
}

// Pairing by address (ADR-177): B asks A, both show the same match code, A's
// admin accepts; then each calls the other with the key it was given. An
// answer with another key is refused.
func TestConnectByAddress(t *testing.T) {
	ctx := context.Background()
	a, b := newOffice(t), newOffice(t)
	shop, _ := a.st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	kb, _ := b.st.Repos().Create(ctx, storage.Repo{Name: "kb", Path: t.TempDir()})

	o, err := b.svc.Connect(ctx, "human:b@x", a.srv.URL, "office-b", b.srv.URL, []string{kb.ID})
	if err != nil {
		t.Fatal(err)
	}
	reqs := a.svc.Requests(ctx)
	if len(reqs) != 1 || reqs[0].Code != o.Code || reqs[0].Name != "office-b" || reqs[0].URL != b.srv.URL {
		t.Fatalf("A got %+v, B shows %s", reqs, o.Code)
	}
	if l, _ := a.svc.List(ctx); len(l) != 0 {
		t.Fatal("paired before an admin accepted")
	}
	// an answer with a key B never gave: refused
	if _, err := b.svc.Answer(ctx, NewKey(), []byte(`{"nonce":"`+o.ID+`","key":"`+NewKey()+`"}`)); err == nil {
		t.Fatal("an answer with another key was taken")
	}

	pa, err := a.svc.AcceptRequest(ctx, "human:a@x", reqs[0].ID, "office-a", []string{shop.ID})
	if err != nil {
		t.Fatal(err)
	}
	if pa.Name != "office-b" || len(pa.Remote) != 1 || pa.Remote[0].ID != kb.ID || pa.Admin {
		t.Fatalf("A sees B as %+v", pa)
	}
	bl, _ := b.svc.List(ctx)
	if len(bl) != 1 || bl[0].Name != "office-a" || len(bl[0].Remote) != 1 || bl[0].Remote[0].ID != shop.ID || bl[0].As != "human:b@x" {
		t.Fatalf("B sees A as %+v", bl)
	}
	if len(a.svc.Requests(ctx)) != 0 || len(b.svc.OutgoingList(ctx)) != 0 {
		t.Fatal("the request is still open")
	}
	// the keys work both ways
	if _, err := a.svc.Send(ctx, pa.ID, Message{Project: kb.ID, Text: "hi", From: Origin{Chat: "c1", Hops: 1}}); err != nil || len(b.got) != 1 {
		t.Fatalf("A to B: %v", err)
	}
	if _, err := b.svc.Send(ctx, bl[0].ID, Message{Project: shop.ID, Text: "hi", From: Origin{Chat: "c2", Hops: 1}}); err != nil || len(a.got) != 1 {
		t.Fatalf("B to A: %v", err)
	}

	// a request turned down is gone; a bad one never kept
	b.svc.Connect(ctx, "human:b@x", a.srv.URL, "office-b", b.srv.URL, nil)
	r2 := a.svc.Requests(ctx)
	if len(r2) != 1 || a.svc.DropRequest(ctx, r2[0].ID) != nil || len(a.svc.Requests(ctx)) != 0 {
		t.Fatalf("drop: %+v", r2)
	}
	if err := a.svc.TakeRequest(ctx, []byte(`{"nonce":"short","key":"x"}`), "1.2.3.4", false); err == nil {
		t.Fatal("a bad request was kept")
	}
	// an address on this machine, from elsewhere: whatever it says of "to" (SSRF)
	ssrf := `{"nonce":"0123456789abcdef0123","name":"x","url":"http://127.0.0.1:6379","to":"http://localhost:8080","key":"` + NewKey() + `"}`
	if err := a.svc.TakeRequest(ctx, []byte(ssrf), "203.0.113.9", false); err == nil {
		t.Fatal("a request pointing at this machine was kept from a remote address")
	}
	if MatchCode("n", "a", "b", "k") == MatchCode("n", "a", "b", "k2") {
		t.Fatal("the match code does not cover the key")
	}
}
