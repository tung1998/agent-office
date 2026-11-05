package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/peer"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/prompts"
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
	Project        string `json:"relayed_from_project,omitempty"` // another project's chat: its name
	// Hops: relays so far in a chain between linked projects (hand-off,
	// report back, answer…), for information: no cap
	Hops      int    `json:"relay_hops,omitempty"`
	Report    bool   `json:"relay_report,omitempty"`            // the linked chat's agent reporting back
	ProjectID string `json:"relayed_from_project_id,omitempty"` // its project's id (a link to its chat)
	// Name: the sending chat's project, whichever (Project is only set for
	// another project's: it marks a chain between linked projects)
	Name string `json:"relayed_from_project_name,omitempty"`
	// Peer: the message came from another office (ADR-167): its id here and
	// its address (ConversationID and ProjectID are that office's)
	Peer    string `json:"relayed_from_peer,omitempty"`
	PeerURL string `json:"relayed_from_peer_url,omitempty"`
}

// Relay says where a message an agent sent into this chat came from
// (send_to_chat, a linked project's report back): the agent, its project and
// its chat, shown with the message.
type Relay struct {
	Agent          string `json:"agent"`
	Project        string `json:"project"`
	ProjectID      string `json:"project_id"`
	ConversationID string `json:"conversation_id"`
	Title          string `json:"title"`
	Report         bool   `json:"report"`        // a linked project's report back: its text shown folded
	URL            string `json:"url,omitempty"` // its chat in another office (ADR-167)
}

// RelayOf is where m was relayed from, or nil when a person wrote it.
func RelayOf(m storage.Message) *Relay {
	var info relayInfo
	if m.Role != "user" || m.Context == "" || json.Unmarshal([]byte(m.Context), &info) != nil || info.ConversationID == "" {
		return nil
	}
	r := &Relay{Agent: info.Agent, Project: cmp.Or(info.Name, info.Project), ProjectID: info.ProjectID,
		ConversationID: info.ConversationID, Title: info.Title, Report: info.Report}
	if info.PeerURL != "" { // another office's chat: a link there, none here
		r.URL, r.ProjectID = peer.ChatLink(info.PeerURL, info.ProjectID, info.ConversationID), ""
	}
	return r
}

// fillRelay completes what an older relayed message did not keep (its
// project), from the chat it came from.
func (e *Engine) fillRelay(ctx context.Context, r *Relay, names map[string]string) {
	if r == nil || r.URL != "" || (r.ProjectID != "" && r.Project != "") {
		return
	}
	c, err := e.store.Chat().GetConversation(ctx, r.ConversationID)
	if err != nil {
		return
	}
	r.ProjectID, r.Title = c.ProjectID, cmp.Or(r.Title, c.Title)
	if _, ok := names[c.ProjectID]; !ok {
		if p, err := e.store.Repos().Get(ctx, c.ProjectID); err == nil {
			names[c.ProjectID] = p.Name
		}
	}
	r.Project = cmp.Or(r.Project, names[c.ProjectID])
}

// relayOrigin is the relayed message a chat's current work answers: the last
// message a person sent, past office's own "[office] … decided" ones; who is
// the person it speaks for.
func relayOrigin(msgs []storage.Message) (info relayInfo, who string, ok bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "user" || strings.HasPrefix(m.Content, "[office] ") {
			continue
		}
		if json.Unmarshal([]byte(m.Context), &info) != nil || info.ConversationID == "" {
			return relayInfo{}, m.Author, false
		}
		return info, m.Author, true
	}
	return relayInfo{}, "", false
}

// peerOrigin is the last hand-off another office sent into the chat (not a
// report back) and the person it speaks for.
func peerOrigin(msgs []storage.Message) (relayInfo, string, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		var info relayInfo
		if m := msgs[i]; m.Role == "user" && json.Unmarshal([]byte(m.Context), &info) == nil && info.Peer != "" && !info.Report {
			return info, m.Author, true
		}
	}
	return relayInfo{}, "", false
}

