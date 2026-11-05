package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// A person's own dashboard background: one image or video per account, kept
// on disk (Config.BackgroundsDir) and shown behind the dashboard by the theme
// (useTheme, the theme pref's bg). Sent as JSON with base64 data, so the CSRF
// rule of JSON-only writes still holds.

const maxBackgroundBytes = 25 << 20

// the types a background may be, as the bytes say (not as the client claims)
var backgroundTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
	"video/mp4": true, "video/webm": true,
}

type backgroundMeta struct {
	Mime    string `json:"mime"`
	Size    int64  `json:"size"`
	Version int64  `json:"version"` // changes with each upload: the dashboard's cache key
}

func (s *server) backgroundPaths(r *http.Request) (data, meta string, ok bool) {
	if s.cfg.BackgroundsDir == "" {
		return "", "", false
	}
	id := userFrom(r).ID
	return filepath.Join(s.cfg.BackgroundsDir, id), filepath.Join(s.cfg.BackgroundsDir, id+".json"), true
}

func (s *server) readBackgroundMeta(path string) (backgroundMeta, error) {
	var m backgroundMeta
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}

func (s *server) getBackground(w http.ResponseWriter, r *http.Request) {
	data, meta, ok := s.backgroundPaths(r)
	if !ok {
		writeError(w, http.StatusNotFound, "no background")
		return
	}
	m, err := s.readBackgroundMeta(meta)
	if err != nil {
		writeError(w, http.StatusNotFound, "no background")
		return
	}
	f, err := os.Open(data)
	if err != nil {
		writeError(w, http.StatusNotFound, "no background")
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", m.Mime)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	// the URL carries ?v=<version>: a new upload is a new URL
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	// ServeContent answers Range requests: a video seeks and loops
	http.ServeContent(w, r, "", time.Unix(0, m.Version), f)
}

func (s *server) setBackground(w http.ResponseWriter, r *http.Request) {
	data, meta, ok := s.backgroundPaths(r)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "backgrounds are off")
		return
	}
	var in struct {
		Data string `json:"data"` // base64
	}
	if !decode(w, r, &in) {
		return
	}
	raw, err := base64.StdEncoding.DecodeString(in.Data)
	if err != nil || len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "invalid file data")
		return
	}
	if len(raw) > maxBackgroundBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file too large (max 25 MB)")
		return
	}
	mime := http.DetectContentType(raw)
	if !backgroundTypes[mime] {
		writeError(w, http.StatusUnsupportedMediaType, "only JPEG, PNG, WebP, GIF images or MP4, WebM videos")
		return
	}
	if err := os.MkdirAll(s.cfg.BackgroundsDir, 0o700); err != nil {
		s.internal(w, r, err)
		return
	}
	// the file first, then its meta: a reader never sees new meta with old bytes
	tmp := data + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		s.internal(w, r, err)
		return
	}
	if err := os.Rename(tmp, data); err != nil {
		_ = os.Remove(tmp)
		s.internal(w, r, err)
		return
	}
	m := backgroundMeta{Mime: mime, Size: int64(len(raw)), Version: time.Now().UnixNano()}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(meta, b, 0o600); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *server) deleteBackground(w http.ResponseWriter, r *http.Request) {
	data, meta, ok := s.backgroundPaths(r)
	if ok {
		for _, p := range []string{meta, data} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.internal(w, r, err)
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
