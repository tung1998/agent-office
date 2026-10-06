package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
)

func (s *server) automationRoutes(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/automation/scan", admin(s.autoScan))
	mux.Handle("POST /api/automation/content", admin(s.autoContent))
	mux.Handle("POST /api/automation/install", admin(s.autoInstall))
	mux.Handle("POST /api/automation/remove", admin(s.autoRemove))
	mux.Handle("POST /api/automation/save-to-library", admin(s.autoSaveToLibrary))
	mux.Handle("POST /api/automation/check", admin(s.autoCheck))
	mux.Handle("GET /api/automation/library/{kind}", admin(s.libList))
	mux.Handle("GET /api/automation/library/{kind}/{name}", admin(s.libGet))
	mux.Handle("PUT /api/automation/library/{kind}/{name}", admin(s.libSave))
	mux.Handle("DELETE /api/automation/library/{kind}/{name}", admin(s.libDelete))
	mux.Handle("GET /api/automation/mcp/catalog", admin(s.mcpCatalog))
	mux.Handle("GET /api/automation/mcp/registry", admin(s.mcpRegistry))
	mux.Handle("GET /api/automation/mcp/status", admin(s.mcpStatus))
	mux.Handle("POST /api/automation/mcp/status/check", admin(s.mcpStatusCheck))
	mux.Handle("POST /api/automation/mcp/enabled", admin(s.mcpSetEnabled))
}

// mcpSetEnabled turns a machine MCP server off or on (automation/mcptoggle.go).
func (s *server) mcpSetEnabled(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ref     automation.Ref `json:"ref"`
		Enabled bool           `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.cfg.Automation.SetMCPEnabled(r.Context(), in.Ref, in.Enabled); err != nil {
		s.autoError(w, r, err)
		return
	}
	s.auditAction(r, "automation.mcp_enabled", "mcp:"+in.Ref.Name, map[string]any{"type": in.Ref.Type, "path": in.Ref.Path,
		"project_path": in.Ref.ProjectPath, "enabled": in.Enabled, "how": automation.ToggleTypes[in.Ref.Type]})
	writeJSON(w, http.StatusOK, map[string]any{"enabled": in.Enabled})
}

// mcpStash keeps the records of the MCP servers office turned off in a
// setting, encrypted (a user server's config holds its secrets).
type mcpStash struct {
	st  storage.Store
	box *secrets.Box
}

const mcpStashKey = "mcp_disabled"

func (m mcpStash) Load(ctx context.Context) (map[string]automation.DisabledMCP, error) {
	out := map[string]automation.DisabledMCP{}
	var sealed string
	if ok, err := m.st.Settings().Get(ctx, mcpStashKey, &sealed); err != nil || !ok || sealed == "" {
		return out, err
	}
	plain, err := m.box.Open(sealed)
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal([]byte(plain), &out)
}

func (m mcpStash) Save(ctx context.Context, recs map[string]automation.DisabledMCP) error {
	raw, err := json.Marshal(recs)
	if err != nil {
		return err
	}
	sealed, err := m.box.Seal(string(raw))
	if err != nil {
		return err
	}
	return m.st.Settings().Set(ctx, mcpStashKey, sealed)
}

func validKind(k string) bool { return k == "skill" || k == "agent" || k == "mcp" }

// autoError maps automation errors to HTTP statuses.
func (s *server) autoError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, automation.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, automation.ErrExists):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "exists"})
	case errors.Is(err, automation.ErrNotAllowed), errors.Is(err, automation.ErrBadName), errors.Is(err, automation.ErrBadTarget), errors.Is(err, automation.ErrNeedsClaude):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		// install/validation messages are meant for the admin (Vietnamese, no secrets)
		s.log.Warn("automation", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *server) autoScan(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.Automation.Scan(r.Context()))
}

func (s *server) autoContent(w http.ResponseWriter, r *http.Request) {
	var ref automation.Ref
	if !decode(w, r, &ref) {
		return
	}
	it, err := s.cfg.Automation.Content(r.Context(), ref)
	if err != nil {
		s.autoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		automation.LibraryItem
		Version string `json:"version"` // what an edit is made from (ADR-072)
	}{it, versionOf(it.Files)})
}

func (s *server) autoInstall(w http.ResponseWriter, r *http.Request) {
	var req automation.InstallRequest
	if !decode(w, r, &req) {
		return
	}
	if !validKind(req.Kind) {
		writeError(w, http.StatusBadRequest, "loại không hợp lệ")
		return
	}
	if req.Version != "" && req.Edited != nil && req.Overwrite { // the editor's skill as it opened it
		if cur, err := s.cfg.Automation.Content(r.Context(), *req.Edited); err == nil && conflicted(w, req.Version, versionOf(cur.Files)) {
			return
		}
	}
	res, err := s.cfg.Automation.Install(r.Context(), req)
	switch {
	case errors.Is(err, automation.ErrUnsafeSkill):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "code": "unsafe", "findings": res.Findings})
		return
	case errors.Is(err, automation.ErrNeedsConsent):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "consent", "findings": res.Findings})
		return
	case err != nil:
		s.autoError(w, r, err)
		return
	}
	// never audit input values: they may be secrets
	s.auditAction(r, "automation.install", req.Kind+":"+req.Name, map[string]any{"scope": req.Target.Scope, "project_path": req.Target.ProjectPath, "library": req.Library, "overwrite": req.Overwrite})
	writeJSON(w, http.StatusOK, res)
}

func (s *server) autoRemove(w http.ResponseWriter, r *http.Request) {
	var ref automation.Ref
	if !decode(w, r, &ref) {
		return
	}
	trash, err := s.cfg.Automation.Remove(r.Context(), ref)
	if err != nil {
		s.autoError(w, r, err)
		return
	}
	s.auditAction(r, "automation.remove", ref.Kind+":"+ref.Name, map[string]any{"type": ref.Type, "path": ref.Path, "project_path": ref.ProjectPath, "trash": trash})
	writeJSON(w, http.StatusOK, map[string]any{"trash": trash})
}

func (s *server) autoSaveToLibrary(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ref  automation.Ref `json:"ref"`
		Name string         `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.cfg.Automation.SaveToLibrary(r.Context(), in.Ref, in.Name); err != nil {
		s.autoError(w, r, err)
		return
	}
	s.auditAction(r, "automation.library.save", in.Ref.Kind+":"+in.Ref.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) autoCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Files map[string]string `json:"files"`
	}
	if !decode(w, r, &in) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"findings": automation.CheckContent(in.Files)})
}

