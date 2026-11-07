package support

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/collections"
	"github.com/tung1998/agent-office/internal/storage"
)

// Runner queues an automation's run (trigger.Runner).
type Runner interface {
	Enqueue(ctx context.Context, a storage.Automation, trigger, payload, dedupe, debounce string) (storage.Job, string, error)
	StartReady(ctx context.Context, now time.Time)
}

// SetRunner sets what runs the sources' automations.
func (s *Service) SetRunner(r Runner) { s.runner = r }

// Inbound is one customer message, whatever the source.
type Inbound struct {
	ExternalID string         `json:"external_id"` // the sender's id of the message: sent again, it is one message
	Thread     string         `json:"thread"`      // the sender's conversation: its messages go on one ticket
	Customer   Contact        `json:"customer"`
	Subject    string         `json:"subject"`
	Text       string         `json:"text"`
	Meta       map[string]any `json:"meta"`
}

// Contact is who wrote.
type Contact struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
	Ref   string `json:"ref"` // the customer's id in the sender's system
}

// Reply is what goes back to the customer.
type Reply struct {
	EventID string `json:"event_id"`
	Text    string `json:"text"`
}

// Result is what became of a message.
type Result struct {
	TicketID string `json:"ticket_id"`
	EventID  string `json:"event_id"`
	JobID    string `json:"job_id,omitempty"`
	// queued: being answered; answered: Reply is the answer; waiting_approval:
	// a person approves the answer first; failed: no answer (a person takes it)
	Status string `json:"status"`
	Reply  *Reply `json:"reply,omitempty"`
}

// Limits of a message.
const (
	maxText     = 16 << 10
	historyLen  = 20
	maxWait     = 120 * time.Second
	statusQueue = "queued"
)

// Ticket statuses.
const (
	StatusNew             = "new"
	StatusOpen            = "open"
	StatusWaitingCustomer = "waiting_customer"
	StatusWaitingApproval = "waiting_approval"
	StatusResolved        = "resolved"
	StatusClosed          = "closed"
)

// Draft states.
const (
	DraftPending    = "pending"
	DraftApproved   = "approved"
	DraftRejected   = "rejected"
	DraftSuperseded = "superseded"
	DraftFailed     = "failed"
)

var errInvalid = errors.New("support: invalid")

// InvalidError is a message or setting that cannot be taken as it is.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }
func (e *InvalidError) Unwrap() error { return errInvalid }

func invalid(format string, a ...any) error { return &InvalidError{fmt.Sprintf(format, a...)} }

// NewToken makes a source's token and the hash stored in place of it.
func NewToken() (token, hash string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(t)))
	return hex.EncodeToString(sum[:])
}

// TokenOK reports token is the source's.
func TokenOK(src storage.SupportSource, token string) bool {
	return token != "" && src.TokenHash != "" && subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(src.TokenHash)) == 1
}

// limiter: messages a source takes a minute (before any is stored).
type limiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func (l *limiter) allow(id string, perMin int, now time.Time) bool {
	if perMin <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string][]time.Time{}
	}
	keep := l.seen[id][:0]
	for _, t := range l.seen[id] {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	if len(keep) >= perMin {
		l.seen[id] = keep
		return false
	}
	l.seen[id] = append(keep, now)
	return true
}

// ErrRateLimited: the source sent more than its messages a minute.
var ErrRateLimited = errors.New("support: rate limited")

