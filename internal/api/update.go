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

	"github.com/tung1998/agent-office/internal/busywork"
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
// running, how far the running build is behind its source, and how the last
// update went.
func (s *server) updateStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"supervised": s.cfg.Supervised,
		"build":      s.build,
		"last":       selfupdate.LoadResult(s.cfg.System.HomeDir),
		"busy":       s.busyWork(r.Context()),
		"auto":       s.autoUpdate(r.Context()),
		"behind":     -1,
	}
	if s.cfg.Updater != nil {
		src := s.cfg.Updater.Source()
		out["source"], out["state"] = src, s.cfg.Updater.State()
		out["behind"] = src.Behind(r.Context(), s.build.Revision)
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

// busyWork lists the work a restart would cut off (ADR-255): answers being
// written (they go on after it), workflows, automation runs, Burn pieces and
// tasks (they end as failed). Project processes run on.
func (s *server) busyWork(ctx context.Context) busywork.Report {
	c := busywork.Counter{Store: s.cfg.Store}
	if s.cfg.Chat != nil {
		c.Chat = s.cfg.Chat
	}
	return c.Count(ctx)
}

// autoUpdate reads the "update on its own when idle" switch.
func (s *server) autoUpdate(ctx context.Context) selfupdate.AutoSetting {
	var a selfupdate.AutoSetting
	if s.cfg.Store != nil {
		_, _ = s.cfg.Store.Settings().Get(ctx, selfupdate.AutoSettingKey, &a)
	}
	return a
}

func (s *server) startUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Test  bool `json:"test"`
		Pull  bool `json:"pull"` // bring in the remote's new commits first
		Force bool `json:"force"`
		// WhenIdle: build now, restart only once nothing runs (ADR-255)
		WhenIdle bool `json:"when_idle"`
		// MaxWaitMinutes: how long it waits at most (0 = 2 hours)
		MaxWaitMinutes int `json:"max_wait_minutes"`
	}
	if !decode(w, r, &in) {
		return
	}
	// a built update waiting for idle: "now" swaps it in at once
	if in.Force && s.cfg.Updater.Waiting() {
		if err := s.cfg.Updater.Hurry(); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.auditAction(r, "system.update", "", map[string]any{"hurry": true})
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
		return
	}
	if !in.WhenIdle && !in.Force {
		if b := s.busyWork(r.Context()); b.Total() > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"code": "busy", "busy": b,
				"error": "Đang chạy: " + b.Summary() + ". Khởi động lại sẽ ngắt chúng (lượt chat tự làm tiếp sau đó; quy trình, tự động hóa và Việc thì dừng lỗi). Chọn \"Cập nhật khi rảnh\" để chờ chúng xong."})
			return
		}
	}
	if in.MaxWaitMinutes < 0 || time.Duration(in.MaxWaitMinutes)*time.Minute > selfupdate.MaxMaxWait {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("max_wait_minutes: từ 0 tới %d", int(selfupdate.MaxMaxWait/time.Minute)))
		return
	}
	o := selfupdate.Options{Test: in.Test, Pull: in.Pull, WhenIdle: in.WhenIdle,
		MaxWait: time.Duration(in.MaxWaitMinutes) * time.Minute, By: userFrom(r).Email}
	if err := s.cfg.Updater.Start(o); err != nil {
		if errors.Is(err, selfupdate.ErrBusy) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "system.update", "", map[string]any{"test": in.Test, "pull": in.Pull, "force": in.Force, "when_idle": in.WhenIdle})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

// cancelUpdate drops the update in progress (waiting for idle, or still
// building): the running build stays.
func (s *server) cancelUpdate(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Updater.Cancel(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.auditAction(r, "system.update.cancel", "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// setAutoUpdate turns updating on its own when idle on or off (ADR-255).
func (s *server) setAutoUpdate(w http.ResponseWriter, r *http.Request) {
	var in selfupdate.AutoSetting
	if !decode(w, r, &in) {
		return
	}
	if err := s.cfg.Store.Settings().Set(r.Context(), selfupdate.AutoSettingKey, in); err != nil {
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "system.update.auto", "", map[string]any{"enabled": in.Enabled})
	writeJSON(w, http.StatusOK, in)
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
