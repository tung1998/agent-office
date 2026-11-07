// Package peer links this office to other agent offices (ADR-167): linked
// projects (ADR-155) whose project lives in another office. Two offices pair
// once with a one-time invite code and then call each other's /api/peer/*
// with a key each issued the other (only its hash kept by the issuer).
package peer

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/repos"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
)

const (
	peersKey        = "peers"
	invitesKey      = "peer_invites"
	pendingJoinsKey = "peer_pending_joins"
	// RefPrefix marks a linked project in another office in a project's
	// Policy.LinkedProjects: "peer:<peer id>/<its project id>".
	RefPrefix = "peer:"
	inviteTTL = 10 * time.Minute
)

// RemoteProject is a project the other office lets this one reach.
type RemoteProject struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Repo        string `json:"repo,omitempty"` // its git remote, with no credentials
	// Assistant: the other office's assistant, run as administrator there
	// (it gave this office Admin): only this office's assistant reaches it
	Assistant bool `json:"assistant,omitempty"`
	// Experts: its agents this office may ask (ask_expert, ADR-177)
	Experts []RemoteExpert `json:"experts,omitempty"`
}

// RemoteExpert is an agent of another office's project lent as an expert.
type RemoteExpert struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// Peer is another office this one is paired with.
type Peer struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`         // the other office, as reached from here
	SelfURL   string `json:"self_url"`    // this office, as the other reaches it (links in what goes there)
	OutKeyEnc string `json:"out_key_enc"` // the key this office calls it with, sealed
	InKeyHash string `json:"in_key_hash"` // SHA-256 of the key it calls this office with
	// Shared: this office's projects it may open chats in
	Shared []string `json:"shared"`
	// Remote: its projects this office may reach (as it last told)
	Remote []RemoteProject `json:"remote"`
	// Admin: it may hand work to this office's assistant, which runs it as
	// administrator on this machine (full access), whatever assistant.mode is
	Admin bool `json:"admin,omitempty"`
	// As: the person (human:<email>) who paired; what the other office sends
	// runs in their name here
	As         string     `json:"as"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type invite struct {
	ID              string    `json:"id"`
	SecretHash      string    `json:"secret_hash"`
	Name            string    `json:"name"` // this office's name, as the other will see it
	URL             string    `json:"url"`  // this office, as the other reaches it
	Shared          []string  `json:"shared"`
	As              string    `json:"as"`
	Expires         time.Time `json:"expires"`
	AnswerEnc       string    `json:"answer_enc,omitempty"`        // the key answered with, once accepted (a retry sends the same, until Expires)
	AcceptedURL     string    `json:"accepted_url,omitempty"`      // the URL AnswerEnc was answered to
	AcceptedKeyHash string    `json:"accepted_key_hash,omitempty"` // hash of the Key that office sent; only that exact (URL, Key) may retry, since URL is self-reported
}

// Origin is where a message sent to another office comes from.
type Origin struct {
	Chat      string `json:"chat"`
	Title     string `json:"title,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Project   string `json:"project,omitempty"` // its project's name
	ProjectID string `json:"project_id,omitempty"`
	Hops      int    `json:"hops"`
	Report    bool   `json:"report,omitempty"` // the answer to what was handed over
}

// Message goes to another office's chat: a new chat in Project, or on in Chat.
type Message struct {
	Project string `json:"project,omitempty"`
	Chat    string `json:"chat,omitempty"`
	Text    string `json:"text"`
	From    Origin `json:"from"`
}

// Reply is the chat a Message went to.
type Reply struct {
	Chat    string `json:"chat"`
	Title   string `json:"title,omitempty"`
	Project string `json:"project,omitempty"`
}

// pairRequest is what a joining office sends with the invite.
type pairRequest struct {
	Invite string `json:"invite"`
	Secret string `json:"secret"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Key    string `json:"key"` // the key the inviting office will call it with
}

type pairReply struct {
	Key      string          `json:"key"` // the key the joining office calls with
	Name     string          `json:"name"`
	Projects []RemoteProject `json:"projects"`
}

