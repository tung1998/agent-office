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
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Project string `json:"project"`
}

func (s *server) getLimitAlert(w http.ResponseWriter, r *http.Request) {
	set := limitalert.Load(r.Context(), s.cfg.Store)
	chans, _ := s.cfg.Store.Channels().List(r.Context(), "")
	bots := []alertBot{}
	for _, c := range chans {
		name := c.Name
		if c.BotName != "" {
			name = "@" + c.BotName
		}
		project := c.ProjectID
		if p, err := s.cfg.Store.Repos().Get(r.Context(), c.ProjectID); err == nil {
			project = p.Name
		}
		bots = append(bots, alertBot{c.ID, name, c.Kind, project})
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel_id": set.ChannelID, "chat_id": set.ChatID, "threshold": set.Threshold, "bots": bots})
}

func (s *server) setLimitAlert(w http.ResponseWriter, r *http.Request) {
	var in limitalert.Settings
	if !decode(w, r, &in) {
		return
	}
	in.ChannelID, in.ChatID = strings.TrimSpace(in.ChannelID), strings.TrimSpace(in.ChatID)
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
	if err := n.Notify(r.Context(), set.ChannelID, set.ChatID, "✅ Office sẽ báo ở đây khi hạn mức AI vượt "+fmt.Sprint(set.Threshold)+"%."); err != nil { // i18n-ignore
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
