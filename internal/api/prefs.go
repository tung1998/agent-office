package api

import (
	"encoding/json"
	"io"
	"net/http"
)

// A person's own dashboard settings (the watch screen's dashboards…), kept
// with the account so they follow it to any browser.

// prefKeys: the settings an account may keep; anything else is refused.
// projects: the sidebar's menu, {order: [ids], hidden: [ids], max, sort, single, groupsOff}.
var prefKeys = map[string]bool{"watch": true, "projects": true}

const maxPrefBytes = 512 << 10

func prefKey(userID, key string) string { return "user_pref/" + userID + "/" + key }

func (s *server) getPref(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !prefKeys[key] {
		writeError(w, http.StatusNotFound, "unknown setting")
		return
	}
	var v json.RawMessage
	ok, err := s.cfg.Store.Settings().Get(r.Context(), prefKey(userFrom(r).ID, key), &v)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !ok {
		v = json.RawMessage("null")
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": v})
}

func (s *server) setPref(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !prefKeys[key] {
		writeError(w, http.StatusNotFound, "unknown setting")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPrefBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "too large")
		return
	}
	var in struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &in); err != nil || len(in.Value) == 0 {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.cfg.Store.Settings().Set(r.Context(), prefKey(userFrom(r).ID, key), in.Value); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
