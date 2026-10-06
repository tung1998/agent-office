package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/tung1998/agent-office/internal/attach"
)

// uploadAttachment stores a file (JSON with base64 data, so the CSRF rule of
// JSON-only writes still holds) and returns its reference.
func (s *server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Data string `json:"data"` // base64
	}
	if !decode(w, r, &in) {
		return
	}
	if _, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id")); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	data, err := base64.StdEncoding.DecodeString(in.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "dữ liệu file không hợp lệ")
		return
	}
	m, err := s.cfg.Chat.Attachments().Save(r.PathValue("id"), userFrom(r).Email, in.Name, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m.Attachment)
}

// getAttachment serves a file for previews. Nothing is rendered as a page:
// only images and PDFs keep their type, the rest is plain text, all sandboxed.
func (s *server) getAttachment(w http.ResponseWriter, r *http.Request) {
	m, p, err := s.cfg.Chat.Attachments().Get(r.PathValue("id"))
	if errors.Is(err, attach.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeError(w, http.StatusNotFound, attach.ErrNotFound.Error())
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", m.Mime)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'")
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(m.Name))
	h.Set("Content-Length", strconv.FormatInt(m.Size, 10))
	http.ServeContent(w, r, "", m.CreatedAt, f)
}
