package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// Decider decides a proposal (kind patch | action) in the name of by, the
// person in the chat ("discord:an"); what it returns says how it went.
type Decider interface {
	Decide(ctx context.Context, kind, id string, approve bool, by string) (string, error)
}

// AlwaysDecider also approves a command and lets its agent run the like of
// it on its own from now on ("luôn cho phép").
type AlwaysDecider interface {
	DecideAlways(ctx context.Context, id, by string) (string, error)
}

// alwaysable: a command proposal that may be allowed for good.
func alwaysable(p proposal) bool {
	if p.Kind != "action" || p.Action != "run_command" {
		return false
	}
	_, ok := perm.SuggestPattern(p.Target)
	return ok
}

// SetDecider lets the people allowed to decide proposals from the chat (ADR-054).
func (m *Manager) SetDecider(d Decider) { m.decider = d }

// proposal is one thing an agent proposed in a chat's conversations, with the
// number the chat knows it by (numbers never shift until all are decided).
type proposal struct {
	N      int    `json:"n"`
	Kind   string `json:"kind"` // patch | action
	ID     string `json:"id"`
	Label  string `json:"label"`
	Action string `json:"action,omitempty"` // the action's kind (git_push, run_command…)
	Target string `json:"target,omitempty"`
	Conv   string `json:"conv,omitempty"` // the conversation it waits in: its agent goes on once decided
}

func pendingKey(channelID, chatID string) string {
	return "channel_pending/" + channelID + "/" + chatID
}

func (m *Manager) listing(ctx context.Context, channelID, chatID string) []proposal {
	var list []proposal
	_, _ = m.store.Settings().Get(ctx, pendingKey(channelID, chatID), &list)
	return list
}

// pendingLock gives the one mutex that serializes read-modify-write of a
// (channelID, chatID)'s pending-proposals list: two goroutines racing to
// read-then-overwrite it would otherwise lose whichever wrote first.
func (m *Manager) pendingLock(channelID, chatID string) *sync.Mutex {
	key := channelID + "/" + chatID
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingLocks == nil {
		m.pendingLocks = map[string]*sync.Mutex{}
	}
	lk := m.pendingLocks[key]
	if lk == nil {
		lk = &sync.Mutex{}
		m.pendingLocks[key] = lk
	}
	return lk
}

// withPending runs fn with the (channelID, chatID) pending-list lock held.
func (m *Manager) withPending(channelID, chatID string, fn func()) {
	lk := m.pendingLock(channelID, chatID)
	lk.Lock()
	defer lk.Unlock()
	fn()
}

// proposals are what waits for a person in a conversation.
func (m *Manager) proposals(ctx context.Context, conversationID string) []proposal {
	var out []proposal
	if ps, err := m.store.Chat().ListPatches(ctx, conversationID); err == nil {
		for _, p := range ps {
			if p.Status == "pending" {
				out = append(out, proposal{Kind: "patch", ID: p.ID, Label: "Sửa " + truncate(strings.Join(p.Files, ", "), 160)})
			}
		}
	}
	if as, err := m.store.Actions().List(ctx, conversationID, "", ""); err == nil {
		for _, a := range as {
			if a.Status == "pending" {
				out = append(out, proposal{Kind: "action", ID: a.ID, Label: firstNonEmpty(actions.Kinds[a.Kind], a.Kind) + ": " + truncate(a.Target, 160), Action: a.Kind, Target: a.Target})
			}
		}
	}
	return out
}

// still reports whether a proposal waits for a decision yet.
func (m *Manager) still(ctx context.Context, p proposal) bool {
	if p.Kind == "patch" {
		x, err := m.store.Chat().GetPatch(ctx, p.ID)
		return err == nil && x.Status == "pending"
	}
	x, err := m.store.Actions().Get(ctx, p.ID)
	return err == nil && x.Status == "pending"
}

// mustAsk: what is always asked, even when the chat approves directly —
// a push, stopping something, a setting, a command that deletes.
func mustAsk(p proposal) bool {
	switch p.Action {
	case "git_push", "stop_process", "stop_container", "config_change", "update_automation":
		return true
	case "run_command", "run_process":
		return perm.Risky(p.Target)
	}
	return false
}

