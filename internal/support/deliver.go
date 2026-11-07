package support

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	base64pkg "encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Going out (ADR-174): a reply to a customer on a bot, Crisp or email, and
// each hook told of a ticket's event, go through the outbox: delivered by
// Run, tried again with a wait (30s, 2m, 10m), failed after the 4th try.
// The API and a form without email have no way out: their caller reads the
// reply (or a hook does).

// Box seals and opens secrets (secrets.Box).
type Box interface {
	Seal(plaintext string) (string, error)
	Open(sealed string) (string, error)
}

// SetBox sets what keeps the sources' and hooks' secrets.
func (s *Service) SetBox(b Box) { s.box = b }

// SetBotSender sets how a reply reaches a bot's chat.
func (s *Service) SetBotSender(fn func(ctx context.Context, channelID, chatID, text string) error) {
	s.botSend = fn
}

const maxAttempts = 4

var backoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// Events a hook can be told of.
var Events = []string{"ticket.created", "ticket.message", "ticket.awaiting_approval", "ticket.replied", "ticket.escalated",
	"ticket.status_changed", "ticket.sla_breached", "ticket.closed"}

// Run delivers the outbox and checks SLAs until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		s.Deliver(ctx, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.kick:
		}
	}
}

func (s *Service) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Deliver sends what is due, and tells the tickets past their SLA.
func (s *Service) Deliver(ctx context.Context, now time.Time) {
	s.deliverMu.Lock()
	defer s.deliverMu.Unlock()
	s.sweepSLA(ctx, now)
	s.syncAll(ctx, now)
	due, err := s.store.Support().DueOut(ctx, now, 50)
	if err != nil {
		return
	}
	for _, o := range due {
		var err error
		switch o.Kind {
		case "reply":
			err = s.deliverReply(ctx, o)
		case "hook":
			err = s.deliverHook(ctx, o)
		default:
			err = fmt.Errorf("không biết gửi %q", o.Kind)
		}
		o.Attempts++
		switch {
		case err == nil:
			o.Status, o.LastError = "sent", ""
		case o.Attempts >= maxAttempts:
			o.Status, o.LastError = "failed", err.Error()
		default:
			o.LastError, o.NextAt = err.Error(), now.Add(backoff[min(o.Attempts-1, len(backoff)-1)])
		}
		_ = s.store.Support().UpdateOut(ctx, o)
		if o.Kind == "reply" && o.Status != "pending" {
			s.replyDelivered(ctx, o)
		}
	}
}

// replyDelivered marks the reply's history line sent or failed (a failed
// one leaves the ticket to a person).
func (s *Service) replyDelivered(ctx context.Context, o storage.SupportOut) {
	repo := s.store.Support()
	e, err := repo.Event(ctx, o.RefID)
	if err != nil {
		return
	}
	e.State = o.Status
	if o.Status == "failed" {
		if e.Meta == nil {
			e.Meta = map[string]any{}
		}
		e.Meta["error"] = o.LastError
	}
	_ = repo.UpdateEvent(ctx, e)
	if o.Status == "failed" {
		if t, err := patchTicket(ctx, repo, o.TicketID, func(x *storage.SupportTicket) { x.Status = StatusOpen }); err == nil {
			s.fire(ctx, t, "ticket.escalated", map[string]any{"reason": "delivery_failed", "error": o.LastError})
		}
	}
	s.notify(o.TicketID)
}

// deliverReply sends a reply where its ticket's customer is.
func (s *Service) deliverReply(ctx context.Context, o storage.SupportOut) error {
	repo := s.store.Support()
	e, err := repo.Event(ctx, o.RefID)
	if err != nil {
		return err
	}
	t, err := repo.Ticket(ctx, o.TicketID)
	if err != nil {
		return err
	}
	src, err := repo.Source(ctx, t.SourceID)
	if err != nil {
		return err
	}
	switch way(src) {
	case "bot":
		if s.botSend == nil {
			return errors.New("bot chưa sẵn sàng")
		}
		return s.botSend(ctx, str(src.Config["channel_id"]), t.ExternalThread, e.Body)
	case "crisp":
		return s.crispSend(ctx, src, t.ExternalThread, e.Body)
	case "email":
		to := s.customerEmail(ctx, t)
		if to == "" {
			return errors.New("khách không có email")
		}
		return s.mail(ctx, src, to, "Re: "+t.Subject, e.Body)
	}
	return nil
}

// way: how a source's replies go out ("" = they do not: the caller reads them).
func way(src storage.SupportSource) string {
	switch src.Kind {
	case "bot", "crisp":
		return src.Kind
	}
	if str(src.Config["reply_via"]) == "email" {
		return "email"
	}
	return ""
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func (s *Service) secret(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	if s.box == nil {
		return "", errors.New("chưa mở được bí mật")
	}
	return s.box.Open(sealed)
}

// CrispAPI is Crisp's REST API (a test points it elsewhere).
var CrispAPI = "https://api.crisp.chat"

func (s *Service) crispSend(ctx context.Context, src storage.SupportSource, session, text string) error {
	key, err := s.secret(src.SecretEnc)
	if err != nil {
		return err
	}
	website, ident := str(src.Config["website_id"]), str(src.Config["identifier"])
	if website == "" || ident == "" || key == "" || session == "" {
		return errors.New("Crisp chưa đủ website_id, identifier, key")
	}
	body, _ := json.Marshal(map[string]any{"type": "text", "from": "operator", "origin": "chat", "content": text})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, CrispAPI+"/v1/website/"+website+"/conversation/"+session+"/message", bytes.NewReader(body))
	req.SetBasicAuth(ident, key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Crisp-Tier", "plugin")
	return s.do(req)
}

func (s *Service) do(req *http.Request) error {
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func (s *Service) customerEmail(ctx context.Context, t storage.SupportTicket) string {
	if t.CustomerID != "" {
		if x, _, err := s.collections.Get(ctx, t.ProjectID, "customers", t.CustomerID); err == nil {
			return str(x.Data["email"])
		}
	}
	return ""
}

// SendMail sends one mail (net/smtp; a test replaces it).
var SendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return smtp.SendMail(addr, auth, from, to, msg)
}

func (s *Service) mail(_ context.Context, src storage.SupportSource, to, subject, body string) error {
	pass, err := s.secret(src.SecretEnc)
	if err != nil {
		return err
	}
	host, user, from := str(src.Config["smtp_host"]), str(src.Config["smtp_user"]), str(src.Config["smtp_from"])
	port := str(src.Config["smtp_port"])
	if port == "" {
		port = "587"
	}
	if host == "" || from == "" {
		return errors.New("email chưa đủ smtp_host, smtp_from")
	}
	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	msg := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + mimeWord(subject) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + strings.ReplaceAll(body, "\n", "\r\n")
	return SendMail(net.JoinHostPort(host, port), auth, from, []string{to}, []byte(msg))
}

// mimeWord: a header in UTF-8 (RFC 2047).
func mimeWord(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?UTF-8?B?" + b64(s) + "?="
		}
	}
	return s
}