// Receive takes a customer message from src: its ticket (the open one of its
// thread, or a new one), its line in the history, and the source's
// automation queued to answer it. A message sent again (same external id)
// is not taken twice: its result is given back.
func (s *Service) Receive(ctx context.Context, src storage.SupportSource, in Inbound) (Result, error) {
	in.Text = strings.TrimSpace(in.Text)
	switch {
	case !src.Enabled:
		return Result{}, invalid("nguồn đang tắt")
	case in.Text == "":
		return Result{}, invalid("thiếu nội dung tin (text)")
	case len(in.Text) > maxText:
		return Result{}, invalid("tin quá dài (tối đa %d KB)", maxText>>10)
	}
	if in.ExternalID != "" {
		if e, err := s.store.Support().EventByExternal(ctx, src.ID, in.ExternalID); err == nil {
			return s.result(ctx, e), nil
		}
	}
	if !s.limit.allow(src.ID, src.RatePerMin, time.Now()) {
		return Result{}, ErrRateLimited
	}
	a, err := s.answerer(ctx, src)
	if err != nil {
		return Result{}, err
	}
	customerID := s.customer(ctx, src.ProjectID, in.Customer)
	st := s.LoadSettings(ctx, src.ProjectID)
	var (
		t       storage.SupportTicket
		msg     storage.SupportEvent
		created bool
	)
	err = s.store.InTx(ctx, func(tx storage.Store) error {
		repo := tx.Support()
		var err error
		if in.Thread != "" {
			t, err = repo.OpenTicket(ctx, src.ID, in.Thread)
		}
		if in.Thread == "" || errors.Is(err, storage.ErrNotFound) {
			subject := firstNonEmpty(strings.TrimSpace(in.Subject), cut(in.Text, 80))
			t = storage.SupportTicket{ProjectID: src.ProjectID, SourceID: src.ID, ExternalThread: in.Thread,
				CustomerID: customerID, Subject: subject, Status: StatusNew, CaseType: classify(st, in.Text)}
			if m := st.sla(t.CaseType); m > 0 {
				due := time.Now().UTC().Add(time.Duration(m) * time.Minute)
				t.SLADueAt = &due
			}
			if t, err = repo.CreateTicket(ctx, t); err != nil {
				return err
			}
			created = true
		} else if err != nil {
			return err
		} else {
			if t.Status == StatusWaitingCustomer || t.Status == StatusWaitingApproval {
				t.Status = StatusOpen
			}
			if t.CustomerID == "" {
				t.CustomerID = customerID
			}
			if t.CaseType == "" {
				t.CaseType = classify(st, in.Text)
			}
			if err := repo.UpdateTicket(ctx, t); err != nil {
				return err
			}
			if err := supersede(ctx, repo, t.ID); err != nil { // the customer said more: an answer waiting is out of date
				return err
			}
		}
		meta := map[string]any{}
		for k, v := range in.Meta {
			meta[k] = v
		}
		if c := in.Customer; c != (Contact{}) {
			meta["customer"] = c
		}
		msg, err = repo.AddEvent(ctx, storage.SupportEvent{TicketID: t.ID, SourceID: src.ID, Kind: "customer_msg", Body: in.Text,
			Actor: "customer:" + firstNonEmpty(in.Customer.Name, in.Customer.Email, in.Customer.Phone, in.Customer.Ref), ExternalID: in.ExternalID, Meta: meta})
		return err
	})
	if errors.Is(err, storage.ErrConflict) && in.ExternalID != "" { // the same message, at the same time
		if e, err := s.store.Support().EventByExternal(ctx, src.ID, in.ExternalID); err == nil {
			return s.result(ctx, e), nil
		}
	}
	if err != nil {
		return Result{}, err
	}
	s.notify(t.ID)
	if created {
		s.fire(ctx, t, "ticket.created", nil)
	}
	s.fire(ctx, t, "ticket.message", map[string]any{"message": map[string]any{"id": msg.ID, "text": msg.Body}})
	// a customer asking for a person, or a case people answer: no AI answer
	if contains(t.Tags, "human") || has(in.Text, st.HumanWords) || st.mode(t, src) == "human" {
		s.escalate(ctx, t, msg, "human")
		return s.result(ctx, msg), nil
	}
	payload, _ := json.Marshal(map[string]any{"support": s.payload(ctx, src, t, msg, st)})
	j, _, err := s.runner.Enqueue(ctx, a, "webhook", string(payload), "support:"+msg.ID, "") // an outside sender: never trusted
	if err != nil {
		s.fail(ctx, t, msg, "chưa xếp được việc trả lời: "+err.Error())
		return s.result(ctx, msg), nil
	}
	msg.JobID = j.ID
	if err := s.store.Support().UpdateEvent(ctx, msg); err != nil {
		return Result{}, err
	}
	s.runner.StartReady(context.WithoutCancel(ctx), time.Now().UTC())
	return Result{TicketID: t.ID, EventID: msg.ID, JobID: j.ID, Status: statusQueue}, nil
}