// announce tells the chat what its answer left waiting, numbered for
// /approve and /reject (and buttons): an admin of the bot decides (ADR-081).
func (m *Manager) announce(ctx context.Context, ch storage.Channel, ad Adapter, chatID, conversationID string) {
	if m.decider == nil || conversationID == "" {
		return
	}
	var list []proposal
	asked := false
	m.withPending(ch.ID, chatID, func() {
		list = m.listing(ctx, ch.ID, chatID)
		known := map[string]bool{}
		next := 1
		for _, p := range list {
			known[p.ID] = true
			next = max(next, p.N+1)
		}
		for _, p := range m.proposals(ctx, conversationID) {
			if known[p.ID] {
				continue
			}
			p.N, next, p.Conv = next, next+1, conversationID
			list = append(list, p)
			asked = true
		}
		_ = m.store.Settings().Set(ctx, pendingKey(ch.ID, chatID), list)
	})
	if asked {
		m.sendPending(ctx, ch, ad, chatID, list)
	}
}

// sendPending lists what waits, with Approve / Reject buttons where the chat
// has them (and someone may decide there); the commands still work.
func (m *Manager) sendPending(ctx context.Context, ch storage.Channel, ad Adapter, chatID string, list []proposal) {
	text := m.pendingText(ctx, ch, list)
	bs, ok := ad.(ButtonSender)
	if !ok || len(ch.Approvers) == 0 {
		_, _ = ad.Send(ctx, chatID, text)
		return
	}
	rows := m.pendingRows(ctx, list)
	if len(rows) == 0 {
		_, _ = ad.Send(ctx, chatID, text)
		return
	}
	ids, err := bs.SendButtons(ctx, chatID, text, rows)
	if err != nil {
		_, _ = ad.Send(ctx, chatID, text) // the commands, then
		return
	}
	if len(ids) > 0 { // what a decision on the dashboard redraws
		_ = m.store.Settings().Set(ctx, buttonsKey(ch.ID, chatID), ids[len(ids)-1])
	}
}

func buttonsKey(channelID, chatID string) string {
	return "channel_buttons/" + channelID + "/" + chatID
}

// Redraw: a proposal of a bot chat's conversation was decided on the
// dashboard, so the chat's latest message with buttons keeps only those of
// what still waits (none left: no button to press again). Its agent is not
// run from here: DecidedOnDashboard does that, when the decision asks for it.
func (m *Manager) Redraw(ctx context.Context, conversationID string) {
	channelID, chatID := m.chatOf(ctx, actions.Scope{ConversationID: conversationID})
	if channelID == "" {
		return
	}
	var msgID string
	if ok, _ := m.store.Settings().Get(ctx, buttonsKey(channelID, chatID), &msgID); !ok || msgID == "" {
		return
	}
	ch, err := m.store.Channels().Get(ctx, channelID)
	if err != nil {
		return
	}
	m.mu.Lock()
	ad := m.adapters[channelID]
	m.mu.Unlock()
	be, ok := ad.(ButtonEditor)
	if !ok {
		return
	}
	var text string
	var rows [][]Button
	m.withPending(channelID, chatID, func() {
		list := m.listing(ctx, channelID, chatID)
		text, rows = m.pendingText(ctx, ch, list), m.pendingRows(ctx, list)
		if len(rows) == 0 {
			text = "Đã quyết trên dashboard."
			_ = m.store.Settings().Set(ctx, pendingKey(channelID, chatID), []proposal{}) // numbers start again
			_ = m.store.Settings().Set(ctx, buttonsKey(channelID, chatID), "")
		}
	})
	if err := be.EditButtons(ctx, chatID, msgID, text, rows); err != nil {
		slog.Warn("channels: buttons not redrawn", "channel", channelID, "err", err)
	}
}

