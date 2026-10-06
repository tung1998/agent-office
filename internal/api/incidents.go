package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

// incident is something across the office that needs a person, with where to
// fix it: a monitor down, a process crashed, an automation office turned off,
// a bot that lost its connection, tasks and runs that failed, a card waiting.
type incident struct {
	Kind        string    `json:"kind"`     // monitor | process | automation | bot | jobs | approval | patch | unread | stalled
	Severity    string    `json:"severity"` // error | warning
	ProjectID   string    `json:"project_id"`
	ProjectName string    `json:"project_name"`
	Title       string    `json:"title"`
	Detail      string    `json:"detail"`
	At          time.Time `json:"at"`
	Link        string    `json:"link"`
	ID          string    `json:"id"`  // what it is about: the monitor, process, automation, bot, latest failed job or action
	Key         string    `json:"key"` // for Bỏ qua: kind and what it is about
	// Prompt: stalled — what Tiếp tục sends to the chat (to the agent that stopped)
	Prompt string `json:"prompt,omitempty"`
}

func dismissKey(key string) string { return "incident_dismissed/" + key }

func (s *server) incidents(w http.ResponseWriter, r *http.Request) {
	out, err := s.incidentsFor(r.Context(), userFrom(r))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": out, "count": len(out)})
}

// incidentsFor is what needs u now (also pushed to their open pages, ADR-078).
func (s *server) incidentsFor(ctx context.Context, u storage.User) ([]incident, error) {
	projects, err := s.cfg.Store.Repos().List(ctx)
	if err != nil {
		return nil, err
	}
	hidden := assistant.ID(ctx, s.cfg.Store)
	day := time.Now().UTC().Add(-24 * time.Hour)
	out := []incident{}
	for _, p := range projects {
		if p.ID == hidden {
			continue
		}
		base := "/projects/" + p.ID
		add := func(kind, sev, title, detail string, at *time.Time, link, id, key string) {
			it := incident{Kind: kind, Severity: sev, ProjectID: p.ID, ProjectName: p.Name, Title: title, Detail: detail, Link: link, ID: id, Key: kind + ":" + key}
			if at != nil {
				it.At = *at
			}
			// let go (Bỏ qua): hidden until it happens again
			var gone time.Time
			if ok, _ := s.cfg.Store.Settings().Get(ctx, dismissKey(it.Key), &gone); ok && !it.At.After(gone) {
				return
			}
			out = append(out, it)
		}
		if ms, err := s.cfg.Store.Monitors().List(ctx, p.ID); err == nil {
			for _, m := range ms {
				if m.Enabled && m.Status == "down" {
					add("monitor", "error", m.Name, m.LastMessage, m.LastChangeAt, base+"?tab=ops", m.ID, m.ID)
				}
			}
		}
		if s.cfg.Ops != nil {
			if ps, err := s.cfg.Store.Processes().List(ctx, p.ID); err == nil {
				for _, x := range ps {
					if st := s.cfg.Ops.State(x.ID); st.Status == "crashed" {
						add("process", "error", x.Name, fmt.Sprintf("thoát, đã khởi động lại %d lần", st.Restarts), st.FinishedAt, base+"?tab=ops", x.ID, x.ID)
					}
				}
			}
		}
		if as, err := s.cfg.Store.Automations().List(ctx, p.ID); err == nil {
			for _, a := range as {
				if a.DisabledCode != "" && a.DisabledCode != "ended" { // stopped at its stop time: as asked, no incident
					add("automation", "warning", a.Name, a.DisabledReason, &a.UpdatedAt, base+"/automations/"+a.ID, a.ID, a.ID)
				}
			}
		}
		if cs, err := s.cfg.Store.Channels().List(ctx, p.ID); err == nil {
			for _, c := range cs {
				if c.Enabled && c.LastError != "" {
					add("bot", "error", firstNonEmptyStr(c.BotName, c.Name), c.LastError, &c.UpdatedAt, base+"/bots/"+c.ID, c.ID, c.ID)
				}
			}
		}
		if failed, err := s.cfg.Store.Jobs().List(ctx, storage.JobFilter{ProjectID: p.ID, Status: "failed", Since: day, Limit: 200}); err == nil && len(failed) > 0 {
			// counted since it was last let go
			var gone time.Time
			_, _ = s.cfg.Store.Settings().Get(ctx, dismissKey("jobs:"+p.ID), &gone)
			fresh := failed[:0:0]
			for _, j := range failed {
				if j.CreatedAt.After(gone) && !(j.Kind == "chat_turn" && chat.Stalled(j.ErrorCode)) { // a chat out of tokens is its own item
					fresh = append(fresh, j)
				}
			}
			if len(fresh) > 0 {
				last := fresh[0]
				add("jobs", "warning", fmt.Sprintf("%d job lỗi trong 24 giờ", len(fresh)), last.Title+": "+last.Error, &last.CreatedAt, "/jobs?project="+p.ID, last.ID, p.ID)
			}
		}
		for _, it := range s.stalledChats(ctx, u, p) {
			var gone time.Time
			if ok, _ := s.cfg.Store.Settings().Get(ctx, dismissKey(it.Key), &gone); ok && !it.At.After(gone) {
				continue
			}
			out = append(out, it)
		}
	}
	if u.Role == storage.RoleAdmin { // cards waiting for a person
		if acts, err := s.cfg.Store.Actions().Pending(ctx, 100); err == nil {
			for _, a := range acts {
				name := a.ProjectID
				for _, p := range projects {
					if p.ID == a.ProjectID {
						name = p.Name
					}
				}
				link := "/projects/" + a.ProjectID + "?tab=chat"
				if a.ConversationID != "" {
					link += "&c=" + a.ConversationID
				} else {
					link = "/assistant"
				}
				out = append(out, incident{Kind: "approval", Severity: "warning", ProjectID: a.ProjectID, ProjectName: name,
					Title: firstNonEmptyStr(a.Target, a.Kind), Detail: a.Reason, At: a.CreatedAt, Link: link, ID: a.ID, Key: "approval:" + a.ID})
			}
		}
	}
	if u.Role == storage.RoleAdmin { // chats' diffs waiting for a person
		if ps, err := s.cfg.Store.Chat().PendingPatches(ctx, 100); err == nil {
			for _, p := range ps {
				c, err := s.cfg.Store.Chat().GetConversation(ctx, p.ConversationID)
				if err != nil || c.ProjectID == hidden {
					continue
				}
				name := c.ProjectID
				for _, x := range projects {
					if x.ID == c.ProjectID {
						name = x.Name
					}
				}
				out = append(out, incident{Kind: "patch", Severity: "warning", ProjectID: c.ProjectID, ProjectName: name,
					Title: strings.Join(p.Files, ", "), Detail: c.Title, At: p.CreatedAt, Link: "/projects/" + c.ProjectID + "?tab=chat&c=" + c.ID + "&m=" + p.MessageID,
					ID: p.ID, Key: "patch:" + p.ID})
			}
		}
	}
	// the person's chats an agent answered since they last looked (theirs alone)
	if ids, err := s.cfg.Store.Chat().Unread(ctx, u.ID, "human:"+u.Email); err == nil {
		for _, id := range ids {
			c, err := s.cfg.Store.Chat().GetConversation(ctx, id)
			if err != nil {
				continue
			}
			name := c.ProjectID
			for _, x := range projects {
				if x.ID == c.ProjectID {
					name = x.Name
				}
			}
			link := "/projects/" + c.ProjectID + "?tab=chat&c=" + c.ID
			if c.ProjectID == hidden {
				link = "/assistant"
			}
			out = append(out, incident{Kind: "unread", Severity: "info", ProjectID: c.ProjectID, ProjectName: name,
				Title: c.Title, At: c.UpdatedAt, Link: link, ID: c.ID, Key: "unread:" + c.ID})
		}
	}
	for _, it := range s.limitIncidents(ctx) { // an AI connection close to its limit
		var gone time.Time
		if ok, _ := s.cfg.Store.Settings().Get(ctx, dismissKey(it.Key), &gone); ok {
			continue
		}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Severity == "error") != (out[j].Severity == "error") {
			return out[i].Severity == "error"
		}
		return out[i].At.After(out[j].At)
	})
	return out, nil
}

