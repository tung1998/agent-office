package api

import (
	"net/http"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
)

// providerLimits lists the latest usage windows each connection reported
// (Claude Code subscriptions: 5 hours, the week, per model).
func (s *server) providerLimits(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.Providers().List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	type dto struct {
		ProviderID   string                      `json:"provider_id"`
		ProviderName string                      `json:"provider_name"`
		IsDefault    bool                        `json:"is_default"`
		Status       string                      `json:"status"`
		Windows      map[string]chat.LimitWindow `json:"windows"`
		UpdatedAt    time.Time                   `json:"updated_at"`
	}
	out := []dto{}
	for _, p := range list {
		var l chat.Limits
		if ok, err := s.cfg.Store.Settings().Get(r.Context(), chat.LimitsKey(p.ID), &l); err != nil || !ok || len(l.Windows) == 0 {
			continue
		}
		out = append(out, dto{p.ID, p.Name, p.IsDefault, l.Status, l.Windows, l.UpdatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"limits": out})
}
