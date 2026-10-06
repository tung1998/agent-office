package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tung1998/agent-office/internal/audit"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/storage"
)

func (s *server) opsRoutes(mux *http.ServeMux, auth, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/projects/{id}/ops/detect", admin(s.opsDetect))
	mux.Handle("GET /api/projects/{id}/processes", auth(s.listProcesses))
	mux.Handle("POST /api/projects/{id}/processes", admin(s.createProcess))
	mux.Handle("PATCH /api/processes/{id}", admin(s.updateProcess))
	mux.Handle("DELETE /api/processes/{id}", admin(s.deleteProcess))
	mux.Handle("POST /api/processes/{id}/start", admin(s.processAction))
	mux.Handle("POST /api/processes/{id}/stop", admin(s.processAction))
	mux.Handle("POST /api/processes/{id}/restart", admin(s.processAction))
	mux.Handle("GET /api/processes/{id}/stream", auth(s.streamProcess))
	mux.Handle("POST /api/processes/{id}/log-attachment", auth(s.processLogAttachment))
	mux.Handle("GET /api/projects/{id}/compose", auth(s.getCompose))
	mux.Handle("POST /api/projects/{id}/compose/action", admin(s.composeAction))
	mux.Handle("GET /api/projects/{id}/compose/action/stream", auth(s.streamComposeAction))
	mux.Handle("GET /api/projects/{id}/compose/logs", auth(s.streamComposeLogs))
	mux.Handle("POST /api/projects/{id}/compose/log-attachment", auth(s.composeLogAttachment))
}

type processDTO struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Name        string    `json:"name"`
	Command     string    `json:"command"`
	Cwd         string    `json:"cwd"`
	Kind        string    `json:"kind"`
	Source      string    `json:"source"`
	Autostart   bool      `json:"autostart"`
	Autorestart bool      `json:"autorestart"`
	State       ops.State `json:"state"`
}

func (s *server) toProcessDTO(p storage.Process) processDTO {
	return processDTO{ID: p.ID, ProjectID: p.ProjectID, Name: p.Name, Command: p.Command, Cwd: p.Cwd, Kind: p.Kind, Source: p.Source,
		Autostart: p.Autostart, Autorestart: p.Autorestart, State: s.cfg.Ops.State(p.ID)}
}

func (s *server) opsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ops.ErrRunning), errors.Is(err, storage.ErrConflict):
		msg := err.Error()
		if errors.Is(err, storage.ErrConflict) {
			msg = "đã có tiến trình cùng tên trong project"
		}
		writeError(w, http.StatusConflict, msg)
	case errors.Is(err, ops.ErrNoFolder), errors.Is(err, ops.ErrBadCwd):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.writeDomainError(w, r, err)
	}
}

func (s *server) opsDetect(w http.ResponseWriter, r *http.Request) {
	p, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if p.Path == "" {
		writeError(w, http.StatusBadRequest, ops.ErrNoFolder.Error())
		return
	}
	writeJSON(w, http.StatusOK, ops.Detect(p.Path))
}

