package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tung1998/agent-office/internal/auth"
)

// Passkeys (ADR-226): added on the Account page, used on the login page. The
// relying party is the origin the browser sends, already checked by csrf.

func (s *server) passkeyError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrPasskeyUnavailable):
		writeError(w, http.StatusBadRequest, "Passkey chỉ dùng được khi mở office qua HTTPS có tên miền, hoặc localhost")
	case errors.Is(err, auth.ErrPasskeyFailed):
		writeError(w, http.StatusUnauthorized, "Passkey không hợp lệ hoặc đã hết hạn, thử lại")
	case errors.Is(err, auth.ErrThrottled):
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "Đăng nhập sai quá nhiều lần, thử lại sau 15 phút")
	default:
		s.writeDomainError(w, r, err)
	}
}

func (s *server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Auth.ListPasskeys(r.Context(), userFrom(r).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "created_at": p.CreatedAt, "last_used_at": p.LastUsedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": out})
}

func (s *server) beginPasskey(w http.ResponseWriter, r *http.Request) {
	opts, state, err := s.cfg.Auth.BeginPasskeyRegistration(r.Context(), userFrom(r).ID, r.Header.Get("Origin"))
	if err != nil {
		s.passkeyError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": opts, "state": state})
}

func (s *server) addPasskey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		State      string          `json:"state"`
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := s.cfg.Auth.FinishPasskeyRegistration(r.Context(), userFrom(r).ID, r.Header.Get("Origin"), in.State, in.Name, in.Credential)
	if err != nil {
		s.passkeyError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": p.ID, "name": p.Name, "created_at": p.CreatedAt})
}

func (s *server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	if err := s.cfg.Auth.DeletePasskey(r.Context(), u.ID, r.PathValue("id"), "human:"+u.Email); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) beginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	opts, state, err := s.cfg.Auth.BeginPasskeyLogin(r.Context(), r.Header.Get("Origin"), auth.ClientMeta{IP: s.clientIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		s.passkeyError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": opts, "state": state})
}

func (s *server) passkeyLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		State      string          `json:"state"`
		Credential json.RawMessage `json:"credential"`
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := s.cfg.Auth.FinishPasskeyLogin(r.Context(), r.Header.Get("Origin"), in.State, in.Credential,
		auth.ClientMeta{IP: s.clientIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		s.passkeyError(w, r, err)
		return
	}
	s.setCookie(w, r, res.Token, s.cfg.Auth.SessionTTL())
	writeJSON(w, http.StatusOK, map[string]any{"user": toDTO(res.User)})
}
