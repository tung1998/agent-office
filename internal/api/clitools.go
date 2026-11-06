package api

import (
	"cmp"
	"errors"
	"net/http"
	"slices"

	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/limitalert"
)

// cliTools reports whether the dashboard may install/sign in CLIs, and their state.
func (s *server) cliTools(w http.ResponseWriter, r *http.Request) {
	if s.cfg.CLITools == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "tools": []any{}})
		return
	}
	// alert_chat: a chat is picked for alerts; with none, new versions and
	// broken installs cannot be told (ADR-259) and the card says so
	al := limitalert.Load(r.Context(), s.cfg.Store)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "tools": s.cfg.CLITools.List(r.Context()), "auto_update": s.cliAuto(r),
		"alert_chat": al.ChannelID != "" && al.ChatID != ""})
}

// cliAuto is how office keeps the CLIs up to date (ADR-259), with the
// versions put back but without what it already told or reinstalled.
func (s *server) cliAuto(r *http.Request) clitools.AutoSetting {
	var a clitools.AutoSetting
	_, _ = s.cfg.Store.Settings().Get(r.Context(), clitools.AutoUpdateKey, &a)
	a.Mode = cmp.Or(clitools.CleanMode(a.Mode), clitools.AutoNotify)
	if a.Tools == nil {
		a.Tools = map[string]string{}
	}
	a.Told, a.Repair = nil, nil
	return a
}

// cliAutoSave changes the mode, for every CLI and per CLI ("" = as every CLI).
func (s *server) cliAutoSave(w http.ResponseWriter, r *http.Request) {
	var in clitools.AutoSetting
	if !decode(w, r, &in) {
		return
	}
	var a clitools.AutoSetting
	_, _ = s.cfg.Store.Settings().Get(r.Context(), clitools.AutoUpdateKey, &a)
	if a.Mode = clitools.CleanMode(in.Mode); a.Mode == "" {
		writeError(w, http.StatusBadRequest, "chế độ phải là off, notify hoặc auto")
		return
	}
	a.Tools = map[string]string{}
	for id, m := range in.Tools {
		if m == "" {
			continue
		}
		if clitools.CleanMode(m) == "" || !slices.Contains(s.cfg.CLITools.IDs(), id) {
			writeError(w, http.StatusBadRequest, "chế độ của "+id+" không hợp lệ")
			return
		}
		a.Tools[id] = m
	}
	if err := s.cfg.Store.Settings().Set(r.Context(), clitools.AutoUpdateKey, a); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.auditAction(r, "cli.auto_update", "", map[string]any{"mode": a.Mode, "tools": a.Tools})
	writeJSON(w, http.StatusOK, s.cliAuto(r))
}

// cliHome is the config folder of the connection named by ?provider= (its
// own account, ADR-178); "" is the machine's account.
func (s *server) cliHome(r *http.Request) (string, error) {
	id := r.URL.Query().Get("provider")
	if id == "" {
		return "", nil
	}
	p, err := s.cfg.Store.Providers().Get(r.Context(), id)
	if err != nil {
		return "", err
	}
	return p.CLIHome, nil
}

func (s *server) cliTool(w http.ResponseWriter, r *http.Request) {
	home, err := s.cliHome(r)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	st, err := s.cfg.CLITools.StatusIn(r.Context(), r.PathValue("id"), home)
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
	case errors.Is(err, clitools.ErrUnknownTool), errors.Is(err, clitools.ErrUnknownMethod), errors.Is(err, clitools.ErrNotInstalled),
		errors.Is(err, clitools.ErrNoHome), errors.Is(err, clitools.ErrUnknownInstall), errors.Is(err, clitools.ErrNoLogin):
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

// cliUpdate updates a CLI (ADR-259): when idle (no run has it running), or
// now; either way new runs wait while its files change, and a version that
// does not start is put back.
func (s *server) cliUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		When string `json:"when"` // idle (default) | now
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	id, by := r.PathValue("id"), Actor(r)
	var u clitools.UpgradeView
	var err error
	if in.When == "now" {
		u, err = s.cfg.CLITools.UpdateNow(id, by)
	} else {
		u, err = s.cfg.CLITools.UpdateWhenIdle(id, by)
	}
	switch {
	case errors.Is(err, clitools.ErrBusy), errors.Is(err, clitools.ErrUpgrading):
		writeError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, clitools.ErrUnknownTool), errors.Is(err, clitools.ErrNotInstalled), errors.Is(err, clitools.ErrUnknownInstall):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditAction(r, "cli.update", id, map[string]any{"when": u.When, "in_use": u.Waiting})
	writeJSON(w, http.StatusAccepted, u)
}

// cliUpdateCancel drops an update still waiting for its runs.
func (s *server) cliUpdateCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.cfg.CLITools.CancelUpgrade(id); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.auditAction(r, "cli.update_cancel", id, nil)
	st, _ := s.cfg.CLITools.Status(r.Context(), id)
	writeJSON(w, http.StatusOK, st)
}

func (s *server) cliLogin(w http.ResponseWriter, r *http.Request) {
	home, err := s.cliHome(r)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	j, err := s.cfg.CLITools.LoginIn(r.PathValue("id"), home)
	s.cliStarted(w, r, j, err, "login")
}

func (s *server) cliLogout(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	home, err := s.cliHome(r)
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	switch err := s.cfg.CLITools.LogoutIn(r.Context(), id, home); {
	case errors.Is(err, clitools.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, clitools.ErrUnknownTool), errors.Is(err, clitools.ErrNotInstalled), errors.Is(err, clitools.ErrNoHome), errors.Is(err, clitools.ErrNoLogin):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditAction(r, "cli.logout", id, map[string]any{"provider": r.URL.Query().Get("provider")})
	st, _ := s.cfg.CLITools.StatusIn(r.Context(), id, home)
	writeJSON(w, http.StatusOK, st)
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
