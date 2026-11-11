package api

import "net/http"

// listSessions: the signed-in devices of the caller's account (ADR-272).
func (s *server) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Auth.ListSessions(r.Context(), userFrom(r).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	cur := sessionFrom(r).ID
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, map[string]any{
			"id": x.ID, "created_at": x.CreatedAt, "last_seen_at": x.LastSeenAt, "expires_at": x.ExpiresAt,
			"user_agent": x.UserAgent, "ip": x.IP, "current": x.ID == cur, "impersonated": x.ImpersonatorID != "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// revokeSession signs one device of the caller's account out.
func (s *server) revokeSession(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if err := s.cfg.Auth.RevokeSession(r.Context(), u.ID, r.PathValue("id"), "human:"+u.Email); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
