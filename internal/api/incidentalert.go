package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/features"
	"github.com/tung1998/agent-office/internal/storage"
)

// incidentAlertKey is where IncidentAlertSettings is kept (Settings store).
const incidentAlertKey = "incident_alert"

// IncidentAlertSettings is where incidents (monitor down, process crashed, a
// broken MCP server…) are told, so they reach a person even with no
// dashboard tab open — the badge alone (ADR-076/077/078) only works for that.
type IncidentAlertSettings struct {
	ChannelID   string `json:"channel_id"`
	ChatID      string `json:"chat_id"`
	MinSeverity string `json:"min_severity"` // error | warning ("" = error)
	// BaseURL: the office's own address (e.g. https://office.example.com), so
	// the notice's link is one a person can tap from Discord/Telegram. Empty
	// = the notice carries no link (a relative path means nothing there).
	BaseURL string `json:"base_url"`
}

func loadIncidentAlert(ctx context.Context, s *server) IncidentAlertSettings {
	var v IncidentAlertSettings
	_, _ = s.cfg.Store.Settings().Get(ctx, incidentAlertKey, &v)
	if v.MinSeverity != "warning" {
		v.MinSeverity = "error"
	}
	return v
}

func (s *server) getIncidentAlert(w http.ResponseWriter, r *http.Request) {
	set := loadIncidentAlert(r.Context(), s)
	writeJSON(w, http.StatusOK, map[string]any{"channel_id": set.ChannelID, "chat_id": set.ChatID, "min_severity": set.MinSeverity, "base_url": set.BaseURL, "bots": s.allBots(r.Context())})
}

func (s *server) setIncidentAlert(w http.ResponseWriter, r *http.Request) {
	var in IncidentAlertSettings
	if !decode(w, r, &in) {
		return
	}
	in.ChannelID, in.ChatID, in.BaseURL = strings.TrimSpace(in.ChannelID), strings.TrimSpace(in.ChatID), strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	if in.MinSeverity != "warning" {
		in.MinSeverity = "error"
	}
	if in.ChannelID != "" {
		if _, err := s.cfg.Store.Channels().Get(r.Context(), in.ChannelID); err != nil {
			writeError(w, http.StatusBadRequest, "không có bot này")
			return
		}
	}
	before := loadIncidentAlert(r.Context(), s)
	if err := s.cfg.Store.Settings().Set(r.Context(), incidentAlertKey, in); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "incident_alert.update", Resource: "setting", ResourceID: incidentAlertKey, Before: before, After: in})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func sentKey(key string) string { return "incident_sent/" + key }

