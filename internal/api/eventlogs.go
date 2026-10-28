package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/events"
	"github.com/tung1998/agent-office/internal/ops"
)

// eventLog follows a console on the caller's open page's stream ({sid, key,
// source, id, file?, service?, from?, on}), as "log" events {key, lines},
// instead of an SSE of its own per console (ADR-158): a browser keeps about
// six connections to office, and a board of consoles used them all, so
// consoles and every request after them waited. source: process (a command's
// output, id = process), action (the running compose action, id = project),
// compose (a service's container logs, id = project). from: the last line
// seen (process, action), to go on after a reconnect. key: the page's own
// name for the console; asking again with it replaces the follow.
func (s *server) eventLog(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SID     int    `json:"sid"`
		Key     string `json:"key"`
		Source  string `json:"source"`
		ID      string `json:"id"`
		File    string `json:"file"`
		Service string `json:"service"`
		From    int    `json:"from"`
		On      bool   `json:"on"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Key == "" {
		writeError(w, http.StatusBadRequest, "thiếu key")
		return
	}
	follow := fmt.Sprintf("%d:%s", in.SID, in.Key)
	if !in.On {
		if old, ok := s.logFollows.LoadAndDelete(follow); ok {
			close(old.(chan struct{}))
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// a bad ask (unknown source or console, another page's stream) leaves the
	// follow it would replace running: it is only replaced below, once valid
	if s.cfg.Ops == nil {
		writeError(w, http.StatusNotImplemented, "chạy tiến trình đang tắt")
		return
	}
	switch in.Source {
	case "process":
		p, err := s.cfg.Store.Processes().Get(r.Context(), in.ID)
		if err != nil {
			s.writeDomainError(w, r, err)
			return
		}
		if !s.sees(r, p.ProjectID) {
			writeError(w, http.StatusNotFound, "không tìm thấy project")
			return
		}
	case "action", "compose":
		if _, err := s.cfg.Store.Repos().Get(r.Context(), in.ID); err != nil {
			s.writeDomainError(w, r, err)
			return
		}
		if !s.sees(r, in.ID) {
			writeError(w, http.StatusNotFound, "không tìm thấy project")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "source không hợp lệ")
		return
	}
	var sub *events.Sub
	ok := false
	if s.cfg.Events != nil {
		sub, ok = s.cfg.Events.Get(in.SID, userFrom(r).ID)
	}
	if !ok {
		writeError(w, http.StatusNotFound, "Không có luồng sự kiện này.")
		return
	}
	stop := make(chan struct{})
	if prev, loaded := s.logFollows.Swap(follow, stop); loaded { // a second ask at once: the last wins
		close(prev.(chan struct{}))
	}
	send := func(lines []ops.Line) bool {
		return s.cfg.Events.SendWait(sub, events.Event{Name: "log", Data: map[string]any{"key": in.Key, "lines": lines}}, stop)
	}
	go func() {
		defer s.logFollows.CompareAndDelete(follow, stop)
		if in.Source == "compose" {
			s.followComposeLogs(sub, stop, in.ID, in.File, in.Service, send)
			return
		}
		id := in.ID
		if in.Source == "action" {
			id = ops.ActionKey(in.ID)
		}
		seq := in.From
		// office restarted since the page saw line `from` (lines live in
		// memory, numbered again from 1): send what there is from the start
		if all, _, _ := s.cfg.Ops.Since(id, 0); len(all) == 0 || all[len(all)-1].Seq < seq {
			seq = 0
		}
		for {
			lines, wake, _ := s.cfg.Ops.Since(id, seq)
			if len(lines) > 0 {
				if !send(lines) {
					return
				}
				seq = lines[len(lines)-1].Seq
			}
			select {
			case <-wake:
			case <-stop:
				return
			case <-sub.Gone():
				return
			}
		}
	}()
	w.WriteHeader(http.StatusNoContent)
}

// followComposeLogs runs `docker compose logs --follow` for one service until
// its logs end or the page stops following, its lines numbered from 1 (a new
// follow starts over: the page clears what it had).
func (s *server) followComposeLogs(sub *events.Sub, stop <-chan struct{}, projectID, file, service string, send func([]ops.Line) bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-stop:
		case <-sub.Gone():
		case <-ctx.Done(): // the logs ended on their own (container gone, docker error)
		}
		cancel()
	}()
	var mu sync.Mutex
	seq := 0
	emit := func(stream, text string) {
		mu.Lock()
		defer mu.Unlock()
		seq++
		if !send([]ops.Line{{Seq: seq, Text: text, Stream: stream, Time: time.Now().UTC()}}) {
			cancel()
		}
	}
	// returns when the logs end or the follow stops: nothing is left waiting
	// (the page keeps what it has; a new stream's hello asks again)
	if err := s.cfg.Ops.ComposeLogs(ctx, projectID, file, service, func(l string) { emit("out", l) }); err != nil && ctx.Err() == nil {
		emit("sys", err.Error())
	}
}