func (s *server) listProcesses(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.Processes().List(r.Context(), r.PathValue("id"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]processDTO, 0, len(list))
	for _, p := range list {
		out = append(out, s.toProcessDTO(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"processes": out})
}

type processInput struct {
	Name        *string `json:"name"`
	Command     *string `json:"command"`
	Cwd         *string `json:"cwd"`
	Kind        *string `json:"kind"`
	Source      *string `json:"source"`
	Autostart   *bool   `json:"autostart"`
	Autorestart *bool   `json:"autorestart"`
}

func (in processInput) apply(p *storage.Process) error {
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.Command != nil {
		p.Command = strings.TrimSpace(*in.Command)
	}
	if in.Cwd != nil {
		p.Cwd = filepath.Clean(strings.TrimSpace(*in.Cwd))
	}
	if in.Kind != nil {
		p.Kind = *in.Kind
	}
	if in.Source != nil {
		p.Source = *in.Source
	}
	if in.Autostart != nil {
		p.Autostart = *in.Autostart
	}
	if in.Autorestart != nil {
		p.Autorestart = *in.Autorestart
	}
	switch {
	case p.Name == "" || len(p.Name) > 80:
		return errors.New("tên từ 1 đến 80 ký tự")
	case p.Command == "" || len(p.Command) > 2000:
		return errors.New("lệnh không được trống (tối đa 2000 ký tự)")
	case p.Kind != "service" && p.Kind != "job":
		return errors.New("loại phải là service hoặc job")
	case filepath.IsAbs(p.Cwd) || p.Cwd == ".." || strings.HasPrefix(p.Cwd, "../"):
		return ops.ErrBadCwd
	}
	if p.Cwd == "" {
		p.Cwd = "."
	}
	return nil
}

func (s *server) createProcess(w http.ResponseWriter, r *http.Request) {
	var in processInput
	if !decode(w, r, &in) {
		return
	}
	project, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if project.Path == "" {
		writeError(w, http.StatusBadRequest, ops.ErrNoFolder.Error())
		return
	}
	p := storage.Process{ProjectID: project.ID, Kind: "service", Cwd: "."}
	if err := in.apply(&p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err = s.cfg.Store.Processes().Create(r.Context(), p)
	if err != nil {
		s.opsError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "process.create", ResourceID: p.ID, ProjectID: p.ProjectID, After: s.toProcessDTO(p),
		Detail: map[string]any{"name": p.Name, "command": p.Command}})
	writeJSON(w, http.StatusCreated, s.toProcessDTO(p))
}

func (s *server) updateProcess(w http.ResponseWriter, r *http.Request) {
	var in processInput
	if !decode(w, r, &in) {
		return
	}
	p, err := s.cfg.Store.Processes().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	old := p
	if err := in.apply(&p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.cfg.Store.Processes().Update(r.Context(), p); err != nil {
		s.opsError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "process.update", ResourceID: p.ID, ProjectID: p.ProjectID, Before: s.toProcessDTO(old), After: s.toProcessDTO(p),
		Detail: map[string]any{"name": p.Name, "command": p.Command}})
	writeJSON(w, http.StatusOK, s.toProcessDTO(p))
}

func (s *server) deleteProcess(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	old, err := s.cfg.Store.Processes().Get(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Store.Processes().Delete(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.cfg.Ops.Forget(id)
	s.audit(r, audit.Change{Action: "process.delete", ResourceID: id, ProjectID: old.ProjectID, Before: s.toProcessDTO(old)})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) processAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	var err error
	switch action {
	case "start":
		err = s.cfg.Ops.Start(r.Context(), id)
	case "stop":
		err = s.cfg.Ops.Stop(id)
	case "restart":
		err = s.cfg.Ops.Restart(r.Context(), id)
	}
	if err != nil {
		s.opsError(w, r, err)
		return
	}
	s.auditAction(r, "process."+action, id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"state": s.cfg.Ops.State(id)})
}

// streamProcess sends output lines and state changes as SSE; ?from=seq replays.
func (s *server) streamProcess(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.cfg.Store.Processes().Get(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.streamLines(w, r, id)
}

func (s *server) streamLines(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	seq, _ := strconv.Atoi(r.URL.Query().Get("from"))
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		if n, err := strconv.Atoi(last); err == nil {
			seq = n
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	tick := time.NewTicker(3 * time.Second) // state (CPU/RAM) refresh + keepalive
	defer tick.Stop()
	var lastState []byte
	for {
		lines, wake, state := s.cfg.Ops.Since(id, seq)
		if len(lines) > 0 {
			raw, _ := json.Marshal(lines)
			seq = lines[len(lines)-1].Seq
			fmt.Fprintf(w, "id: %d\nevent: lines\ndata: %s\n\n", seq, raw)
		}
		if raw, _ := json.Marshal(state); string(raw) != string(lastState) {
			lastState = raw
			fmt.Fprintf(w, "event: state\ndata: %s\n\n", raw)
		}
		flusher.Flush()
		select {
		case <-wake:
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
		case <-r.Context().Done():
			return
		}
	}
}

// processLogAttachment saves the last lines as a text attachment so the
// person can hand them to an agent in Chat.
func (s *server) processLogAttachment(w http.ResponseWriter, r *http.Request) {
	p, err := s.cfg.Store.Processes().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	tail := s.cfg.Ops.Tail(p.ID, 300)
	if strings.TrimSpace(tail) == "" {
		writeError(w, http.StatusBadRequest, "chưa có log")
		return
	}
	st := s.cfg.Ops.State(p.ID)
	header := fmt.Sprintf("Tiến trình: %s\nLệnh: %s (trong %s)\nTrạng thái: %s", p.Name, p.Command, p.Cwd, st.Status)
	if st.ExitCode != nil {
		header += fmt.Sprintf(", mã thoát %d", *st.ExitCode)
	}
	name := strings.NewReplacer(" ", "-", ":", "-", "/", "-").Replace(p.Name) + ".log"
	m, err := s.cfg.Chat.Attachments().Save(p.ProjectID, userFrom(r).Email, name, []byte(header+"\n\n"+tail))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m.Attachment)
}

// ---- docker compose ----

func (s *server) composeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ops.ErrNoCompose), errors.Is(err, ops.ErrUnknownFile), errors.Is(err, ops.ErrUnknownSvc),
		errors.Is(err, ops.ErrBadAction), errors.Is(err, ops.ErrDockerMissing), errors.Is(err, ops.ErrNoComposeCLI):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.As(err, new(*ops.DockerError)):
		writeError(w, http.StatusBadGateway, "docker: "+err.Error())
	default:
		s.opsError(w, r, err)
	}
}