// Service keeps the paired offices and talks to them.
type Service struct {
	st     storage.Store
	box    *secrets.Box
	client *http.Client
	now    func() time.Time
	mu     sync.Mutex
}

// New builds a Service.
func New(st storage.Store, box *secrets.Box) *Service {
	return &Service{st: st, box: box, now: time.Now,
		client: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // a key never follows a redirect elsewhere
		}}}
}

// List returns the paired offices.
func (s *Service) List(ctx context.Context) ([]Peer, error) {
	var list []Peer
	if _, err := s.st.Settings().Get(ctx, peersKey, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// Get returns one paired office.
func (s *Service) Get(ctx context.Context, id string) (Peer, error) {
	list, err := s.List(ctx)
	if err != nil {
		return Peer{}, err
	}
	for _, p := range list {
		if p.ID == id {
			return p, nil
		}
	}
	return Peer{}, storage.ErrNotFound
}

// update changes the stored list under the lock.
func (s *Service) update(ctx context.Context, fn func([]Peer) ([]Peer, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.List(ctx)
	if err != nil {
		return err
	}
	if list, err = fn(list); err != nil {
		return err
	}
	return s.st.Settings().Set(ctx, peersKey, list)
}

// Remove unpairs an office: its key stops working here at once.
func (s *Service) Remove(ctx context.Context, id string) error {
	return s.update(ctx, func(list []Peer) ([]Peer, error) {
		n := len(list)
		list = slices.DeleteFunc(list, func(p Peer) bool { return p.ID == id })
		if len(list) == n {
			return nil, storage.ErrNotFound
		}
		return list, nil
	})
}

// SetShared sets which of this office's projects the other may reach, and
// tells it (best effort: it can also ask with Refresh).
func (s *Service) SetShared(ctx context.Context, id string, shared []string) error {
	cur, _ := s.Get(ctx, id)
	clean, err := s.projects(ctx, shared, cur.Shared...)
	if err != nil {
		return err
	}
	if err := s.update(ctx, func(list []Peer) ([]Peer, error) {
		for i := range list {
			if list[i].ID == id {
				list[i].Shared = clean
				return list, nil
			}
		}
		return nil, storage.ErrNotFound
	}); err != nil {
		return err
	}
	s.tell(ctx, id)
	return nil
}

// SetAdmin lets the office id run this office's assistant as administrator
// on this machine (on), or stops it at once (off), and tells it.
func (s *Service) SetAdmin(ctx context.Context, id string, on bool) error {
	if err := s.update(ctx, func(list []Peer) ([]Peer, error) {
		for i := range list {
			if list[i].ID == id {
				list[i].Admin = on
				return list, nil
			}
		}
		return nil, storage.ErrNotFound
	}); err != nil {
		return err
	}
	s.tell(ctx, id)
	return nil
}

// tell gives the office id the projects it may now reach (best effort: it
// can also ask with Refresh).
func (s *Service) tell(ctx context.Context, id string) {
	if p, err := s.Get(ctx, id); err == nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = s.call(ctx, p, http.MethodPut, "/api/peer/projects", map[string]any{"projects": s.SharedProjects(ctx, p)}, nil)
		}()
	}
}

// Opens: p may open and read chats in projectID here (the assistant only as
// administrator, assistantID "" = none set up).
func (p Peer) Opens(projectID, assistantID string) bool {
	if projectID == "" {
		return false
	}
	if projectID == assistantID {
		return p.Admin
	}
	return slices.Contains(p.Shared, projectID)
}

// projects keeps the ids of existing projects, once each. An id in gone (one
// shared before) whose project was deleted since is dropped, not an error:
// the dashboard sends back the whole list, stale ids included.
func (s *Service) projects(ctx context.Context, ids []string, gone ...string) ([]string, error) {
	out := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(out, id) {
			continue
		}
		if _, err := s.st.Repos().Get(ctx, id); err != nil {
			if slices.Contains(gone, id) {
				continue
			}
			return nil, fmt.Errorf("không có project %q", id)
		}
		out = append(out, id)
	}
	return out, nil
}

