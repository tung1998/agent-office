package trigger

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/composio"
	"github.com/tung1998/agent-office/internal/storage"
)

// App events through Composio (ADR-281): Composio posts every trigger of its
// project (a new Gmail message, a Slack mention, a GitHub commit…) to one URL.
// That URL is a webhook automation of office (its token in the query, as
// ?token=…); office reads the event's slug and runs each webhook of the
// project that lists it in app_events, so one URL serves many automations.

// AppEvent is one Composio trigger event, whatever its payload version.
type AppEvent struct {
	Slug    string          `json:"event"`             // GMAIL_NEW_GMAIL_MESSAGE (upper case)
	ID      string          `json:"id,omitempty"`      // the delivery (webhook-id, else the payload's own)
	Trigger string          `json:"trigger,omitempty"` // ti_…: the trigger instance
	Account string          `json:"account,omitempty"` // ca_…: the connected account
	User    string          `json:"user,omitempty"`    // Composio's user id
	Data    json.RawMessage `json:"data"`
}

// ParseAppEvent reads a Composio delivery: V3 ({type: composio.trigger.message,
// metadata.trigger_slug, data}), V2 ({type: slug, data}) or V1 ({trigger_name,
// payload}). ok is false for anything else, which goes on as a plain webhook.
func ParseAppEvent(h http.Header, body []byte) (AppEvent, bool) {
	var p struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		LogID    string `json:"log_id"`
		Metadata struct {
			Slug    string `json:"trigger_slug"`
			Trigger string `json:"trigger_id"`
			Account string `json:"connected_account_id"`
			User    string `json:"user_id"`
		} `json:"metadata"`
		Data        json.RawMessage `json:"data"`
		TriggerName string          `json:"trigger_name"`
		TriggerID   string          `json:"trigger_id"`
		Connection  string          `json:"connection_id"`
		Payload     json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(body, &p) != nil {
		return AppEvent{}, false
	}
	ev := AppEvent{ID: strings.TrimSpace(h.Get("webhook-id"))}
	switch {
	case p.Type == "composio.trigger.message" && p.Metadata.Slug != "": // V3
		ev.Slug, ev.Trigger, ev.Account, ev.User, ev.Data = p.Metadata.Slug, p.Metadata.Trigger, p.Metadata.Account, p.Metadata.User, p.Data
		ev.ID = firstNonEmpty(ev.ID, firstNonEmpty(p.ID, p.Metadata.Trigger+":"+p.LogID))
	case p.TriggerName != "" && len(p.Payload) > 0: // V1
		ev.Slug, ev.Trigger, ev.Account, ev.Data = p.TriggerName, p.TriggerID, p.Connection, p.Payload
		ev.ID = firstNonEmpty(ev.ID, p.LogID)
	case p.Type != "" && len(p.Data) > 0 && p.LogID != "" && slugLike(p.Type): // V2: its data carries the ids
		var d struct {
			Trigger string `json:"trigger_id"`
			Account string `json:"connection_id"`
			User    string `json:"user_id"`
		}
		_ = json.Unmarshal(p.Data, &d)
		ev.Slug, ev.Trigger, ev.Account, ev.User, ev.Data = p.Type, d.Trigger, d.Account, d.User, p.Data
		ev.ID = firstNonEmpty(ev.ID, p.LogID)
	default:
		return AppEvent{}, false
	}
	ev.Slug = strings.ToUpper(strings.TrimSpace(ev.Slug))
	if len(ev.Data) == 0 {
		ev.Data = json.RawMessage("null")
	}
	return ev, ev.Slug != ""
}

// slugLike: a trigger's name (letters, digits, underscores), not a type
// like "issue.created" another sender may post.
func slugLike(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return s != ""
}

// HandlesAppEvent reports whether webhook a takes events of slug. own: the
// event reached a's own URL, where no list (or "*") means every event.
func HandlesAppEvent(a storage.Automation, slug string, own bool) bool {
	if len(a.Config.AppEvents) == 0 {
		return own
	}
	for _, e := range a.Config.AppEvents {
		if e = strings.TrimSpace(e); e == "*" || strings.EqualFold(e, slug) {
			return true
		}
	}
	return false
}

// receiveApp runs every webhook of a's project that takes ev, a first. A
// webhook Composio signs for (ADR-282) takes only signed deliveries.
func (r *Runner) receiveApp(w http.ResponseWriter, req *http.Request, a storage.Automation, ev AppEvent, raw []byte) {
	if r.appSecret != nil {
		if secret := r.appSecret(req.Context(), a.ID); secret != "" && composio.Verify(secret, req.Header, raw, time.Now()) != nil {
			hookJSON(w, http.StatusUnauthorized, map[string]any{"error": composio.ErrBadSignature.Error()})
			return
		}
	}
	targets := []storage.Automation{}
	if HandlesAppEvent(a, ev.Slug, true) {
		targets = append(targets, a)
	}
	if list, err := r.store.Automations().List(req.Context(), a.ProjectID); err == nil {
		for _, o := range list {
			if o.ID != a.ID && o.Source == "webhook" && o.Enabled && !o.Config.PullRequest && HandlesAppEvent(o, ev.Slug, false) {
				targets = append(targets, o)
			}
		}
	}
	if len(targets) == 0 {
		hookJSON(w, http.StatusOK, map[string]any{"status": "ignored", "reason": "no automation takes " + ev.Slug})
		return
	}
	body, _ := json.Marshal(ev)
	dedupe := "app:" + ev.ID
	if ev.ID == "" {
		dedupe = ""
	}
	jobs, queued := []map[string]string{}, false
	for _, t := range targets {
		d := dedupe
		if d == "" {
			d = bodyKey(body)
		}
		j, status, err := r.Enqueue(req.Context(), t, "webhook", string(body), d, debounceKey(t, body))
		if err != nil {
			hookJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "chưa nhận được, thử lại sau"})
			return
		}
		queued = queued || status == "queued"
		jobs = append(jobs, map[string]string{"automation_id": t.ID, "status": status, "job_id": j.ID})
	}
	if queued {
		r.StartReady(context.WithoutCancel(req.Context()), time.Now().UTC())
	}
	hookJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "event": ev.Slug, "jobs": jobs})
}

// CleanAppEvents is a list of slugs as saved: trimmed, upper case, no repeats
// or blanks, at most 50.
func CleanAppEvents(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.ToUpper(strings.TrimSpace(e))
		if e == "" || seen[e] || (e != "*" && !slugLike(e)) || len(out) >= 50 {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}