// escalate hands a ticket to a person: no AI answer on it from now on.
func (s *Service) escalate(ctx context.Context, t storage.SupportTicket, msg storage.SupportEvent, reason string) {
	repo := s.store.Support()
	t, _ = patchTicket(ctx, repo, t.ID, func(x *storage.SupportTicket) {
		if !contains(x.Tags, "human") {
			x.Tags = append(x.Tags, "human")
		}
		x.Status = StatusOpen
	})
	_, _ = repo.AddEvent(ctx, storage.SupportEvent{TicketID: t.ID, SourceID: t.SourceID, Kind: "status", Body: "escalated", Actor: "office",
		ReplyTo: msg.ID, State: "human", Meta: map[string]any{"reason": reason}})
	s.fire(ctx, t, "ticket.escalated", map[string]any{"reason": reason, "message": map[string]any{"id": msg.ID, "text": msg.Body}})
	s.notify(t.ID)
}

// answerer: the source's automation, if it can answer.
func (s *Service) answerer(ctx context.Context, src storage.SupportSource) (storage.Automation, error) {
	if s.runner == nil {
		return storage.Automation{}, errors.New("support: no runner")
	}
	if src.AutomationID == "" {
		return storage.Automation{}, invalid("nguồn chưa chọn tự động hóa trả lời")
	}
	a, err := s.store.Automations().Get(ctx, src.AutomationID)
	if err != nil || a.ProjectID != src.ProjectID {
		return storage.Automation{}, invalid("tự động hóa trả lời của nguồn không còn")
	}
	if a.Action != "chat" && a.Action != "workflow" {
		return storage.Automation{}, invalid("tự động hóa trả lời phải là chat hoặc quy trình")
	}
	return a, nil
}

// patchTicket reloads the ticket, applies mutate, and saves it — retrying
// (storage.ErrStale means another update landed between someone else's read
// and this one) so two concurrent paths changing different fields of the
// same ticket (an agent's support_set, an inbound customer message, a reply
// delivery outcome…) don't silently revert each other instead of clobbering.
func patchTicket(ctx context.Context, repo storage.SupportRepo, id string, mutate func(*storage.SupportTicket)) (storage.SupportTicket, error) {
	t, err := repo.Ticket(ctx, id)
	if err != nil {
		return t, err
	}
	for attempt := 0; ; attempt++ {
		mutate(&t)
		err = repo.UpdateTicket(ctx, t)
		if !errors.Is(err, storage.ErrStale) || attempt >= 5 {
			return t, err
		}
		if t, err = repo.Ticket(ctx, id); err != nil {
			return t, err
		}
	}
}

// supersede: the drafts of a ticket still waiting are out of date.
func supersede(ctx context.Context, repo storage.SupportRepo, ticketID string) error {
	evs, err := repo.Events(ctx, ticketID)
	if err != nil {
		return err
	}
	for _, e := range evs {
		if e.Kind == "ai_draft" && e.State == DraftPending {
			e.State = DraftSuperseded
			if err := repo.UpdateEvent(ctx, e); err != nil {
				return err
			}
		}
	}
	return nil
}

// customer finds the customer record of c (by email, ref, phone) or makes
// it ("" when c names no one). A known customer keeps its name unless c has one.
func (s *Service) customer(ctx context.Context, projectID string, c Contact) string {
	data := map[string]any{}
	for f, v := range map[string]string{"email": strings.ToLower(strings.TrimSpace(c.Email)), "phone": strings.TrimSpace(c.Phone), "external_ref": strings.TrimSpace(c.Ref)} {
		if v != "" {
			data[f] = v
		}
	}
	name := strings.TrimSpace(c.Name)
	if name == "" && len(data) == 0 {
		return ""
	}
	id := ""
	for _, f := range []string{"email", "external_ref", "phone"} {
		if v, ok := data[f]; ok {
			list, _, err := s.collections.Query(ctx, projectID, "customers", collections.Query{Where: []storage.Cond{{Field: f, Value: v}}, Limit: 1})
			if err == nil && len(list) == 1 {
				id = list[0].ID
				break
			}
		}
	}
	if name != "" {
		data["name"] = name
	} else if id == "" {
		data["name"] = firstNonEmpty(c.Email, c.Phone, c.Ref)
	}
	x, _, err := s.collections.Upsert(ctx, projectID, "customers", id, data, "support")
	if err != nil {
		return ""
	}
	return x.ID
}