// pendingRows are the Approve / Reject buttons of what still waits (none:
// nothing waits).
func (m *Manager) pendingRows(ctx context.Context, list []proposal) [][]Button {
	var rows [][]Button
	waiting := 0
	for _, p := range list {
		if !m.still(ctx, p) {
			continue
		}
		waiting++
		if len(rows) < 4 {
			n := strconv.Itoa(p.N)
			// by id: a number is given again once all is decided, an old button must not pick the new one
			row := []Button{{Label: "✅ Duyệt " + n, Data: "/approve " + p.ID}}
			if _, ok := m.decider.(AlwaysDecider); ok && alwaysable(p) {
				row = append(row, Button{Label: "♾️ Luôn cho phép " + n, Data: "/approve-always " + p.ID})
			}
			row = append(row, Button{Label: "❌ Từ chối " + n, Data: "/reject " + p.ID, Danger: true})
			// skipped: rejected, and the agent is not run again about it
			rows = append(rows, append(row, Button{Label: "⏭️ Bỏ qua " + n, Data: "/skip " + p.ID, Danger: true}))
		}
	}
	if waiting > 1 {
		rows = append(rows, []Button{{Label: "✅ Duyệt tất cả", Data: "/approve all"}})
	}
	return rows
}

// redrawButtons: a button was pressed and something decided, so the message
// it was on keeps only the buttons of what still waits; once nothing does,
// it shows what was decided, with no buttons left to press again.
func (m *Manager) redrawButtons(ctx context.Context, ch storage.Channel, ad Adapter, in Incoming, result string) {
	be, ok := ad.(ButtonEditor)
	if !ok || in.ButtonMsg == "" {
		return
	}
	list := m.listing(ctx, ch.ID, in.ChatID)
	text, rows := m.pendingText(ctx, ch, list), m.pendingRows(ctx, list)
	if len(rows) == 0 {
		text = result
	}
	if err := be.EditButtons(ctx, in.ChatID, in.ButtonMsg, text, rows); err != nil {
		slog.Warn("channels: buttons not redrawn", "channel", ch.ID, "err", err)
	}
}

// pendingText lists what still waits, by number.
func (m *Manager) pendingText(ctx context.Context, ch storage.Channel, list []proposal) string {
	var lines []string
	for _, p := range list {
		if m.still(ctx, p) {
			lines = append(lines, fmt.Sprintf("%d. %s", p.N, p.Label))
		}
	}
	if len(lines) == 0 {
		return "Không có gì chờ duyệt."
	}
	how := "Bấm nút bên dưới, hoặc gõ /approve 1 (/approve all) để duyệt, /reject 1 để từ chối, /skip 1 để bỏ qua (agent không chạy tiếp)."
	if len(ch.Approvers) == 0 {
		how = "Bot này chưa có ai được duyệt qua chat: duyệt trên dashboard (Tổng quan → Cần xử lý)."
	}
	return "Chờ duyệt:\n" + strings.Join(lines, "\n") + "\n" + how
}

func outcome(p proposal, approve bool, detail string, err error) string {
	switch {
	case err != nil:
		return fmt.Sprintf("⚠️ %d. %s: %s", p.N, p.Label, truncate(err.Error(), 300))
	case approve:
		return strings.TrimSpace(fmt.Sprintf("✅ Đã duyệt %s — %s", numbered(p), truncate(detail, 400)))
	}
	return "❌ Đã từ chối " + numbered(p)
}

func numbered(p proposal) string {
	if p.N == 0 {
		return p.Label
	}
	return fmt.Sprintf("%d. %s", p.N, p.Label)
}

// MayDecide: the user is in the bot's Admin list, named one by one ("*" is
// never an admin: anyone who may message the bot would get the machine).
func MayDecide(ch storage.Channel, userID string) bool {
	return userID != "" && slices.Contains(ch.Approvers, userID)
}