func (s *server) getCompose(w http.ResponseWriter, r *http.Request) {
	v, err := s.cfg.Ops.Compose(r.Context(), r.PathValue("id"), r.URL.Query().Get("file"), r.URL.Query().Get("stats") == "1")
	if err != nil && !errors.Is(err, ops.ErrNoCompose) {
		s.composeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) composeAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File    string `json:"file"`
		Action  string `json:"action"`
		Service string `json:"service"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.cfg.Ops.ComposeAction(r.Context(), r.PathValue("id"), in.File, in.Action, in.Service); err != nil {
		if errors.Is(err, ops.ErrRunning) {
			writeError(w, http.StatusConflict, "đang chạy một thao tác docker khác, đợi xong đã")
			return
		}
		s.composeError(w, r, err)
		return
	}
	s.auditAction(r, "compose."+in.Action, r.PathValue("id"), map[string]any{"file": in.File, "service": in.Service})
	writeJSON(w, http.StatusAccepted, map[string]any{"state": s.cfg.Ops.State(ops.ActionKey(r.PathValue("id")))})
}

// streamComposeAction streams the output of the running/last compose action.
func (s *server) streamComposeAction(w http.ResponseWriter, r *http.Request) {
	s.streamLines(w, r, ops.ActionKey(r.PathValue("id")))
}

// streamComposeLogs follows a service's container logs as SSE `lines` events.
func (s *server) streamComposeLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	var mu sync.Mutex
	seq := 0
	send := func(stream, text string) {
		mu.Lock()
		defer mu.Unlock()
		seq++
		raw, _ := json.Marshal([]ops.Line{{Seq: seq, Text: text, Stream: stream, Time: time.Now().UTC()}})
		fmt.Fprintf(w, "event: lines\ndata: %s\n\n", raw)
		flusher.Flush()
	}
	ctx := r.Context()
	go func() { // keepalive
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				mu.Lock()
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
				mu.Unlock()
			}
		}
	}()
	q := r.URL.Query()
	err := s.cfg.Ops.ComposeLogs(ctx, r.PathValue("id"), q.Get("file"), q.Get("service"), func(l string) { send("out", l) })
	if err != nil {
		send("sys", err.Error())
	}
	<-ctx.Done() // stay open so the browser does not reconnect in a loop
}

// composeLogAttachment saves a service's recent logs as a text attachment.
func (s *server) composeLogAttachment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File    string `json:"file"`
		Service string `json:"service"`
	}
	if !decode(w, r, &in) {
		return
	}
	tail, err := s.cfg.Ops.ComposeTail(r.Context(), r.PathValue("id"), in.File, in.Service, 300)
	if err != nil {
		s.composeError(w, r, err)
		return
	}
	if strings.TrimSpace(tail) == "" {
		writeError(w, http.StatusBadRequest, "chưa có log")
		return
	}
	body := fmt.Sprintf("Service docker compose: %s (file %s)\n\n%s", in.Service, in.File, tail)
	m, err := s.cfg.Chat.Attachments().Save(r.PathValue("id"), userFrom(r).Email, in.Service+"-container.log", []byte(body))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, m.Attachment)
}