// SharedProjects are this office's projects p may reach, as it sees them.
func (s *Service) SharedProjects(ctx context.Context, p Peer) []RemoteProject {
	out, aid := []RemoteProject{}, assistant.ID(ctx, s.st)
	for _, id := range p.Shared {
		if r, err := s.st.Repos().Get(ctx, id); err == nil && id != aid {
			out = append(out, RemoteProject{ID: r.ID, Name: r.Name, Description: oneLine(r.Description, 300), Repo: PublicRemote(repos.Remote(r.Path, r.GitRemote)),
				Experts: s.experts(ctx, r)})
		}
	}
	if p.Admin && aid != "" {
		out = append(out, RemoteProject{ID: aid, Name: AssistantName, Description: "Trợ lý office của máy này, chạy toàn quyền (administrator) trên máy", Assistant: true})
	}
	return out
}

// experts are the agents project r lends as experts (Policy.Experts).
func (s *Service) experts(ctx context.Context, r storage.Repo) []RemoteExpert {
	var out []RemoteExpert
	for _, id := range perm.LoadPolicy(ctx, s.st, r.ID).ExpertIDs(r.DefaultAgentID) {
		if a, err := s.st.Agents().Get(ctx, id); err == nil && a.ProjectID == r.ID && !a.Disabled {
			out = append(out, RemoteExpert{ID: a.ID, Name: a.Name, Role: oneLine(a.Role, 200)})
		}
	}
	return out
}

// AssistantName is how an office's assistant shows in the other office.
const AssistantName = "Trợ lý office"

// SetRemote keeps the projects the other office now lets this one reach.
func (s *Service) SetRemote(ctx context.Context, id string, projects []RemoteProject) error {
	projects = cleanRemote(projects)
	return s.update(ctx, func(list []Peer) ([]Peer, error) {
		for i := range list {
			if list[i].ID == id {
				list[i].Remote = projects
				return list, nil
			}
		}
		return nil, storage.ErrNotFound
	})
}

// cleanRemote keeps what the other office says of its projects short (it
// goes into prompts and links).
func cleanRemote(projects []RemoteProject) []RemoteProject {
	out := []RemoteProject{}
	for _, r := range projects[:min(len(projects), 200)] {
		if r.ID = strings.TrimSpace(r.ID); r.ID != "" && !strings.ContainsAny(r.ID, "/?#& ") {
			out = append(out, RemoteProject{ID: r.ID, Name: oneLine(cmp.Or(r.Name, r.ID), 80), Description: oneLine(r.Description, 300), Repo: PublicRemote(r.Repo), Assistant: r.Assistant,
				Experts: cleanExperts(r.Experts, r.Assistant)})
		}
	}
	return out
}

// cleanExperts keeps what the other office says of its experts short; its
// assistant lends none.
func cleanExperts(list []RemoteExpert, assistant bool) []RemoteExpert {
	if assistant {
		return nil
	}
	var out []RemoteExpert
	for _, x := range list[:min(len(list), 20)] {
		if x.ID = strings.TrimSpace(x.ID); x.ID != "" && !strings.ContainsAny(x.ID, "/?#& ") {
			out = append(out, RemoteExpert{ID: x.ID, Name: oneLine(cmp.Or(x.Name, x.ID), 80), Role: oneLine(x.Role, 200)})
		}
	}
	return out
}

// Refresh asks the other office which projects it lets this one reach.
func (s *Service) Refresh(ctx context.Context, id string) (Peer, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return Peer{}, err
	}
	var out struct {
		Projects []RemoteProject `json:"projects"`
	}
	if err := s.call(ctx, p, http.MethodGet, "/api/peer/projects", nil, &out); err != nil {
		return Peer{}, err
	}
	if err := s.SetRemote(ctx, id, out.Projects); err != nil {
		return Peer{}, err
	}
	return s.Get(ctx, id)
}

