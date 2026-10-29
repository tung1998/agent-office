package api

import (
	"net/http"

	"github.com/tung1998/agent-office/internal/chat"
)

// pendingActions is the approval inbox: every proposal waiting for a person
// in the projects they see.
func (s *server) pendingActions(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.Actions().Pending(r.Context(), 100)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	sc, err := s.scope(r)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]chat.ActionDTO, 0, len(list))
	for _, a := range list {
		if !sc.has(a.ProjectID) {
			continue
		}
		out = append(out, chat.ToActionDTO(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": out})
}