// payload is what the automation reads: the ticket, who, the message, the
// ticket's history before it.
func (s *Service) payload(ctx context.Context, src storage.SupportSource, t storage.SupportTicket, msg storage.SupportEvent, st Settings) map[string]any {
	var history []map[string]any
	if evs, err := s.store.Support().Events(ctx, t.ID); err == nil {
		for _, e := range evs {
			if e.ID == msg.ID || (e.Kind != "customer_msg" && e.Kind != "sent" && e.Kind != "note") {
				continue
			}
			history = append(history, map[string]any{"kind": e.Kind, "by": e.Actor, "text": e.Body, "at": e.CreatedAt})
		}
	}
	if len(history) > historyLen {
		history = history[len(history)-historyLen:]
	}
	out := map[string]any{"ticket_id": t.ID, "subject": t.Subject, "source": src.Name, "channel": src.Kind, "message": msg.Body,
		"history": history, "meta": msg.Meta, "case_type": t.CaseType,
		"tools": "support_kb_search finds the playbooks; support_set sets the case type; your final answer is the reply to the customer"}
	if len(st.Cases) > 0 {
		var cases []map[string]any
		for _, c := range st.Cases {
			cases = append(cases, map[string]any{"key": c.Key, "name": c.Name, "when": c.Description})
		}
		out["case_types"] = cases
	}
	if t.CustomerID != "" {
		if x, _, err := s.collections.Get(ctx, src.ProjectID, "customers", t.CustomerID); err == nil {
			out["customer"] = map[string]any{"id": x.ID, "data": x.Data}
		}
	}
	return out
}

