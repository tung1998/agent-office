package api

import (
	"net/http"
	"strconv"
	"time"
)

// conversationAdvice is what advisors told the stuck agents of a chat (ADR-262).
func (s *server) conversationAdvice(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Chat.Advice(r.Context(), r.PathValue("id"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"advice": list})
}

// adviceStats sums the advices of the last days (default 7) and the
// settings that govern them, for the overview.
func (s *server) adviceStats(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 || days > 90 {
		days = 7
	}
	st, err := s.cfg.Store.Advice().Stats(r.Context(), time.Now().UTC().AddDate(0, 0, -days))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	set, _ := s.cfg.Usage.Settings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "stats": st, "mode": set.Advisor(), "daily_usd": set.AdvisorBudget()})
}