// Relay sends an approved send_message action: a.Args.Message to the chat
// a.TargetID, as the person whose message started the run (a.JobID). Only a
// person's run can speak for them, and only in chats they may open.
// It returns what happened and the chat it went to (a new one's id).
func (e *Engine) Relay(ctx context.Context, a storage.Action) (string, string, error) {
	job, err := e.store.Jobs().Get(ctx, a.JobID)
	if err != nil {
		return "", "", errors.New("không rõ lượt chat gửi tin này")
	}
	as, ok := storage.SpeaksFor(ctx, e.store, job)
	if !ok {
		return "", "", errors.New("chỉ gửi thay được khi lượt chat do một người gửi, hoặc tự động hóa theo lịch có người chịu trách nhiệm (không phải bot hay webhook)")
	}
	if a.Args.Peer != "" {
		return e.relayPeer(ctx, a)
	}
	var target storage.Conversation
	if a.TargetID == "" && a.Args.Project != "" { // a new chat in a linked project, opened by the person
		if e.isAssistant(ctx, a.Args.Project) {
			return "", "", errors.New("không mở chat ở trợ lý office được")
		}
		c, err := e.StartConversationFor(actor.With(ctx, as), a.Args.Project, "")
		if err != nil {
			return "", "", err
		}
		// the work goes on as the person let it run where it came from: a
		// unit's chat its board opens runs at that chat's mode (ADR-207)
		if src, err := e.store.Chat().GetConversation(ctx, a.ConversationID); err == nil && perm.Valid(src.Mode) {
			c.Mode = src.Mode
		}
		if target, err = e.store.Chat().CreateConversation(ctx, c); err != nil {
			return "", "", err
		}
	} else if target, err = e.store.Chat().GetConversation(ctx, a.TargetID); err != nil {
		return "", "", errors.New("không tìm thấy cuộc chat đích")
	}
	if target.ProjectID != a.ProjectID && target.ProjectID != a.Args.Project {
		return "", "", errors.New("cuộc chat đích không thuộc project này hay project liên kết")
	}
	if target.ID == a.ConversationID {
		return "", "", errors.New("đây là chính cuộc chat đang nói, không cần gửi sang")
	}
	if e.isAssistant(ctx, target.ProjectID) && target.CreatedBy != as {
		return "", "", errors.New("cuộc chat trợ lý office của người khác: không gửi được")
	}
	from := relayInfo{ConversationID: a.ConversationID, Agent: a.ProposedBy, ProjectID: a.ProjectID}
	if p, err := e.store.Repos().Get(ctx, a.ProjectID); err == nil {
		from.Name = p.Name
	}
	if target.ProjectID != a.ProjectID { // a chain between linked projects: counted
		from.Hops = 1
		if msgs, err := e.store.Chat().ListMessages(ctx, a.ConversationID); err == nil {
			if o, _, ok := relayOrigin(msgs); ok {
				from.Hops = o.Hops + 1
			}
		}
		from.Project = from.Name
	}
	if c, err := e.store.Chat().GetConversation(ctx, a.ConversationID); err == nil {
		from.Title = c.Title
	}
	raw, _ := json.Marshal(from)
	_, msg, err := e.SendWithContext(actor.With(context.Background(), as), target.ID, a.Args.Message, string(raw), nil)
	link := "/projects/" + target.ProjectID + "?tab=chat&c=" + target.ID
	var off *OffError
	if errors.As(err, &off) && msg.ID != "" {
		return "đã gửi (agent của chat đó đang tạm nghỉ, chưa trả lời): " + link, target.ID, nil
	}
	if err != nil {
		return "", "", err
	}
	return "đã gửi vào \"" + cmp.Or(target.Title, "chat mới") + "\": " + link, target.ID, nil
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

// reportBack: an agent answered in a chat a linked project's agent opened or
// sent to (send_to_chat): its answer goes back to that chat in the person's
// name, and the agent there goes on from it (answers what it can, asks the
// person the rest). No cap between this office's projects: the agents stop
// when they have nothing to add.
func (e *Engine) reportBack(conv storage.Conversation, project storage.Repo, agent storage.Agent, reply string) {
	if strings.TrimSpace(reply) == "" {
		return
	}
	ctx := context.Background()
	msgs, err := e.store.Chat().ListMessages(ctx, conv.ID)
	if err != nil {
		return
	}
	origin, as, ok := relayOrigin(msgs)
	if !ok && as != "" { // a person here went on (a workflow, a follow-up): another office's hand-off still hears back
		origin, as, ok = peerOrigin(msgs)
	}
	// only a linked project's hand-off: never a report itself (the agent that
	// asked answers on with send_to_chat when it has to)
	if !ok || origin.Project == "" || origin.Report || !strings.HasPrefix(as, "human:") {
		return
	}
	if origin.Peer != "" {
		e.reportPeer(conv, project, agent, reply, origin)
		return
	}
	src, err := e.store.Chat().GetConversation(ctx, origin.ConversationID)
	if err != nil || src.Cleaned != "" {
		return
	}
	info := relayInfo{ConversationID: conv.ID, Title: conv.Title, Agent: agent.Name, Project: project.Name, Name: project.Name, ProjectID: project.ID, Hops: origin.Hops + 1, Report: true}
	raw, _ := json.Marshal(info)
	text := prompts.Render("chat/linked-report", map[string]any{"Project": project.Name, "Agent": agent.Name,
		"Link": "/projects/" + conv.ProjectID + "?tab=chat&c=" + conv.ID, "Reply": truncate(reply, 8000)})
	e.deliver(actor.With(context.Background(), as), src.ID, text, string(raw), func(err error) { e.note(conv, "Không báo lại được chat gốc: "+err.Error()) })
}

// deliver sends text into a chat as the person as, in the background: a
// chat answering now gets it after its answer (up to 10 minutes); failed
// tells what went wrong (a chat whose agent is off just keeps it).
func (e *Engine) deliver(ctx context.Context, conversationID, text, context_ string, failed func(error)) {
	safeGo("relay deliver", func() {
		for try := 0; try < 120; try++ {
			_, _, err := e.SendWithContext(ctx, conversationID, text, context_, nil)
			var off *OffError
			if errors.As(err, &off) || (!errors.Is(err, ErrBusy) && !errors.Is(err, ErrAgentBusy)) {
				if err != nil && !errors.As(err, &off) && failed != nil {
					failed(err)
				}
				return
			}
			time.Sleep(e.reportWait())
		}
	})
}

func (e *Engine) reportWait() time.Duration {
	if e.reportEvery > 0 {
		return e.reportEvery
	}
	return 5 * time.Second
}

// linkedBrief tells an agent the projects its own links to (send_to_chat
// with project opens a chat there).
func (e *Engine) linkedBrief(ctx context.Context, projectID, conversationID string) string {
	type item struct{ Name, Description string }
	var items []item
	for _, id := range perm.LoadPolicy(ctx, e.store, projectID).LinkedProjects {
		if p, err := e.store.Repos().Get(ctx, id); err == nil && !e.isAssistant(ctx, p.ID) {
			items = append(items, item{p.Name, truncate(oneLine(p.Description), 200)})
		}
	}
	var chats []linkedOpen // the work already handed over from this chat: carried on there
	for _, c := range e.linkedChats(ctx, conversationID) {
		chats = append(chats, linkedOpen{c.project, cmp.Or(c.conv.Title, "(chưa có tiêu đề)"), "/projects/" + c.conv.ProjectID + "?tab=chat&c=" + c.conv.ID}) // i18n-ignore
	}
	// other offices: only through the MCP other_office (ADR-169)
	others := e.peerChats(ctx, conversationID)
	other := e.peers != nil && len(e.peers.LinksOf(ctx, projectID)) > 0
	if len(items) == 0 && len(chats) == 0 && !other && len(others) == 0 {
		return ""
	}
	return "\n" + prompts.Render("chat/linked", map[string]any{"Projects": items, "Chats": chats, "Other": other, "OtherChats": others}) + "\n"
}

// linkedOpen is a chat of a linked project this chat handed work to.
type linkedOpen struct{ Project, Title, Link string }

type linkedChat struct {
	conv    storage.Conversation
	project string
}

// linkedChats are the chats of other projects this chat handed work to: the
// ones send_to_chat went to, and those that reported back here (a chat
// opened before its target was kept on the action is found that way).
func (e *Engine) linkedChats(ctx context.Context, conversationID string) []linkedChat {
	var ids []string
	if sent, err := e.store.Actions().List(ctx, conversationID, "", ""); err == nil {
		for _, a := range sent {
			if a.Kind == "send_message" && a.Status == "done" && a.Args.Project != "" && a.Args.Peer == "" && a.TargetID != "" {
				ids = append(ids, a.TargetID)
			}
		}
	}
	if msgs, err := e.store.Chat().ListMessages(ctx, conversationID); err == nil {
		for _, m := range msgs {
			var info relayInfo
			if m.Role == "user" && json.Unmarshal([]byte(m.Context), &info) == nil && info.Report && info.Project != "" {
				ids = append(ids, info.ConversationID)
			}
		}
	}
	var out []linkedChat
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		c, err := e.store.Chat().GetConversation(ctx, id)
		if err != nil || c.Cleaned != "" {
			continue
		}
		lc := linkedChat{conv: c}
		if p, err := e.store.Repos().Get(ctx, c.ProjectID); err == nil {
			lc.project = p.Name
		}
		out = append(out, lc)
	}
	return out
}