// Done hears a job end (trigger.OnDone): when it answered a customer
// message, the answer goes out (auto) or waits for a person (approve).
func (s *Service) Done(ctx context.Context, j storage.Job, answer string, runErr error) {
	repo := s.store.Support()
	msg, err := repo.EventByJob(ctx, j.ID)
	if err != nil || msg.Kind != "customer_msg" {
		return // not ours
	}
	t, err := repo.Ticket(ctx, msg.TicketID)
	if err != nil {
		return
	}
	src, err := repo.Source(ctx, msg.SourceID)
	if err != nil {
		return
	}
	answer = strings.TrimSpace(answer)
	if runErr != nil || answer == "" {
		s.fail(ctx, t, msg, firstNonEmpty(errText(runErr), j.Error, "không có câu trả lời"))
		return
	}
	draft := storage.SupportEvent{TicketID: t.ID, SourceID: src.ID, Kind: "ai_draft", Body: answer, Actor: "agent", JobID: j.ID,
		ConversationID: j.ConversationID, ReplyTo: msg.ID, State: DraftPending}
	if s.stale(ctx, t.ID, msg) { // the customer wrote again meanwhile: that message gets its own answer
		draft.State = DraftSuperseded
		_, _ = repo.AddEvent(ctx, draft)
		return
	}
	st := s.LoadSettings(ctx, t.ProjectID)
	if t, err = repo.Ticket(ctx, t.ID); err != nil { // the agent may have set its case meanwhile
		return
	}
	if contains(t.Tags, "human") { // a person took it meanwhile
		draft.State = DraftSuperseded
		_, _ = repo.AddEvent(ctx, draft)
		return
	}
	if st.mode(t, src) == "auto" {
		draft.State = DraftApproved
		if draft, err = repo.AddEvent(ctx, draft); err != nil {
			return
		}
		s.send(ctx, t, draft, answer, "agent")
		return
	}
	if draft, err = repo.AddEvent(ctx, draft); err != nil {
		return
	}
	t, _ = patchTicket(ctx, repo, t.ID, func(x *storage.SupportTicket) { x.Status = StatusWaitingApproval })
	s.fire(ctx, t, "ticket.awaiting_approval", map[string]any{"draft": map[string]any{"id": draft.ID, "text": draft.Body}})
	s.notify(t.ID)
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// stale: a customer message came after msg on the ticket.
func (s *Service) stale(ctx context.Context, ticketID string, msg storage.SupportEvent) bool {
	evs, _ := s.store.Support().Events(ctx, ticketID)
	after := false
	for _, e := range evs {
		if e.ID == msg.ID {
			after = true
			continue
		}
		if after && e.Kind == "customer_msg" {
			return true
		}
	}
	return false
}

// send records the reply going to the customer: the API's caller reads it
// (the response, or polling); a bot, Crisp or email gets it through the
// outbox. Hooks hear ticket.replied.
func (s *Service) send(ctx context.Context, t storage.SupportTicket, draft storage.SupportEvent, text, by string) {
	repo := s.store.Support()
	state := "sent"
	src, err := repo.Source(ctx, t.SourceID)
	if err == nil && way(src) != "" {
		state = "queued"
	}
	e, err := repo.AddEvent(ctx, storage.SupportEvent{TicketID: t.ID, SourceID: t.SourceID, Kind: "sent", Body: text, Actor: by,
		ReplyTo: draft.ReplyTo, ConversationID: draft.ConversationID, State: state, Meta: map[string]any{"draft": draft.ID}})
	if err != nil {
		return
	}
	if state == "queued" {
		if _, err := repo.AddOut(ctx, storage.SupportOut{ProjectID: t.ProjectID, Kind: "reply", RefID: e.ID, TicketID: t.ID}); err == nil {
			s.wake()
		}
	}
	now := time.Now().UTC()
	t, _ = patchTicket(ctx, repo, t.ID, func(x *storage.SupportTicket) {
		if x.FirstResponseAt == nil {
			x.FirstResponseAt = &now
		}
		x.Status = StatusWaitingCustomer
	})
	s.fire(ctx, t, "ticket.replied", map[string]any{"reply": map[string]any{"id": e.ID, "text": text, "by": by}})
	s.notify(t.ID)
}

// fail: no answer came; the ticket waits for a person.
func (s *Service) fail(ctx context.Context, t storage.SupportTicket, msg storage.SupportEvent, why string) {
	repo := s.store.Support()
	_, _ = repo.AddEvent(ctx, storage.SupportEvent{TicketID: t.ID, SourceID: t.SourceID, Kind: "ai_draft", Body: why, Actor: "office",
		JobID: msg.JobID, ReplyTo: msg.ID, State: DraftFailed})
	t, _ = patchTicket(ctx, repo, t.ID, func(x *storage.SupportTicket) { x.Status = StatusOpen })
	s.fire(ctx, t, "ticket.escalated", map[string]any{"reason": "no_answer", "error": why})
	s.notify(t.ID)
}

// Approve sends a pending draft (text: the person's edit, "" = as it is).
func (s *Service) Approve(ctx context.Context, projectID, draftID, text, by string) error {
	repo := s.store.Support()
	d, t, err := s.draft(ctx, projectID, draftID)
	if err != nil {
		return err
	}
	d.State = DraftApproved
	if text = strings.TrimSpace(text); text != "" && text != d.Body {
		d.Meta = map[string]any{"edited": true, "ai": d.Body}
	} else {
		text = d.Body
	}
	if err := repo.UpdateEvent(ctx, d); err != nil {
		return err
	}
	s.send(ctx, t, d, text, by)
	return nil
}

// Reject drops a pending draft; the ticket waits for a person's answer.
func (s *Service) Reject(ctx context.Context, projectID, draftID, by string) error {
	repo := s.store.Support()
	d, t, err := s.draft(ctx, projectID, draftID)
	if err != nil {
		return err
	}
	d.State = DraftRejected
	d.Meta = map[string]any{"by": by}
	if err := repo.UpdateEvent(ctx, d); err != nil {
		return err
	}
	t, _ = patchTicket(ctx, repo, t.ID, func(x *storage.SupportTicket) { x.Status = StatusOpen })
	s.notify(t.ID)
	return nil
}

func (s *Service) draft(ctx context.Context, projectID, draftID string) (storage.SupportEvent, storage.SupportTicket, error) {
	repo := s.store.Support()
	d, err := repo.Event(ctx, draftID)
	if err != nil {
		return d, storage.SupportTicket{}, err
	}
	t, err := repo.Ticket(ctx, d.TicketID)
	if err != nil || t.ProjectID != projectID {
		return d, t, storage.ErrNotFound
	}
	if d.Kind != "ai_draft" || d.State != DraftPending {
		return d, t, invalid("bản nháp này không còn chờ duyệt")
	}
	return d, t, nil
}

// result: what became of a customer message so far.
func (s *Service) result(ctx context.Context, msg storage.SupportEvent) Result {
	r := Result{TicketID: msg.TicketID, EventID: msg.ID, JobID: msg.JobID, Status: statusQueue}
	evs, _ := s.store.Support().Events(ctx, msg.TicketID)
	for _, e := range evs {
		if e.ReplyTo != msg.ID {
			continue
		}
		switch {
		case e.Kind == "sent":
			r.Status, r.Reply = "answered", &Reply{EventID: e.ID, Text: e.Body}
			return r
		case e.Kind == "ai_draft" && e.State == DraftPending:
			r.Status = StatusWaitingApproval
		case e.Kind == "ai_draft" && (e.State == DraftFailed || e.State == DraftRejected):
			r.Status = "failed"
		case e.Kind == "ai_draft" && e.State == DraftSuperseded && r.Status == statusQueue:
			r.Status = "superseded"
		case e.Kind == "status" && e.State == "human":
			r.Status = "human"
		}
	}
	return r
}

// Wait waits up to d for a message's result to settle (not queued).
func (s *Service) Wait(ctx context.Context, msgID string, d time.Duration) (Result, error) {
	msg, err := s.store.Support().Event(ctx, msgID)
	if err != nil {
		return Result{}, err
	}
	deadline := time.Now().Add(min(d, maxWait))
	for {
		ch := s.watch(msg.TicketID)
		r := s.result(ctx, msg)
		if r.Status != statusQueue || time.Now().After(deadline) {
			return r, nil
		}
		select {
		case <-ch:
		case <-time.After(time.Until(deadline)):
		case <-ctx.Done():
			return s.result(context.WithoutCancel(ctx), msg), nil
		}
	}
}

// watch: a channel closed at the ticket's next change.
func (s *Service) watch(ticketID string) <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchers == nil {
		s.watchers = map[string]chan struct{}{}
	}
	ch, ok := s.watchers[ticketID]
	if !ok {
		ch = make(chan struct{})
		s.watchers[ticketID] = ch
	}
	return ch
}