// Invite makes a one-time code (valid inviteTTL) another office joins with:
// it may then reach shared, and what it sends runs as the person as.
func (s *Service) Invite(ctx context.Context, as, name, selfURL string, shared []string) (string, time.Time, error) {
	u, err := CleanURL(selfURL)
	if err != nil {
		return "", time.Time{}, err
	}
	if shared, err = s.projects(ctx, shared); err != nil {
		return "", time.Time{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", time.Time{}, errors.New("thiếu tên office này")
	}
	secret := randomHex(24)
	inv := invite{ID: ids.New("inv"), SecretHash: Hash(secret), Name: name, URL: u, Shared: shared, As: as, Expires: s.now().Add(inviteTTL).UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.invites(ctx)
	list = append(list, inv)
	if err := s.st.Settings().Set(ctx, invitesKey, list); err != nil {
		return "", time.Time{}, err
	}
	raw, _ := json.Marshal(map[string]string{"u": u, "i": inv.ID, "s": secret, "n": name})
	return "ofi_" + base64.RawURLEncoding.EncodeToString(raw), inv.Expires, nil
}

// invites are the open ones (the expired dropped); the caller holds mu.
func (s *Service) invites(ctx context.Context) []invite {
	var list []invite
	_, _ = s.st.Settings().Get(ctx, invitesKey, &list)
	now := s.now()
	return slices.DeleteFunc(list, func(i invite) bool { return now.After(i.Expires) })
}

// pendingJoin is a Join() still in flight: the key sent with it, kept so a
// retry of the same invite (its response may have been lost) sends the same
// key again, instead of a new one Accept() won't recognize.
type pendingJoin struct {
	Invite  string    `json:"invite"`
	KeyEnc  string    `json:"key_enc"`
	Expires time.Time `json:"expires"`
}

// pendingJoins are the open ones (the expired dropped); the caller holds mu.
func (s *Service) pendingJoins(ctx context.Context) []pendingJoin {
	var list []pendingJoin
	_, _ = s.st.Settings().Get(ctx, pendingJoinsKey, &list)
	now := s.now()
	return slices.DeleteFunc(list, func(p pendingJoin) bool { return now.After(p.Expires) })
}

// Join pairs with the office whose invite code this is: this office is
// selfURL to it, named name there.
func (s *Service) Join(ctx context.Context, as, code, name, selfURL string) (Peer, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(strings.TrimSpace(code), "ofi_"))
	var c struct{ U, I, S, N string }
	if err != nil || json.Unmarshal(raw, &c) != nil || c.U == "" || c.I == "" || c.S == "" {
		return Peer{}, errors.New("mã mời không hợp lệ")
	}
	their, err := CleanURL(c.U)
	if err != nil {
		return Peer{}, err
	}
	self, err := CleanURL(selfURL)
	if err != nil {
		return Peer{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Peer{}, errors.New("thiếu tên office này")
	}
	s.mu.Lock()
	list := s.pendingJoins(ctx)
	j := slices.IndexFunc(list, func(x pendingJoin) bool { return x.Invite == c.I })
	var inEnc string
	if j >= 0 {
		inEnc = list[j].KeyEnc // a retry (the first reply may have been lost): send the same key again
	} else {
		if inEnc, err = s.box.Seal(NewKey()); err == nil {
			list = append(list, pendingJoin{Invite: c.I, KeyEnc: inEnc, Expires: s.now().Add(inviteTTL)})
			err = s.st.Settings().Set(ctx, pendingJoinsKey, list)
		}
	}
	s.mu.Unlock()
	if err != nil {
		return Peer{}, err
	}
	in, err := s.box.Open(inEnc) // the key it will call this office with
	if err != nil {
		return Peer{}, err
	}
	p := Peer{ID: ids.New("peer"), Name: c.N, URL: their, SelfURL: self, InKeyHash: Hash(in), Shared: []string{}, As: as, CreatedAt: s.now().UTC()}
	var out pairReply
	if err := s.do(ctx, http.MethodPost, their+"/api/peer/pair", "", pairRequest{Invite: c.I, Secret: c.S, Name: name, URL: self, Key: in}, &out); err != nil {
		return Peer{}, err
	}
	if !strings.HasPrefix(out.Key, "ofp_") {
		return Peer{}, errors.New("office bên kia trả lời không đúng")
	}
	if p.OutKeyEnc, err = s.box.Seal(out.Key); err != nil {
		return Peer{}, err
	}
	p.Name = oneLine(cmp.Or(out.Name, c.N, their), 80)
	p.Remote = cleanRemote(out.Projects)
	if err = s.update(ctx, func(list []Peer) ([]Peer, error) { return upsert(list, &p), nil }); err != nil {
		return Peer{}, err
	}
	s.mu.Lock() // paired: no retry will come for this invite anymore
	list = slices.DeleteFunc(s.pendingJoins(ctx), func(x pendingJoin) bool { return x.Invite == c.I })
	_ = s.st.Settings().Set(ctx, pendingJoinsKey, list) // best effort: it expires on its own either way
	s.mu.Unlock()
	return p, nil
}

// upsert adds p; pairing again with the same office replaces it, under its
// id (projects linking to it keep working).
func upsert(list []Peer, p *Peer) []Peer {
	if i := slices.IndexFunc(list, func(x Peer) bool { return x.URL == p.URL }); i >= 0 {
		p.ID = list[i].ID
		list = slices.Delete(list, i, i+1)
	}
	return append(list, *p)
}

// loopback: an office address on this machine.
func loopback(u string) bool {
	x, err := url.Parse(u)
	if err != nil {
		return false
	}
	h := strings.ToLower(x.Hostname())
	ip, err := netip.ParseAddr(h)
	return h == "localhost" || err == nil && ip.IsLoopback()
}

// Accept answers a joining office's pairing (an invite this office made).
func (s *Service) Accept(ctx context.Context, body []byte) ([]byte, error) {
	var req pairRequest
	if json.Unmarshal(body, &req) != nil {
		return nil, ErrDenied
	}
	s.mu.Lock()
	list := s.invites(ctx)
	i := slices.IndexFunc(list, func(x invite) bool { return x.ID == req.Invite })
	if i < 0 || subtle.ConstantTimeCompare([]byte(list[i].SecretHash), []byte(Hash(req.Secret))) != 1 {
		s.mu.Unlock()
		return nil, ErrDenied
	}
	inv := list[i]
	their, err := CleanURL(req.URL)
	// a joining office on this machine only when this one is reached locally
	// too: else its address could point back at a service here (SSRF)
	if err == nil && loopback(their) && !loopback(inv.URL) {
		err = errors.New("địa chỉ office là máy này: chỉ được khi office mời cũng ở máy này")
	}
	if err == nil && (!strings.HasPrefix(req.Key, "ofp_") || strings.TrimSpace(req.Name) == "") {
		err = ErrDenied
	}
	if err != nil { // the invite stays for a corrected try
		s.mu.Unlock()
		return nil, err
	}
	if list[i].AnswerEnc != "" && (list[i].AcceptedURL != their || list[i].AcceptedKeyHash != Hash(req.Key)) {
		// already answered, and either the URL (self-reported, unverified) or
		// the Key (the joining office's actual secret, sent over TLS) doesn't
		// match: never hand the first office's key to a second caller
		s.mu.Unlock()
		return nil, ErrDenied
	}
	if list[i].AnswerEnc == "" { // kept before replying: a retry (response lost in transit) gets the same key back
		list[i].AcceptedURL = their
		list[i].AcceptedKeyHash = Hash(req.Key)
		if list[i].AnswerEnc, err = s.box.Seal(NewKey()); err == nil {
			err = s.st.Settings().Set(ctx, invitesKey, list)
		}
	}
	inv = list[i]
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	key, err := s.box.Open(inv.AnswerEnc)
	if err != nil {
		return nil, err
	}
	p := Peer{ID: ids.New("peer"), Name: oneLine(req.Name, 80), URL: their, SelfURL: inv.URL, InKeyHash: Hash(key), Shared: inv.Shared, Remote: []RemoteProject{}, As: inv.As, CreatedAt: s.now().UTC()}
	if p.OutKeyEnc, err = s.box.Seal(req.Key); err != nil {
		return nil, err
	}
	if err := s.update(ctx, func(list []Peer) ([]Peer, error) { return upsert(list, &p), nil }); err != nil {
		return nil, err
	}
	return json.Marshal(pairReply{Key: key, Name: inv.Name, Projects: s.SharedProjects(ctx, p)})
}

// ErrDenied: a call with no valid key or invite.
var ErrDenied = errors.New("không được phép")

// Auth finds the office a key belongs to.
func (s *Service) Auth(ctx context.Context, key string) (Peer, bool) {
	if !strings.HasPrefix(key, "ofp_") {
		return Peer{}, false
	}
	h := Hash(key)
	list, err := s.List(ctx)
	if err != nil {
		return Peer{}, false
	}
	for _, p := range list {
		if subtle.ConstantTimeCompare([]byte(p.InKeyHash), []byte(h)) == 1 {
			if now := s.now().UTC(); p.LastUsedAt == nil || now.Sub(*p.LastUsedAt) > time.Minute {
				_ = s.update(ctx, func(list []Peer) ([]Peer, error) {
					for i := range list {
						if list[i].ID == p.ID {
							list[i].LastUsedAt = &now
						}
					}
					return list, nil
				})
			}
			return p, true
		}
	}
	return Peer{}, false
}

// Send hands a message to the paired office id.
func (s *Service) Send(ctx context.Context, id string, m Message) (Reply, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return Reply{}, errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	var out Reply
	err = s.call(ctx, p, http.MethodPost, "/api/peer/relay", m, &out)
	return out, err
}

// Search is a search_history in the other office: its shared project, in
// the chats between the two offices.
type Search struct {
	Query   string `json:"query"`
	Days    int    `json:"days,omitempty"`
	Author  string `json:"author,omitempty"`
	Project string `json:"project"`
}

// Hit is a message a Search found there.
type Hit struct {
	Chat      string    `json:"chat"`
	Message   string    `json:"message"`
	Project   string    `json:"project"`
	Title     string    `json:"title,omitempty"`
	Author    string    `json:"author,omitempty"`
	Snippet   string    `json:"snippet"`
	CreatedAt time.Time `json:"created_at"`
}

// SearchChats searches the paired office id's chats with this one.
func (s *Service) SearchChats(ctx context.Context, id string, q Search) ([]Hit, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	var out struct {
		Hits []Hit `json:"hits"`
	}
	err = s.call(ctx, p, http.MethodPost, "/api/peer/search", q, &out)
	return out.Hits, err
}

// ReadChat reads a chat between the two offices in the paired office id
// (around message, when given), as read_link shows it; project, when given,
// is the project there it must belong to (ADR-169).
//
// It returns the text and the chat's project there ("" from an office older
// than ADR-169).
func (s *Service) ReadChat(ctx context.Context, id, project, chat, message string) (string, string, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return "", "", errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	var out struct {
		Text    string `json:"text"`
		Project string `json:"project"`
	}
	q := url.Values{}
	if message != "" {
		q.Set("m", message)
	}
	if project != "" {
		q.Set("p", project)
	}
	path := "/api/peer/chats/" + url.PathEscape(chat)
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	err = s.call(ctx, p, http.MethodGet, path, nil, &out)
	return out.Text, out.Project, err
}

// ChatInfo is a chat of a shared project the other office lists (ADR-211).
type ChatInfo struct {
	Chat      string    `json:"chat"`
	Project   string    `json:"project"`
	Title     string    `json:"title,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListChats lists the chats of project in the paired office id that this
// office may read, the latest first, updated in the last days.
func (s *Service) ListChats(ctx context.Context, id, project string, days int) ([]ChatInfo, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	var out struct {
		Chats []ChatInfo `json:"chats"`
	}
	path := "/api/peer/projects/" + url.PathEscape(project) + "/chats?days=" + strconv.Itoa(days)
	if err := s.call(ctx, p, http.MethodGet, path, nil, &out); err != nil {
		return nil, older(err)
	}
	return out.Chats, nil
}

// RemoteTool is an office tool another office may call on a project shared
// with it (ADR-212).
type RemoteTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema,omitempty"`
	ReadOnly    bool           `json:"read_only,omitempty"`
}

// ToolResult is what a RemoteTool call answered.
type ToolResult struct {
	Text  string `json:"text"`
	Error bool   `json:"error,omitempty"`
}

// Tools lists the tools this office may call on project in office id, and
// whether it calls them as administrator there.
func (s *Service) Tools(ctx context.Context, id, project string) ([]RemoteTool, bool, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, false, errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	var out struct {
		Tools []RemoteTool `json:"tools"`
		Admin bool         `json:"admin"`
	}
	if err := s.call(ctx, p, http.MethodGet, "/api/peer/projects/"+url.PathEscape(project)+"/tools", nil, &out); err != nil {
		return nil, false, older(err)
	}
	return out.Tools, out.Admin, nil
}

// CallTool calls tool on project in office id with args (a JSON object).
func (s *Service) CallTool(ctx context.Context, id, project, tool string, args json.RawMessage) (ToolResult, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return ToolResult{}, errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	var out ToolResult
	err = s.call(ctx, p, http.MethodPost, "/api/peer/projects/"+url.PathEscape(project)+"/tools/"+url.PathEscape(tool), args, &out)
	return out, older(err)
}

// older: a 404 from the other office means it predates the call.
func older(err error) error {
	if err != nil && strings.Contains(err.Error(), "(404)") {
		return errors.New("office bên kia chưa có lệnh này: cần cập nhật office ở đó")
	}
	return err
}

// FetchPack asks office id for a pack (.zip, ADR-170) of its project, with
// include; at most max bytes.
func (s *Service) FetchPack(ctx context.Context, id, project string, include []string, max int64) ([]byte, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, errors.New("office liên kết không còn (đã gỡ ghép nối?)")
	}
	key, err := s.box.Open(p.OutKeyEnc)
	if err != nil {
		return nil, err
	}
	q := url.Values{"include": {strings.Join(include, ",")}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL+"/api/peer/projects/"+url.PathEscape(project)+"/pack?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không gọi được office bên kia: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, max+1))
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusNotFound && !strings.Contains(string(raw), "error") {
		return nil, errors.New("office bên kia chưa có xuất project (cần cập nhật office đó)")
	}
	if res.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return nil, fmt.Errorf("office bên kia từ chối (%d): %s", res.StatusCode, cmp.Or(e.Error, http.StatusText(res.StatusCode)))
	}
	if int64(len(raw)) > max {
		return nil, errors.New("gói project quá lớn")
	}
	return raw, nil
}

// LinkOf finds, among the projects in other offices projectID links to, the
// one a dashboard link of the other office points into.
func (s *Service) LinkOf(ctx context.Context, projectID, link string) (Link, bool) {
	for _, l := range s.LinksOf(ctx, projectID) {
		if strings.HasPrefix(link, l.Peer.URL+"/projects/"+l.Project.ID+"?") {
			return l, true
		}
	}
	return Link{}, false
}

// call sends a request to p with the key this office has for it.
func (s *Service) call(ctx context.Context, p Peer, method, path string, in, out any) error {
	key, err := s.box.Open(p.OutKeyEnc)
	if err != nil {
		return err
	}
	return s.do(ctx, method, p.URL+path, key, in, out)
}

func (s *Service) do(ctx context.Context, method, target, key string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("không gọi được office bên kia: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("office bên kia từ chối (%d): %s", res.StatusCode, cmp.Or(e.Error, http.StatusText(res.StatusCode)))
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return errors.New("office bên kia trả lời không đúng")
	}
	return nil
}

// CleanURL checks an office's address: https, or http only on this machine
// or a Tailscale network (100.64.0.0/10, *.ts.net). No query or fragment.
func CleanURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("địa chỉ office %q không hợp lệ", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !privateHost(u.Hostname()) {
			return "", fmt.Errorf("địa chỉ office %q phải dùng https (http chỉ cho máy này hoặc mạng Tailscale)", raw)
		}
	default:
		return "", fmt.Errorf("địa chỉ office %q phải là https://…", raw)
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/"), nil
}

var tailnet = netip.MustParsePrefix("100.64.0.0/10")

func privateHost(h string) bool {
	h = strings.ToLower(h)
	if h == "localhost" || strings.HasSuffix(h, ".ts.net") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && (ip.IsLoopback() || tailnet.Contains(ip.Unmap()))
}

// Resolve finds a linked project in another office among those projectID
// links to: by its ref ("peer:<id>/<project id>"), "<office>/<project>",
// or the project's name or id there.
func Resolve(ctx context.Context, st storage.Store, s *Service, projectID, ref string) (Peer, RemoteProject, error) {
	ref = strings.TrimSpace(ref)
	if s == nil {
		return Peer{}, RemoteProject{}, errors.New("không có office liên kết")
	}
	for _, l := range Linked(ctx, st, s, projectID) {
		if l.Ref == ref || strings.EqualFold(l.Label(), ref) || l.Project.ID == ref || strings.EqualFold(l.Project.Name, ref) {
			return l.Peer, l.Project, nil
		}
	}
	return Peer{}, RemoteProject{}, fmt.Errorf("%q không phải project liên kết", ref)
}

// Link is one linked project in another office.
type Link struct {
	Ref     string
	Peer    Peer
	Project RemoteProject
}

// Label is how agents and people name it: "<office>/<project>".
func (l Link) Label() string { return l.Peer.Name + "/" + l.Project.Name }

// Linked lists the projects in other offices projectID links to (those the
// other office still shares).
func Linked(ctx context.Context, st storage.Store, s *Service, projectID string) []Link {
	if s == nil {
		return nil
	}
	var out []Link
	for _, ref := range perm.LoadPolicy(ctx, st, projectID).LinkedProjects {
		id, pid, ok := SplitRef(ref)
		if !ok {
			continue
		}
		p, err := s.Get(ctx, id)
		if err != nil {
			continue
		}
		// the other office's assistant is its machine: never a project's link
		if i := slices.IndexFunc(p.Remote, func(r RemoteProject) bool { return r.ID == pid && !r.Assistant }); i >= 0 {
			out = append(out, Link{Ref: ref, Peer: p, Project: p.Remote[i]})
		}
	}
	return out
}

// LinksOf lists the projects in other offices projectID links to.
func (s *Service) LinksOf(ctx context.Context, projectID string) []Link {
	return Linked(ctx, s.st, s, projectID)
}

// SplitRef reads "peer:<peer id>/<project id>".
func SplitRef(ref string) (peerID, projectID string, ok bool) {
	rest, ok := strings.CutPrefix(ref, RefPrefix)
	if !ok {
		return "", "", false
	}
	peerID, projectID, ok = strings.Cut(rest, "/")
	return peerID, projectID, ok && peerID != "" && projectID != ""
}

// ChatLink is a chat's address in the office p.
func ChatLink(base, projectID, chatID string) string {
	return base + "/projects/" + url.PathEscape(projectID) + "?tab=chat&c=" + url.QueryEscape(chatID)
}

// Hash is how keys and secrets are kept.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// NewKey makes a key an office calls another with.
func NewKey() string { return "ofp_" + randomHex(24) }

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// PublicRemote is a git remote fit to show another office: a URL with no
// user, password or query, or scp-like user@host:path; "" otherwise.
func PublicRemote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 300 || strings.ContainsAny(s, " \t\n\r") {
		return ""
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" || !slices.Contains([]string{"https", "http", "ssh", "git"}, u.Scheme) {
			return ""
		}
		if u.Scheme == "ssh" && u.User != nil {
			u.User = url.User(u.User.Username()) // ssh://git@host: the user is no secret
		} else {
			u.User = nil
		}
		u.RawQuery, u.Fragment = "", ""
		return u.String()
	}
	// scp-like: [user@]host:path, never a local path
	host, path, ok := strings.Cut(s, ":")
	if !ok || path == "" || strings.HasPrefix(s, "/") || strings.ContainsAny(host, "/\\") || strings.Count(s, "@") > 1 || strings.ContainsAny(path, "@\\") {
		return ""
	}
	return s
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