// LinkedPending is what waits for a person in a linked project's chat this
// chat handed work to (send_to_chat): shown here, so the person approves it
// without opening that chat.
type LinkedPending struct {
	ProjectID      string      `json:"project_id"`
	Project        string      `json:"project"`
	ConversationID string      `json:"conversation_id"`
	Title          string      `json:"title"`
	Actions        []ActionDTO `json:"actions"`
	Patches        []PatchDTO  `json:"patches"`
}

// LinkedPending lists, per linked chat this chat sent to, its pending
// proposals and diffs (only chats with some).
func (e *Engine) LinkedPending(ctx context.Context, conversationID string) []LinkedPending {
	out := []LinkedPending{}
	for _, lc := range e.linkedChats(ctx, conversationID) {
		c := lc.conv
		lp := LinkedPending{ProjectID: c.ProjectID, Project: lc.project, ConversationID: c.ID, Title: c.Title, Actions: []ActionDTO{}, Patches: []PatchDTO{}}
		if acts, err := e.store.Actions().List(ctx, c.ID, "", ""); err == nil {
			for _, x := range acts {
				if x.Status == "pending" {
					lp.Actions = append(lp.Actions, ToActionDTO(x))
				}
			}
		}
		if patches, err := e.store.Chat().ListPatches(ctx, c.ID); err == nil {
			for _, p := range patches {
				if p.Status == "pending" {
					lp.Patches = append(lp.Patches, e.patchDTO(ctx, p))
				}
			}
		}
		if len(lp.Actions)+len(lp.Patches) > 0 {
			e.numberPending(ctx, c.ID, lp.Actions, lp.Patches)
			out = append(out, lp)
		}
	}
	return out
}
