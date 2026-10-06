package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/tung1998/agent-office/internal/transfer"
)

func (s *server) transferExport(w http.ResponseWriter, r *http.Request) {
	b, err := s.cfg.Transfer.Export(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	now := time.Now().UTC()
	b.ExportedAt = &now
	s.auditAction(r, "config.export", "", map[string]any{"providers": len(b.Providers), "templates": len(b.Templates), "projects": len(b.Projects)})
	w.Header().Set("Content-Disposition", `attachment; filename="office-config-`+now.Format("20060102-1504")+`.json"`)
	writeJSON(w, http.StatusOK, b)
}

func (s *server) transferImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Bundle transfer.Bundle `json:"bundle"`
		DryRun bool            `json:"dry_run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "File không phải bundle config hợp lệ")
		return
	}
	res, err := s.cfg.Transfer.Import(r.Context(), in.Bundle, in.DryRun)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !in.DryRun {
		s.auditAction(r, "config.import", "", map[string]any{"changes": len(res.Changes)})
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) transferBackup(w http.ResponseWriter, r *http.Request) {
	path, err := s.cfg.Backup(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.auditAction(r, "data.backup", path, nil)
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}