// dismissIncident lets an incident go (Bỏ qua): it shows again only when it
// happens again.
func (s *server) dismissIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Key == "" || strings.HasPrefix(in.Key, "approval:") || strings.HasPrefix(in.Key, "patch:") { // a card is decided, not let go
		writeError(w, http.StatusBadRequest, "không bỏ qua được mục này")
		return
	}
	if err := s.cfg.Store.Settings().Set(r.Context(), dismissKey(in.Key), time.Now().UTC()); err != nil {
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "incident.dismiss", in.Key, nil)
	w.WriteHeader(http.StatusNoContent)
}

// retryIncident tries again what an incident is about: checks a monitor now,
// restarts a process, turns an automation back on, reconnects a bot. (A
// failed job is retried through /api/jobs/{id}/retry.)
func (s *server) retryIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	var err error
	switch in.Kind {
	case "monitor":
		if s.cfg.Monitors == nil {
			err = errors.New("chưa bật theo dõi")
		} else {
			_, err = s.cfg.Monitors.CheckNow(ctx, in.ID)
		}
	case "process":
		if s.cfg.Ops == nil {
			err = errors.New("chưa bật vận hành")
		} else {
			err = s.cfg.Ops.Restart(ctx, in.ID)
		}
	case "automation":
		var a storage.Automation
		if a, err = s.cfg.Store.Automations().Get(ctx, in.ID); err == nil && a.Config.EndsAt != nil && !a.Config.EndsAt.After(time.Now()) {
			err = errors.New("giờ dừng đã qua: hãy đặt giờ dừng mới hoặc bỏ giờ dừng")
		} else if err == nil {
			a.Enabled, a.Failures, a.DisabledCode, a.DisabledReason = true, 0, "", "" // turned back on
			if a.Source == "schedule" {
				if next, nerr := trigger.NextRun(a.Config, time.Now().UTC()); nerr == nil {
					a.NextRunAt = &next
				}
			}
			if err = s.cfg.Store.Automations().Update(ctx, a); err == nil {
				s.reloadBot(a)
			}
		}
	case "bot":
		if s.cfg.Channels == nil {
			err = errors.New("chưa bật bot")
		} else if _, err = s.cfg.Store.Channels().Get(ctx, in.ID); err == nil {
			s.cfg.Channels.Reload(in.ID)
		}
	default:
		writeError(w, http.StatusBadRequest, "không chạy lại được mục này")
		return
	}
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.auditAction(r, "incident.retry", in.Kind+":"+in.ID, nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
