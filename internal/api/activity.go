package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/tung1998/agent-office/internal/activity"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/storage"
)

// activityOverview: how every project worked over the period (ADR-210), for
// the overview page: work boards, agents' runs and cost, setup changes,
// schedules. The office assistant's own project is left out. ?project=: that
// project and those it links to (a watch box of one project).
func (s *server) activityOverview(w http.ResponseWriter, r *http.Request) {
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 {
		hours = 24
	}
	hours = min(hours, 720)
	sc, err := s.scope(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var projects []storage.Repo
	if id := r.URL.Query().Get("project"); id != "" {
		if !sc.has(id) {
			writeError(w, http.StatusNotFound, "không tìm thấy project")
			return
		}
		for _, x := range activity.Linked(r.Context(), s.cfg.Store, id) {
			if sc.has(x.ID) {
				projects = append(projects, x)
			}
		}
	} else {
		list, err := s.cfg.Store.Repos().List(r.Context())
		if err != nil {
			s.internal(w, r, err)
			return
		}
		helper := assistant.ID(r.Context(), s.cfg.Store)
		for _, x := range list {
			if x.ID != helper && sc.has(x.ID) {
				projects = append(projects, x)
			}
		}
	}
	ov, err := activity.Load(r.Context(), s.cfg.Store, projects, time.Now().Add(-time.Duration(hours)*time.Hour))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}
