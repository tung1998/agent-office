package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/fullexport"
	"github.com/tung1998/agent-office/internal/storage"
)

// A project imported without its code, its repo not clonable here yet
// (ADR-279): the dashboard shows why, and the person clones again, with
// another URL, points to a folder they cloned, or gives up the code.
func (s *server) pendingCodeRoutes(mux *http.ServeMux, auth, admin func(http.HandlerFunc) http.Handler) {
	if s.cfg.System.HomeDir == "" {
		return
	}
	mux.Handle("GET /api/projects/{id}/pending-code", auth(s.pendingCode))
	mux.Handle("POST /api/projects/{id}/pending-code/retry", admin(s.pendingCodeRetry))
	mux.Handle("POST /api/projects/{id}/pending-code/attach", admin(s.pendingCodeAttach))
	mux.Handle("DELETE /api/projects/{id}/pending-code", admin(s.pendingCodeDrop))
}

func (s *server) pendingCode(w http.ResponseWriter, r *http.Request) {
	pc, ok, err := fullexport.LoadPending(s.cfg.System.HomeDir, r.PathValue("id"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"pending": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pending": true, "remote": pc.Project.Remote, "branch": pc.Project.Branch, "reason": pc.Reason, "message": pc.Message, "at": pc.At,
		"kept": map[string]any{"commits": pc.Project.Bundle, "changes": pc.Project.Patch, "untracked": pc.Project.Untracked},
	})
}

func (s *server) pendingCodeRetry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	repo, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "không có project")
		return
	}
	ok, note, err := fullexport.RetryPending(r.Context(), s.cfg.System.HomeDir, repo.ID, repo.Path, in.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if ok && strings.TrimSpace(in.URL) != "" { // the repo it now comes from
		_ = s.cfg.Store.Repos().SetPath(r.Context(), repo.ID, repo.Path, strings.TrimSpace(in.URL))
	}
	s.audit(r, audit.Change{Action: "project.code_clone", Resource: "project", ResourceID: repo.ID, ProjectID: repo.ID, Detail: map[string]any{"ok": ok, "url": in.URL}})
	out := map[string]any{"ok": ok, "note": note}
	if !ok {
		if pc, waiting, _ := fullexport.LoadPending(s.cfg.System.HomeDir, repo.ID); waiting {
			out["reason"], out["message"] = pc.Reason, pc.Message
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) pendingCodeAttach(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Dir) == "" {
		writeError(w, http.StatusBadRequest, "cần đường dẫn thư mục")
		return
	}
	dir := strings.TrimSpace(in.Dir)
	if strings.HasPrefix(dir, "~/") {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		writeError(w, http.StatusBadRequest, "cần đường dẫn tuyệt đối (vd. /Users/ban/code/repo)")
		return
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	repo, err := s.cfg.Store.Repos().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "không có project")
		return
	}
	note, err := fullexport.AttachPending(r.Context(), s.cfg.System.HomeDir, repo.ID, dir)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	remote := repo.GitRemote
	if out, err := exec.CommandContext(r.Context(), "git", "-C", dir, "remote", "get-url", "origin").Output(); err == nil {
		remote = strings.TrimSpace(string(out))
	}
	if err := s.cfg.Store.Repos().SetPath(r.Context(), repo.ID, dir, remote); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, http.StatusConflict, "thư mục này đã là của project khác")
			return
		}
		s.internal(w, r, err)
		return
	}
	if old := repo.Path; old != dir { // the empty folder made while it waited
		if entries, err := os.ReadDir(old); err == nil && len(entries) == 0 {
			_ = os.Remove(old)
		}
	}
	s.audit(r, audit.Change{Action: "project.code_attach", Resource: "project", ResourceID: repo.ID, ProjectID: repo.ID, Detail: map[string]any{"dir": dir}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "note": note, "path": dir})
}

func (s *server) pendingCodeDrop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := fullexport.DropPending(s.cfg.System.HomeDir, id); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "project.code_drop", Resource: "project", ResourceID: id, ProjectID: id})
	w.WriteHeader(http.StatusNoContent)
}

// probeAccess fills each staged project with a remote with whether this
// machine may clone it, a few at a time.
func probeAccess(ctx context.Context, list []stagedProject) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := range list {
		if list[i].Remote == "" || list[i].Kind != fullexport.KindRemote {
			continue
		}
		wg.Add(1)
		go func(sp *stagedProject) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			reason, msg := fullexport.ProbeRemote(ctx, sp.Remote)
			sp.Access, sp.AccessNote = "ok", ""
			if reason != "" {
				sp.Access, sp.AccessNote = reason, msg
			}
		}(&list[i])
	}
	wg.Wait()
}
