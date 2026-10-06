package api

import (
	"net/http"
	"time"

	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/storage"
)

type revisionDTO struct {
	ID         string    `json:"id"`
	OrgModelID string    `json:"org_model_id"`
	Action     string    `json:"action"`
	Actor      string    `json:"actor"`
	AgentCount int       `json:"agent_count"`
	CreatedAt  time.Time `json:"created_at"`
}

func toRevisionDTO(r storage.Revision) revisionDTO {
	return revisionDTO{ID: r.ID, OrgModelID: r.OrgModelID, Action: r.Action, Actor: r.Actor, AgentCount: r.AgentCount, CreatedAt: r.CreatedAt}
}

func (s *server) listRevisions(w http.ResponseWriter, r *http.Request) {
	revs, err := s.cfg.Org.Revisions(r.Context(), r.PathValue("id"), orgmodel.KeepRevisions)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]revisionDTO, 0, len(revs))
	for _, x := range revs {
		out = append(out, toRevisionDTO(x))
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": out})
}

func (s *server) getRevision(w http.ResponseWriter, r *http.Request) {
	rev, err := s.cfg.Store.Revisions().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	t, err := orgmodel.RevisionTemplate(rev)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": toRevisionDTO(rev), "snapshot": t})
}

func (s *server) restoreRevision(w http.ResponseWriter, r *http.Request) {
	m, err := s.cfg.Org.Restore(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.auditAction(r, "org_model.restore", m.ID, map[string]any{"revision": r.PathValue("id")})
	d, _ := s.loadOrg(r, m.ID, true)
	writeJSON(w, http.StatusOK, map[string]any{"model": d})
}
