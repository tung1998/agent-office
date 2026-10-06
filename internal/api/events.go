package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tung1998/agent-office/internal/events"
	"github.com/tung1998/agent-office/internal/storage"
)

// events streams what changed (ADR-072) to an open dashboard page: the
// tables written, so it refreshes what it shows, and the data the server
// pushes as it changes (the machine's figures, ADR-078). SSE, one per tab;
// its first event says its id, for the topics it turns on.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok || s.cfg.Events == nil {
		writeError(w, http.StatusNotImplemented, "không có luồng sự kiện")
		return
	}
	u := userFrom(r)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	sub, stop := s.cfg.Events.Subscribe(events.Viewer{UserID: u.ID, Email: u.Email, Admin: u.Role == storage.RoleAdmin})
	defer stop()
	write := func(name string, data any) {
		raw, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, raw)
	}
	write("hello", map[string]any{"sid": sub.ID})
	flusher.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case ev := <-sub.C:
			write(ev.Name, ev.Data)
		}
		flusher.Flush()
	}
}

// eventTopic turns a topic on or off for the caller's open page (the Máy
// tab's full figures: "machine").
func (s *server) eventTopic(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SID   int    `json:"sid"`
		Topic string `json:"topic"`
		On    bool   `json:"on"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Topic != "machine" || s.cfg.Events == nil || !s.cfg.Events.SetTopic(in.SID, userFrom(r).ID, in.Topic, in.On) {
		writeError(w, http.StatusNotFound, "Không có luồng sự kiện này.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
