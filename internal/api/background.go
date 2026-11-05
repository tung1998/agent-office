package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// A person's own dashboard backgrounds: a library of images and videos per
// account, kept on disk (Config.BackgroundsDir/<user>.d/<version>, its meta
// beside it as <version>.json). The theme pref's bg says which one is shown
// (useTheme); turning it off keeps the file. Sent as JSON with base64 data, so
// the CSRF rule of JSON-only writes still holds.

const maxBackgroundBytes = 25 << 20

// the types a background may be, as the bytes say (not as the client claims)
var backgroundTypes = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true,
	"video/mp4": true, "video/webm": true,
}

type backgroundMeta struct {
	Mime    string `json:"mime"`
	Size    int64  `json:"size"`
	Version int64  `json:"version"` // the file's id in the library and the dashboard's cache key
}

// the account's library folder, with the single background of before moved in
func (s *server) backgroundDir(r *http.Request) (string, bool) {
	if s.cfg.BackgroundsDir == "" {
		return "", false
	}
	id := userFrom(r).ID
	dir := filepath.Join(s.cfg.BackgroundsDir, id+".d")
	oldData, oldMeta := filepath.Join(s.cfg.BackgroundsDir, id), filepath.Join(s.cfg.BackgroundsDir, id+".json")
	if m, err := readBackgroundMeta(oldMeta); err == nil && os.MkdirAll(dir, 0o700) == nil {
		m.Version = backgroundVersion(m.Version)
		name := filepath.Join(dir, strconv.FormatInt(m.Version, 10))
		if os.Rename(oldData, name) == nil {
			b, _ := json.Marshal(m)
			if os.WriteFile(name+".json", b, 0o600) == nil {
				_ = os.Remove(oldMeta)
			}
		}
	}
	return dir, true
}

// A version is the upload's time in milliseconds, exact as a JSON number. The
// single background of before had nanoseconds: the theme pref may still hold
// one, so it reads as its millisecond.
func backgroundVersion(v int64) int64 {
	if v > 1e15 {
		return v / 1e6
	}
	return v
}

func readBackgroundMeta(path string) (backgroundMeta, error) {
	var m backgroundMeta
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(raw, &m)
}

// the library, newest first
func listBackgrounds(dir string) []backgroundMeta {
	out := []backgroundMeta{}
	names, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, n := range names {
		if m, err := readBackgroundMeta(n); err == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out
}

// ?v=<version>: one file of the library; none given, the newest
func (s *server) backgroundFile(r *http.Request) (data string, m backgroundMeta, ok bool) {
	dir, ok := s.backgroundDir(r)
	if !ok {
		return "", m, false
	}
	if q := r.URL.Query().Get("v"); q != "" {
		v, err := strconv.ParseInt(q, 10, 64)
		if err != nil {
			return "", m, false
		}
		name := filepath.Join(dir, strconv.FormatInt(backgroundVersion(v), 10))
		m, err = readBackgroundMeta(name + ".json")
		return name, m, err == nil
	}
	all := listBackgrounds(dir)
	if len(all) == 0 {
		return "", m, false
	}
	return filepath.Join(dir, strconv.FormatInt(all[0].Version, 10)), all[0], true
}

func (s *server) listBackgrounds(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.backgroundDir(r)
	if !ok {
		writeJSON(w, http.StatusOK, []backgroundMeta{})
		return
	}
	writeJSON(w, http.StatusOK, listBackgrounds(dir))
}

func (s *server) getBackground(w http.ResponseWriter, r *http.Request) {
	data, m, ok := s.backgroundFile(r)
	if !ok {
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
	// the URL carries ?v=<version>: each file has its own URL
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	// ServeContent answers Range requests: a video seeks and loops
	http.ServeContent(w, r, "", time.UnixMilli(m.Version), f)
}

// adds a file to the library
func (s *server) setBackground(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.backgroundDir(r)
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.internal(w, r, err)
		return
	}
	m := backgroundMeta{Mime: mime, Size: int64(len(raw)), Version: time.Now().UnixMilli()}
	data := filepath.Join(dir, strconv.FormatInt(m.Version, 10))
	// the file first, then its meta: the library lists a file only once it is whole
	f, err := os.OpenFile(data, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	for err != nil && errors.Is(err, os.ErrExist) { // two in the same millisecond
		m.Version++
		data = filepath.Join(dir, strconv.FormatInt(m.Version, 10))
		f, err = os.OpenFile(data, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	_, err = f.Write(raw)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(data)
		s.internal(w, r, err)
		return
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(data+".json", b, 0o600); err != nil {
		_ = os.Remove(data)
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// removes one file of the library (?v=<version>)
func (s *server) deleteBackground(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("v") == "" {
		writeError(w, http.StatusBadRequest, "v is required")
		return
	}
	data, _, ok := s.backgroundFile(r)
	if ok {
		for _, p := range []string{data + ".json", data} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.internal(w, r, err)
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
