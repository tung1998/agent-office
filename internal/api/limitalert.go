package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/limitalert"
)

// Where the AI connections' limit alerts go (a bot's chat), and from how full.

type alertBot struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Project   string `json:"project"`
	ProjectID string `json:"project_id"`
}

// allBots: every project's bots, for what any of them can announce in (the
// limit alerts, an automation or a Burn borrowing another project's bot)
func (s *server) allBots(ctx context.Context) []alertBot {
	chans, _ := s.cfg.Store.Channels().List(ctx, "")
	names := map[string]string{}
	bots := []alertBot{}
	for _, c := range chans {
		name := c.Name
		if c.BotName != "" {
			name = "@" + c.BotName
		}
		project, ok := names[c.ProjectID]
		if !ok {
			project = c.ProjectID
			if p, err := s.cfg.Store.Repos().Get(ctx, c.ProjectID); err == nil {
				project = p.Name
			}
			names[c.ProjectID] = project
		}
		bots = append(bots, alertBot{c.ID, name, c.Kind, project, c.ProjectID})
	}
	return bots
}

// listAllBots: the bots a notice may go out by, of the projects the person
// sees (a manager borrows only from its own, ADR-224).
func (s *server) listAllBots(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scope(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	bots := []alertBot{}
	for _, b := range s.allBots(r.Context()) {
		if sc.has(b.ProjectID) {
			bots = append(bots, b)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bots": bots})
}

func (s *server) getLimitAlert(w http.ResponseWriter, r *http.Request) {
	set := limitalert.Load(r.Context(), s.cfg.Store)
	bots := s.allBots(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"channel_id": set.ChannelID, "chat_id": set.ChatID, "threshold": set.Threshold, "errors": set.Errors, "bots": bots})
}

func (s *server) setLimitAlert(w http.ResponseWriter, r *http.Request) {
	var in limitalert.Settings
	if !decode(w, r, &in) {
		return
	}
	in.ChannelID, in.ChatID = strings.TrimSpace(in.ChannelID), strings.TrimSpace(in.ChatID)
	if e := in.Errors; e != nil && (e.Streak < 0 || e.Count < 0 || e.Window < 0 || e.Streak > 100 || e.Count > 1000 || e.Window > 1440) {
		writeError(w, http.StatusBadRequest, "quy tắc cảnh báo lỗi không hợp lệ (liên tiếp 0–100, số lỗi 0–1000, trong 0–1440 phút)")
		return
	}
	if in.ChannelID != "" {
		if _, err := s.cfg.Store.Channels().Get(r.Context(), in.ChannelID); err != nil {
			writeError(w, http.StatusBadRequest, "không có bot này")
			return
		}
	}
	before := limitalert.Load(r.Context(), s.cfg.Store)
	if err := limitalert.Save(r.Context(), s.cfg.Store, in); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "limit_alert.update", Resource: "setting", ResourceID: "limit_alert", Before: before, After: in})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Notifier posts to a bot's chat (the channels manager).
type Notifier interface {
	Notify(ctx context.Context, channelID, chatID, text string) error
}

func (s *server) testLimitAlert(w http.ResponseWriter, r *http.Request) {
	set := limitalert.Load(r.Context(), s.cfg.Store)
	n, ok := s.cfg.Channels.(Notifier)
	if !ok || set.ChannelID == "" || set.ChatID == "" {
		writeError(w, http.StatusBadRequest, "chưa chọn bot và kênh nhận cảnh báo")
		return
	}
	if err := n.Notify(r.Context(), set.ChannelID, set.ChatID, "✅ Office sẽ báo ở đây khi hạn mức AI vượt "+fmt.Sprint(set.Threshold)+"%"+errorRuleText(*set.Errors)+"."); err != nil { // i18n-ignore
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// limitIncidents: windows past the threshold, until they reset.
func (s *server) limitIncidents(ctx context.Context) []incident {
	set := limitalert.Load(ctx, s.cfg.Store)
	provs, err := s.cfg.Store.Providers().List(ctx)
	if err != nil {
		return nil
	}
	var out []incident
	now := time.Now()
	for _, p := range provs {
		var l chat.Limits
		if ok, _ := s.cfg.Store.Settings().Get(ctx, chat.LimitsKey(p.ID), &l); !ok {
			continue
		}
		for name, win := range l.Windows {
			pct := int(win.Utilization*100 + 0.5)
			if pct < set.Threshold || !win.ResetsAt.After(now) {
				continue
			}
			label := limitalert.WindowNames[name]
			if label == "" {
				label = name
			}
			sev := "warning"
			if pct >= 95 {
				sev = "error"
			}
			at := l.UpdatedAt
			out = append(out, incident{Kind: "limit", Severity: sev, Title: fmt.Sprintf("%s: %s %d%%", p.Name, label, pct),
				Detail: "reset " + win.ResetsAt.Local().Format("15:04 02/01"), At: at, Link: "/providers",
				ID: p.ID, Key: "limit:" + p.ID + ":" + name + ":" + fmt.Sprint(win.ResetsAt.Unix()/60)})
		}
	}
	return out
}

// errorRuleText says the failed-prompt rule in the test message ("" = off).
func errorRuleText(e limitalert.ErrorRule) string {
	var parts []string
	if e.Streak > 0 {
		parts = append(parts, fmt.Sprintf("%d lần lỗi liên tiếp", e.Streak)) // i18n-ignore
	}
	if e.Count > 0 {
		parts = append(parts, fmt.Sprintf("%d lần lỗi trong %d phút", e.Count, e.Window)) // i18n-ignore
	}
	if len(parts) == 0 {
		return ""
	}
	return ", và khi gửi prompt bị " + strings.Join(parts, " hoặc ") // i18n-ignore
}