// fire tells the project's hooks of a ticket's event.
func (s *Service) fire(ctx context.Context, t storage.SupportTicket, event string, extra map[string]any) {
	hooks, err := s.store.Support().Hooks(ctx, t.ProjectID)
	if err != nil {
		return
	}
	var payload map[string]any
	for _, h := range hooks {
		if !h.Enabled || (len(h.Events) > 0 && !contains(h.Events, event)) {
			continue
		}
		if payload == nil {
			payload = s.eventPayload(ctx, t, event, extra)
		}
		if _, err := s.store.Support().AddOut(ctx, storage.SupportOut{ProjectID: t.ProjectID, Kind: "hook", RefID: h.ID, TicketID: t.ID,
			Event: event, Payload: payload}); err != nil {
			slog.Error("support: hook", "hook", h.ID, "err", err)
		}
	}
	if payload != nil {
		s.wake()
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Service) eventPayload(ctx context.Context, t storage.SupportTicket, event string, extra map[string]any) map[string]any {
	p := map[string]any{"event": event, "at": time.Now().UTC(),
		"ticket": map[string]any{"id": t.ID, "status": t.Status, "case_type": t.CaseType, "subject": t.Subject, "source_id": t.SourceID,
			"thread": t.ExternalThread, "customer_id": t.CustomerID}}
	if t.CustomerID != "" {
		if x, _, err := s.collections.Get(ctx, t.ProjectID, "customers", t.CustomerID); err == nil {
			p["customer"] = x.Data
		}
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// deliverHook: a POST signed with the hook's secret (X-Office-Signature:
// sha256=<hex HMAC of the body>), or the hook's automation queued.
func (s *Service) deliverHook(ctx context.Context, o storage.SupportOut) error {
	h, err := s.store.Support().Hook(ctx, o.RefID)
	if err != nil {
		return errors.New("hook không còn")
	}
	body, _ := json.Marshal(o.Payload)
	if h.Kind == "automation" {
		a, err := s.store.Automations().Get(ctx, h.AutomationID)
		if err != nil || a.ProjectID != h.ProjectID || s.runner == nil {
			return errors.New("tự động hóa của hook không còn")
		}
		if _, _, err := s.runner.Enqueue(ctx, a, "webhook", `{"support_event":`+string(body)+`}`, "hook:"+o.ID, ""); err != nil {
			return err
		}
		s.runner.StartReady(context.WithoutCancel(ctx), time.Now().UTC())
		return nil
	}
	secret, err := s.secret(h.SecretEnc)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Office-Event", o.Event)
	req.Header.Set("X-Office-Delivery", o.ID)
	req.Header.Set("X-Office-Signature", "sha256="+Sign(secret, body))
	return s.do(req)
}

// Sign is the hex HMAC-SHA256 of body with secret (what a receiver checks).
func Sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// sweepSLA: a ticket past its first-answer deadline is told once.
func (s *Service) sweepSLA(ctx context.Context, now time.Time) {
	if now.Sub(s.lastSweep) < time.Minute {
		return
	}
	s.lastSweep = now
	list, err := s.store.Support().Overdue(ctx, now)
	if err != nil {
		return
	}
	for _, t := range list {
		t, err := patchTicket(ctx, s.store.Support(), t.ID, func(x *storage.SupportTicket) { x.SLABreached = true })
		if err != nil {
			continue
		}
		_, _ = s.store.Support().AddEvent(ctx, storage.SupportEvent{TicketID: t.ID, SourceID: t.SourceID, Kind: "status", Body: "sla_breached", Actor: "office"})
		s.fire(ctx, t, "ticket.sla_breached", map[string]any{"due_at": t.SLADueAt})
		s.notify(t.ID)
	}
}

func atoi(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

func b64(s string) string { return base64Std.EncodeToString([]byte(s)) }

var base64Std = base64pkg.StdEncoding

// syncAll refreshes the playbooks of every project with the pack on, hourly.
func (s *Service) syncAll(ctx context.Context, now time.Time) {
	if now.Sub(s.lastSync) < time.Hour {
		return
	}
	s.lastSync = now
	projects, err := s.store.Repos().List(ctx)
	if err != nil {
		return
	}
	for _, p := range projects {
		if s.On(ctx, p.ID) {
			if _, err := s.SyncPlaybooks(ctx, p.ID); err != nil {
				slog.Warn("support: playbooks", "project", p.ID, "err", err)
			}
		}
	}
}
