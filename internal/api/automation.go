package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/automation"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
)

func (s *server) automationRoutes(mux *http.ServeMux, auth, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/automation/scan", admin(s.autoScan))
	mux.Handle("POST /api/automation/content", admin(s.autoContent))
	mux.Handle("POST /api/automation/install", admin(s.autoInstall))
	mux.Handle("POST /api/automation/remove", admin(s.autoRemove))
	mux.Handle("POST /api/automation/save-to-library", admin(s.autoSaveToLibrary))
	mux.Handle("POST /api/automation/check", admin(s.autoCheck))
	mux.Handle("GET /api/automation/templates", auth(s.automationTemplateList))
	mux.Handle("PUT /api/automation/templates/{key}", admin(s.automationTemplateSave))
	mux.Handle("DELETE /api/automation/templates/{key}", admin(s.automationTemplateDelete))
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

func validKind(k string) bool { return k == "skill" || k == "agent" || k == "mcp" || k == "template" }

// automationItemVersion is versionOf applied to whatever field holds an
// automation library item's edited content: Files for skill/agent, but
// Template for mcp (Content() never populates Files for mcp items, so
// versionOf(it.Files) would hash a constant nil and never detect a change).
func automationItemVersion(it automation.LibraryItem) string {
	if it.Kind == "mcp" {
		return versionOf(it.Template)
	}
	return versionOf(it.Files)
}

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
	}{it, automationItemVersion(it)})
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
		if cur, err := s.cfg.Automation.Content(r.Context(), *req.Edited); err == nil && conflicted(w, req.Version, automationItemVersion(cur)) {
			return
		}
	}
	res, err := s.cfg.Automation.Install(r.Context(), req)
	switch {
	case errors.Is(err, automation.ErrConflict):
		writeSaveConflict(w)
		return
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
	// ?q=&area=&category=&tag=&risk=&offset=&limit=: one page of the
	// matches, the office's own (no "source") first, with counts for the
	// filters; without limit the whole list, as before
	qv := r.URL.Query()
	limit, _ := strconv.Atoi(qv.Get("limit"))
	if limit <= 0 {
		writeJSON(w, http.StatusOK, map[string]any{"items": list})
		return
	}
	page, facets := filterLibrary(list, qv)
	offset, _ := strconv.Atoi(qv.Get("offset"))
	offset = min(max(offset, 0), len(page))
	writeJSON(w, http.StatusOK, map[string]any{"items": page[offset:min(offset+limit, len(page))], "total": len(page), "facets": facets})
}

// libraryFacets counts the values of each filter among the matches of the
// other filters, so choosing one still shows how many the others hold.
type libraryFacets struct {
	Areas      map[string]int `json:"areas"`
	Categories map[string]int `json:"categories"`
	Risks      map[string]int `json:"risks"`
	Tags       []tagCount     `json:"tags"` // the most used, at most 40
}

type tagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

func filterLibrary(list []automation.LibraryItem, qv url.Values) ([]automation.LibraryItem, libraryFacets) {
	q := strings.ToLower(strings.TrimSpace(qv.Get("q")))
	want := map[string]string{"area": qv.Get("area"), "category": qv.Get("category"), "tag": strings.ToLower(qv.Get("tag")), "risk": qv.Get("risk")}
	value := func(it automation.LibraryItem, key string) string {
		switch key {
		case "area":
			return it.Area
		case "category":
			return it.Category
		case "risk":
			return it.Risk
		}
		return ""
	}
	// matches reports whether it passes every filter but skip
	matches := func(it automation.LibraryItem, skip string) bool {
		if q != "" && !strings.Contains(strings.ToLower(it.Name), q) && !strings.Contains(strings.ToLower(it.Description), q) && !slices.Contains(it.Tags, q) {
			return false
		}
		for key, v := range want {
			if v == "" || key == skip {
				continue
			}
			if key == "tag" {
				if !slices.ContainsFunc(it.Tags, func(t string) bool { return strings.ToLower(t) == v }) {
					return false
				}
			} else if value(it, key) != v {
				return false
			}
		}
		return true
	}
	f := libraryFacets{Areas: map[string]int{}, Categories: map[string]int{}, Risks: map[string]int{}, Tags: []tagCount{}}
	tags := map[string]int{}
	var out []automation.LibraryItem
	for _, it := range list {
		if matches(it, "") {
			out = append(out, it)
		}
		for key, m := range map[string]map[string]int{"area": f.Areas, "category": f.Categories, "risk": f.Risks} {
			if v := value(it, key); v != "" && matches(it, key) {
				m[v]++
			}
		}
		if matches(it, "tag") {
			for _, t := range it.Tags {
				tags[strings.ToLower(t)]++
			}
		}
	}
	for t, n := range tags {
		f.Tags = append(f.Tags, tagCount{t, n})
	}
	sort.Slice(f.Tags, func(i, j int) bool {
		if f.Tags[i].Count != f.Tags[j].Count {
			return f.Tags[i].Count > f.Tags[j].Count
		}
		return f.Tags[i].Tag < f.Tags[j].Tag
	})
	f.Tags = f.Tags[:min(len(f.Tags), 40)]
	sort.SliceStable(out, func(i, j int) bool { return out[i].Source == "" && out[j].Source != "" })
	if out == nil {
		out = []automation.LibraryItem{}
	}
	return out, f
}

func (s *server) automationTemplateList(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Automation.Library.List("template")
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]automation.AutomationPreset, 0, len(list))
	for _, item := range list {
		if item.Automation != nil {
			items = append(items, *item.Automation)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) automationTemplateSave(w http.ResponseWriter, r *http.Request) {
	var in automation.AutomationPreset
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Description) == "" || strings.TrimSpace(in.Category) == "" || strings.TrimSpace(in.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "tên, mô tả, nhóm và nội dung mẫu là bắt buộc")
		return
	}
	if in.Source != "schedule" && in.Source != "webhook" {
		writeError(w, http.StatusBadRequest, "nguồn tự động hóa không hợp lệ")
		return
	}
	if utf8.RuneCountInString(in.Name) > 120 || utf8.RuneCountInString(in.Description) > 500 || utf8.RuneCountInString(in.Category) > 80 || len(in.Prompt) > 100_000 {
		writeError(w, http.StatusBadRequest, "mẫu tự động hóa vượt quá giới hạn độ dài")
		return
	}
	key := r.PathValue("key")
	if err := s.cfg.Automation.Library.SaveAutomationPreset(key, in); err != nil {
		s.autoError(w, r, err)
		return
	}
	s.auditAction(r, "automation.template.save", key, map[string]any{"name": in.Name, "category": in.Category})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) automationTemplateDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if err := s.cfg.Automation.Library.Delete("template", key); err != nil {
		s.autoError(w, r, err)
		return
	}
	s.auditAction(r, "automation.template.delete", key, nil)
	w.WriteHeader(http.StatusNoContent)
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
		Files      map[string]string            `json:"files"`
		Template   *automation.MCPTemplate      `json:"template"`
		Automation *automation.AutomationPreset `json:"automation_template"`
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
	case "template":
		if in.Automation == nil {
			writeError(w, http.StatusBadRequest, "thiếu mẫu tự động hóa")
			return
		}
		err = s.cfg.Automation.Library.SaveAutomationPreset(name, *in.Automation)
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