// pollIncidentAlertsLoop runs for the office's whole life (there is no
// request to tie it to): every few minutes it looks for incidents and tells
// the chat the admin picked, so a crash reaches someone even with no
// dashboard tab open.
func (s *server) pollIncidentAlertsLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		s.PollIncidentAlerts(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// PollIncidentAlerts sends a notice for every new system incident (one not
// already sent for this occurrence — systemIncidents' Key carries the
// occurrence, e.g. a process's crash, so a fixed-then-broken-again process
// tells again). Pha 1: a push only, no two-way reply.
func (s *server) PollIncidentAlerts(ctx context.Context) {
	set := loadIncidentAlert(ctx, s)
	n, ok := s.cfg.Channels.(Notifier)
	if !ok || set.ChannelID == "" || set.ChatID == "" {
		return
	}
	for _, it := range s.systemIncidents(ctx) {
		if set.MinSeverity == "error" && it.Severity != "error" {
			continue
		}
		var sent bool
		if ok, _ := s.cfg.Store.Settings().Get(ctx, sentKey(it.Key), &sent); ok && sent {
			continue
		}
		title := it.Title
		if it.ProjectName != "" {
			title = it.ProjectName + ": " + title
		}
		text := "🚨 " + title // i18n-ignore
		if it.Detail != "" {
			text += " — " + it.Detail
		}
		if set.BaseURL != "" && it.Link != "" {
			text += "\n" + set.BaseURL + it.Link
		}
		nctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := n.Notify(nctx, set.ChannelID, set.ChatID, text)
		cancel()
		if err != nil {
			slog.Warn("incident alert: notify failed", "key", it.Key, "err", err)
			continue
		}
		_ = s.cfg.Store.Settings().Set(ctx, sentKey(it.Key), true)
	}
}

// systemIncidents: the incidents that are the office's own health (monitor,
// process, MCP server), across every project, with nobody signed in — there
// is no request here, unlike incidentsFor. Deliberately narrower: it skips a
// person's own cards (approvals, unread chats…), which are not what this
// Burn piece asked for (docs/PLAN.md:122).
func (s *server) systemIncidents(ctx context.Context) []incident {
	projects, err := s.cfg.Store.Repos().List(ctx)
	if err != nil {
		return nil
	}
	hidden := assistant.ID(ctx, s.cfg.Store)
	day := time.Now().UTC().Add(-24 * time.Hour)
	var out []incident
	for _, p := range projects {
		if p.ID == hidden {
			continue
		}
		base := "/projects/" + p.ID
		on, _ := features.Effective(features.LoadConfig(ctx, s.cfg.Store, p.ID), p.Path != "")
		// key carries when it happened (like mcpIncidents' key, incidents.go:269),
		// not just the monitor/process's fixed ID — so sentKey only ever matches
		// this occurrence: the next time it goes down tells again.
		add := func(kind, sev, title, detail string, at *time.Time, link, id string) {
			it := incident{Kind: kind, Severity: sev, ProjectID: p.ID, ProjectName: p.Name, Title: title, Detail: detail, Link: link, ID: id, Key: kind + ":" + id}
			if at != nil {
				it.At = *at
				it.Key += ":" + strconv.FormatInt(at.Unix(), 10)
			}
			out = append(out, it)
		}
		if features.Has(on, features.Ops) {
			ms, _ := s.cfg.Store.Monitors().List(ctx, p.ID)
			for _, m := range ms {
				if m.Enabled && m.Status == "down" {
					add("monitor", "error", m.Name, m.LastMessage, m.LastChangeAt, base+"?tab=ops", m.ID)
				}
			}
			if s.cfg.Ops != nil {
				if ps, err := s.cfg.Store.Processes().List(ctx, p.ID); err == nil {
					for _, x := range ps {
						if st := s.cfg.Ops.State(x.ID); st.Status == "crashed" {
							add("process", "error", x.Name, fmt.Sprintf("thoát, đã khởi động lại %d lần", st.Restarts), st.FinishedAt, base+"?tab=ops", x.ID) // i18n-ignore
						}
					}
				}
			}
		}
		if as, err := s.cfg.Store.Automations().List(ctx, p.ID); err == nil {
			for _, a := range as {
				if a.DisabledCode != "" && a.DisabledCode != "ended" { // stopped at its stop time: as asked, no incident
					add("automation", "warning", a.Name, a.DisabledReason, &a.UpdatedAt, base+"/automations/"+a.ID, a.ID)
				}
			}
		}
		if failed, err := s.cfg.Store.Jobs().List(ctx, storage.JobFilter{ProjectID: p.ID, Status: "failed", Since: day, Limit: 200}); err == nil && len(failed) > 0 {
			var fresh []storage.Job
			for _, j := range failed {
				if !(j.Kind == "chat_turn" && chat.Stalled(j.ErrorCode)) && j.RetryAt == nil { // a chat out of tokens, or going on by itself, is its own item
					fresh = append(fresh, j)
				}
			}
			if len(fresh) > 0 {
				last := fresh[0]
				add("jobs", "warning", fmt.Sprintf("%d job lỗi trong 24 giờ", len(fresh)), last.Title+": "+last.Error, &last.CreatedAt, "/jobs?project="+p.ID, p.ID) // i18n-ignore
			}
		}
	}
	out = append(out, s.mcpIncidents(ctx)...)
	return out
}