// approvals answers /pending, /approve and /reject from a chat (an admin of the bot decides).
func (m *Manager) approvals(ctx context.Context, ch storage.Channel, in Incoming, cmd, arg, who string) string {
	if m.decider == nil {
		return "Office này chưa bật duyệt qua chat."
	}
	list := m.listing(ctx, ch.ID, in.ChatID)
	if cmd == "pending" {
		return m.pendingText(ctx, ch, list)
	}
	if !MayDecide(ch, in.UserID) {
		return "Chỉ người trong danh sách Admin của bot được duyệt."
	}
	by := ch.Kind + ":" + who
	if cmd == "mode" { // the modes are gone (ADR-081): the lists say who runs how
		return "Bot không còn chế độ. Người trong danh sách Admin chạy theo quyền của agent; Người dùng thì đề xuất, chờ admin duyệt."
	}
	always, skip := cmd == "approve-always", cmd == "skip"
	approve := cmd == "approve" || always
	var picked []proposal
	all := arg == "" && len(list) == 1 || strings.EqualFold(strings.TrimSpace(arg), "all") || strings.EqualFold(strings.TrimSpace(arg), "tat-ca")
	if always {
		all = false // one by one: each adds a permission
	}
	stale := 0 // a button of a proposal no longer listed: decided before
	for _, f := range strings.FieldsFunc(arg, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(f)
		i := slices.IndexFunc(list, func(p proposal) bool { return err == nil && p.N == n || p.ID == f })
		switch {
		case i >= 0:
			picked = append(picked, list[i])
		case err != nil && strings.Contains(f, "_"):
			stale++
		}
	}
	if all {
		picked = list
	}
	if len(picked) == 0 && stale > 0 {
		return "Đề xuất này đã được quyết trước đó."
	}
	if len(picked) == 0 {
		return "Hãy ghi số đề xuất, ví dụ /approve 1 hoặc /approve all.\n" + m.pendingText(ctx, ch, list)
	}
	var lines []string
	decided := map[string][]string{} // by conversation
	for _, p := range picked {
		if !m.still(ctx, p) {
			if !all {
				lines = append(lines, fmt.Sprintf("%d. %s: đã được quyết trước đó.", p.N, p.Label))
			}
			continue
		}
		var (
			detail string
			err    error
		)
		if always {
			ad, ok := m.decider.(AlwaysDecider)
			if !ok || !alwaysable(p) {
				lines = append(lines, fmt.Sprintf("%d. %s: không luôn cho phép được, hãy /approve %d.", p.N, p.Label, p.N))
				continue
			}
			detail, err = ad.DecideAlways(ctx, p.ID, by)
		} else {
			detail, err = m.decider.Decide(ctx, p.Kind, p.ID, approve, by)
		}
		if errors.Is(err, actions.ErrDecided) { // decided meanwhile (on the dashboard): its agent goes on from there
			lines = append(lines, fmt.Sprintf("%d. %s: đã được quyết trước đó.", p.N, p.Label))
			continue
		}
		if skip {
			if err == nil {
				lines = append(lines, "⏭️ Đã bỏ qua "+numbered(p))
			} else {
				lines = append(lines, outcome(p, false, detail, err))
			}
			continue // the agent is not run again about it
		}
		lines = append(lines, outcome(p, approve, detail, err))
		if p.Conv != "" {
			decided[p.Conv] = append(decided[p.Conv], outcome(p, approve, detail, err))
		}
	}
	// the agent's answer is over: it goes on with what was decided (it would
	// otherwise stand still until someone wrote again)
	for conv, done := range decided {
		m.resume(ctx, ch, in, conv, done, MayDecide(ch, in.UserID))
	}
	// all decided: numbers start again (re-read fresh under lock: list was
	// captured before deciding, a concurrent announce() may have added since)
	m.withPending(ch.ID, in.ChatID, func() {
		fresh := m.listing(ctx, ch.ID, in.ChatID)
		if !slices.ContainsFunc(fresh, func(p proposal) bool { return m.still(ctx, p) }) {
			_ = m.store.Settings().Set(ctx, pendingKey(ch.ID, in.ChatID), []proposal{})
		}
	})
	if len(lines) == 0 {
		return "Không có gì chờ duyệt."
	}
	return strings.Join(lines, "\n")
}

// DirectApprover (for actions.SetAutoApprover): a proposal is approved at
// once, in the name of who turned it on, when either:
//   - a bot's chat is in direct/admin mode (ADR-054), or
//   - the run's own permission is already full access — the agent's own
//     (ADR-074: wherever it runs — chat, bot, automation), or an automation's
//     own override of it.
//
// Either way, what must always be asked stays asked (push, stop, settings,
// delete…), and so does creating/running an automation — an unattended run
// never grants itself more automations.
func (m *Manager) DirectApprover(ctx context.Context, a storage.Action) (string, bool) {
	if a.JobID == "" {
		return "", false
	}
	job, err := m.store.Jobs().Get(ctx, a.JobID)
	if err != nil {
		return "", false
	}
	// ADR-074 (security fix): the job already carries whether this run has
	// full (administrator) access and who enabled it, computed once when it
	// started (internal/trigger.Runner or internal/chat.Engine) — never
	// re-derived here from the agent or the automation. But a proposal can
	// stay pending a while (a job queued behind others, a human-in-the-loop
	// action waiting on an approval), so who enabled it is re-checked against
	// admin status right now, at decision time — an admin stripped of the
	// role after the job started must not have it auto-approve on their behalf.
	if job.FullAccess && job.FullAccessBy != "" && m.isAdminEmail(ctx, job.FullAccessBy) &&
		a.Kind != "create_automation" && a.Kind != "run_automation" && !mustAsk(proposal{Action: a.Kind, Target: a.Target}) {
		return job.FullAccessBy, true
	}
	return "", false
}

