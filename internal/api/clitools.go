package api

import (
	"errors"
	"net/http"

	"github.com/tung1998/agent-office/internal/clitools"
)

// cliTools reports whether the dashboard may install/sign in CLIs, and their state.
func (s *server) cliTools(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CLITools == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "tools": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "tools": s.cfg.CLITools.List(r.Context())})
}

func (s *server) cliTool(w http.ResponseWriter, r *http.Request) {
	st, err := s.cfg.CLITools.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) cliStarted(w http.ResponseWriter, r *http.Request, j *clitools.Job, err error, action string) {
	switch {
	case errors.Is(err, clitools.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, clitools.ErrUnknownTool), errors.Is(err, clitools.ErrUnknownMethod), errors.Is(err, clitools.ErrNotInstalled):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	v := j.Snapshot()
	s.auditAction(r, "cli."+action, v.Tool, map[string]any{"command": v.Command, "job": v.ID})
	writeJSON(w, http.StatusAccepted, v)
}

func (s *server) cliInstall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Method string `json:"method"`
	}
	if !decode(w, r, &in) {
		return
	}
	j, err := s.cfg.CLITools.Install(r.PathValue("id"), in.Method)
	s.cliStarted(w, r, j, err, "install")
}

func (s *server) cliLogin(w http.ResponseWriter, r *http.Request) {
	j, err := s.cfg.CLITools.Login(r.PathValue("id"))
	s.cliStarted(w, r, j, err, "login")
}

func (s *server) cliJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.cfg.CLITools.Job(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Không tìm thấy tác vụ")
		return
	}
	writeJSON(w, http.StatusOK, j.Snapshot())
}

func (s *server) cliJobInput(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	j, ok := s.cfg.CLITools.Job(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Không tìm thấy tác vụ")
		return
	}
	if err := j.Input(in.Text); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, j.Snapshot())
}

func (s *server) cliJobCancel(w http.ResponseWriter, r *http.Request) {
	j, ok := s.cfg.CLITools.Job(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Không tìm thấy tác vụ")
		return
	}
	j.Cancel()
	writeJSON(w, http.StatusOK, j.Snapshot())
}