func (s *Service) notify(ticketID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.watchers[ticketID]; ok {
		close(ch)
		delete(s.watchers, ticketID)
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// Answer sends a person's own reply on a ticket (to its latest customer
// message); a draft still waiting is out of date then.
func (s *Service) Answer(ctx context.Context, projectID, ticketID, text, by string) error {
	repo := s.store.Support()
	t, err := repo.Ticket(ctx, ticketID)
	if err != nil || t.ProjectID != projectID {
		return storage.ErrNotFound
	}
	if text = strings.TrimSpace(text); text == "" {
		return invalid("câu trả lời trống")
	}
	evs, err := repo.Events(ctx, t.ID)
	if err != nil {
		return err
	}
	last := ""
	for _, e := range evs {
		if e.Kind == "customer_msg" {
			last = e.ID
		}
	}
	if err := supersede(ctx, repo, t.ID); err != nil {
		return err
	}
	s.send(ctx, t, storage.SupportEvent{ReplyTo: last}, text, by)
	return nil
}

// SetStatus moves a ticket (resolved, closed, open…).
func (s *Service) SetStatus(ctx context.Context, projectID, ticketID, status string) error {
	switch status {
	case StatusOpen, StatusWaitingCustomer, StatusResolved, StatusClosed:
	default:
		return invalid("trạng thái %q không đặt tay được", status)
	}
	repo := s.store.Support()
	t, err := repo.Ticket(ctx, ticketID)
	if err != nil || t.ProjectID != projectID {
		return storage.ErrNotFound
	}
	t, err = patchTicket(ctx, repo, t.ID, func(x *storage.SupportTicket) {
		x.Status = status
		if status == StatusResolved || status == StatusClosed {
			now := time.Now().UTC()
			x.ResolvedAt = &now
		} else {
			x.ResolvedAt = nil
		}
	})
	if err != nil {
		return err
	}
	s.fire(ctx, t, "ticket.status_changed", map[string]any{"status": status})
	if status == StatusResolved || status == StatusClosed {
		s.fire(ctx, t, "ticket.closed", map[string]any{"status": status})
	}
	s.notify(t.ID)
	return nil
}