// isAdminEmail: that email is still an admin of the office (ADR-074: a
// FullAccess/override an admin turned on only holds while they still are
// one). Mirrors trigger.Runner.isAdminEmail (unexported there, so copied
// here rather than shared).
func (m *Manager) isAdminEmail(ctx context.Context, email string) bool {
	if email == "" {
		return false
	}
	u, err := m.store.Users().GetByEmail(ctx, email)
	return err == nil && u.Role == storage.RoleAdmin && !u.Disabled
}

// resume runs a conversation's agent again once a person decided what it
// proposed from the bot's chat (ADR-084): what was decided, and its results,
// as the message to go on from — in that same conversation, as its bot rule.
func (m *Manager) resume(ctx context.Context, ch storage.Channel, in Incoming, conv string, done []string, admin bool) {
	jobs, err := m.store.Jobs().List(ctx, storage.JobFilter{ConversationID: conv, Limit: 20})
	if err != nil {
		return
	}
	var rule storage.Automation
	for _, j := range jobs { // the bot's rule that answered there
		if j.Origin == "automation" && trigger.IsChannel(j.Trigger) {
			if a, err := m.store.Automations().Get(ctx, j.OriginID); err == nil && a.Enabled && a.Action == "chat" && a.Config.ChannelID == ch.ID {
				rule = a
				break
			}
		}
	}
	if rule.ID == "" {
		return
	}
	who := firstNonEmpty(in.UserName, in.UserID)
	text := "[office] " + who + " đã quyết các đề xuất của bạn:\n" + strings.Join(done, "\n") +
		"\n\nLàm tiếp việc đang dở theo kết quả trên (không đề xuất lại những gì đã duyệt); xong thì báo ngắn gọn."
	p := trigger.ChannelPayload{Message: text, User: who, UserID: in.UserID, ChatID: in.ChatID, ChannelID: ch.ID, ConversationID: conv, Admin: admin}
	raw, _ := json.Marshal(p)
	actx := actor.With(ctx, ch.Kind+":"+who)
	m.mu.Lock() // registered before the runner can answer
	job, status, err := m.runner.Enqueue(actx, rule, ch.Kind, string(raw), "", "")
	ad := m.adapters[ch.ID]
	if err == nil && status == "queued" && ad != nil && m.root != nil { // the chat sees it at work, as for a message
		tctx, stop := context.WithCancel(m.root)
		m.waiting[job.ID] = waiter{typing: stop, chat: in.ChatID, started: time.Now()}
		go typing(tctx, ad, in.ChatID)
	}
	m.mu.Unlock()
	if err != nil {
		slog.Error("channels: resume after approval", "channel", ch.ID, "err", err)
		return
	}
	if m.root != nil {
		m.runner.StartReady(m.root, time.Now().UTC())
	}
}

// DecidedOnDashboard goes on with a bot chat's conversation after an office
// admin decided its proposals on the dashboard (ADR-084): its agent answers
// in that chat (the thread, the channel), as the bot's admins' messages run.
func (m *Manager) DecidedOnDashboard(ctx context.Context, conversationID, who string, done []string) {
	channelID, chatID := m.chatOf(ctx, actions.Scope{ConversationID: conversationID})
	if channelID == "" {
		return
	}
	ch, err := m.store.Channels().Get(ctx, channelID)
	if err != nil || !ch.Enabled {
		return
	}
	m.resume(ctx, ch, Incoming{ChatID: chatID, UserName: who}, conversationID, done, true)
}
