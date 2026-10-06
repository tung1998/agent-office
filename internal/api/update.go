package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/selfupdate"
)

type buildInfo struct {
	Version  string `json:"version"`
	Revision string `json:"revision,omitempty"`
	Time     string `json:"time,omitempty"`
	Dirty    bool   `json:"dirty"`
	Subject  string `json:"subject,omitempty"` // title of the commit at Revision
}

// loadBuild reads the build once at startup, with the commit title looked up
// in the source folder ("" when there is no git or the revision is unknown).
func loadBuild(version, root string) buildInfo {
	b := currentBuild(version)
	if root != "" && b.Revision != "" {
		b.Subject = commitSubject(root, b.Revision)
	}
	return b
}

func commitSubject(root, rev string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "log", "-1", "--format=%s", rev, "--").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func currentBuild(version string) buildInfo {
	b := buildInfo{Version: version}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				b.Revision = s.Value
			case "vcs.time":
				b.Time = s.Value
			case "vcs.modified":
				b.Dirty = s.Value == "true"
			}
		}
	}
	return b
}

// updateStatus tells the dashboard whether office can update itself, what is
// running, and how the last update went.
func (s *server) updateStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"supervised": s.cfg.Supervised,
		"build":      s.build,
		"last":       selfupdate.LoadResult(s.cfg.System.HomeDir),
		"busy":       s.busyWork(),
	}
	if s.cfg.Updater != nil {
		src := s.cfg.Updater.Source()
		_, _, st := s.cfg.Updater.Since(0)
		out["source"], out["state"] = src, st
	}
	writeJSON(w, http.StatusOK, out)
}

// updateChanges compares the source with the running build. It runs git, so
// the dashboard asks only when the update page opens or on "Kiểm tra lại".
func (s *server) updateChanges(w http.ResponseWriter, r *http.Request) {
	c, err := s.cfg.Updater.Source().Changes(r.Context(), s.build.Revision)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// busyWork lists work an update restart would cut off.
func (s *server) busyWork() map[string]int {
	b := map[string]int{"chats": 0, "tasks": 0}
	if s.cfg.Chat != nil {
		b["chats"] = s.cfg.Chat.ActiveTurns()
	}
	return b
}

func (s *server) startUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Test  bool `json:"test"`
		Force bool `json:"force"`
	}
	if !decode(w, r, &in) {
		return
	}
	if b := s.busyWork(); !in.Force && (b["chats"] > 0 || b["tasks"] > 0) {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "busy", "busy": b,
			"error": fmt.Sprintf("Đang có %d lượt chat và %d Việc chạy; khởi động lại sẽ cắt ngang chúng", b["chats"], b["tasks"])})
		return
	}
	if err := s.cfg.Updater.Start(selfupdate.Options{Test: in.Test}); err != nil {
		if errors.Is(err, selfupdate.ErrBusy) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "system.update", "", map[string]any{"test": in.Test, "force": in.Force})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

// streamUpdate streams the build output and state as SSE (`lines`, `state`).
func (s *server) streamUpdate(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	seq := 0
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	var last []byte
	for {
		lines, wake, st := s.cfg.Updater.Since(seq)
		if len(lines) > 0 {
			raw, _ := json.Marshal(lines)
			seq = lines[len(lines)-1].Seq
			fmt.Fprintf(w, "event: lines\ndata: %s\n\n", raw)
		}
		if raw, _ := json.Marshal(st); string(raw) != string(last) {
			last = raw
			fmt.Fprintf(w, "event: state\ndata: %s\n\n", raw)
		}
		flusher.Flush()
		select {
		case <-wake:
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