func (s *server) libList(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if !validKind(kind) {
		writeError(w, http.StatusBadRequest, "loại không hợp lệ")
		return
	}
	list, err := s.cfg.Automation.Library.List(kind)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *server) libGet(w http.ResponseWriter, r *http.Request) {
	it, err := s.cfg.Automation.Library.Get(r.PathValue("kind"), r.PathValue("name"))
	if err != nil {
		s.autoError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *server) libSave(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	var in struct {
		Files    map[string]string       `json:"files"`
		Template *automation.MCPTemplate `json:"template"`
	}
	if !decode(w, r, &in) {
		return
	}
	var err error
	switch kind {
	case "skill":
		if _, ok := in.Files["SKILL.md"]; !ok {
			writeError(w, http.StatusBadRequest, "skill phải có SKILL.md")
			return
		}
		if refuse, _ := automation.Verdict(automation.CheckContent(in.Files)); refuse {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": automation.ErrUnsafeSkill.Error(), "code": "unsafe", "findings": automation.CheckContent(in.Files)})
			return
		}
		err = s.cfg.Automation.Library.SaveSkill(name, in.Files)
	case "agent":
		var content string
		for _, c := range in.Files {
			content = c
		}
		if strings.TrimSpace(content) == "" {
			writeError(w, http.StatusBadRequest, "nội dung agent trống")
			return
		}
		err = s.cfg.Automation.Library.SaveAgent(name, content)
	case "mcp":
		if in.Template == nil {
			writeError(w, http.StatusBadRequest, "thiếu cấu hình MCP")
			return
		}
		t := *in.Template
		t.Name = name
		if t.Inputs == nil {
			t.Inputs = []automation.Input{}
		}
		err = s.cfg.Automation.Library.SaveMCP(t)
	default:
		writeError(w, http.StatusBadRequest, "loại không hợp lệ")
		return
	}
	if err != nil {
		s.autoError(w, r, err)
		return
	}
	s.auditAction(r, "automation.library.save", kind+":"+name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) libDelete(w http.ResponseWriter, r *http.Request) {
	kind, name := r.PathValue("kind"), r.PathValue("name")
	if err := s.cfg.Automation.Library.Delete(kind, name); err != nil {
		s.autoError(w, r, err)
		return
	}
	s.auditAction(r, "automation.library.delete", kind+":"+name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) mcpCatalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": automation.Catalog()})
}

func (s *server) mcpRegistry(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	list, err := s.cfg.Automation.Registry.Search(r.Context(), q, 30)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

// mcpPath accepts "" (machine-wide) or a registered project's folder, so the
// client cannot make the office run `claude mcp list` anywhere.
func (s *server) mcpPath(w http.ResponseWriter, r *http.Request, path string) (string, bool) {
	if s.cfg.Automation.Health == nil {
		writeError(w, http.StatusNotFound, "không có kiểm tra MCP")
		return "", false
	}
	if path == "" {
		return "", true
	}
	if s.cfg.Automation.Projects != nil {
		if _, ok := s.cfg.Automation.Projects(r.Context())[path]; ok {
			return path, true
		}
	}
	writeError(w, http.StatusBadRequest, "thư mục không phải project của office")
	return "", false
}

func (s *server) mcpStatus(w http.ResponseWriter, r *http.Request) {
	if path, ok := s.mcpPath(w, r, r.URL.Query().Get("path")); ok {
		writeJSON(w, http.StatusOK, s.cfg.Automation.Health.Get(path))
	}
}

func (s *server) mcpStatusCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	if path, ok := s.mcpPath(w, r, in.Path); ok {
		writeJSON(w, http.StatusOK, s.cfg.Automation.Health.Check(path))
	}
}
