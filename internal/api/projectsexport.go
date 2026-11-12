package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/fullexport"
	"github.com/tung1998/agent-office/internal/repos"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/transfer"
)

// Chosen projects to and from another office (ADR-276): a pack and the code
// of each, imported into this running office; a project that clashes with
// one here (same name or same git remote) is overwritten, made anew as
// "<name> 2", or skipped, as the person picks.

const projectsImportPath = "/api/transfer/projects-import"

var stageToken = regexp.MustCompile(`^[0-9a-f]{24}$`)

func (s *server) stagingDir(token string) string {
	return filepath.Join(s.exportsDir(), "import-"+token)
}

func (s *server) projectsExport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProjectIDs []string `json:"project_ids"`
		Include    []string `json:"include"`
		Passphrase string   `json:"passphrase"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !fullBusy.TryLock() {
		writeError(w, http.StatusConflict, "đang có một lần xuất/nhập, chờ nó xong")
		return
	}
	defer fullBusy.Unlock()
	include := append([]string{transfer.IncludeConfig}, in.Include...) // the agents always go: a project is nothing without them
	var refs []fullexport.ProjectRef
	for _, id := range in.ProjectIDs {
		rp, err := s.cfg.Store.Repos().Get(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "không có project "+id)
			return
		}
		refs = append(refs, fullexport.ProjectRef{ID: rp.ID, Name: rp.Name, Path: rp.Path})
	}
	if err := os.MkdirAll(s.exportsDir(), 0o700); err != nil {
		s.internal(w, r, err)
		return
	}
	name := "projects-" + time.Now().Format("20060102-150405") + ".tar.zst.age"
	path := filepath.Join(s.exportsDir(), name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	_, err = fullexport.ExportProjects(r.Context(), f, fullexport.ProjectsExportOptions{
		HomeDir: s.cfg.System.HomeDir, Projects: refs, Passphrase: in.Passphrase,
		Pack: func(id string) ([]byte, error) {
			p, err := s.cfg.Transfer.ExportPack(r.Context(), id, include, nil)
			if err != nil {
				return nil, err
			}
			return json.Marshal(p)
		},
	})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	s.audit(r, audit.Change{Action: "office.projects_export", Resource: "office", Detail: map[string]any{"projects": in.ProjectIDs}, Err: err})
	if err != nil {
		_ = os.Remove(path)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	st, _ := os.Stat(path)
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "size": st.Size()})
}

// stagedProject is what the preview tells of one project.
type stagedProject struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Remote   string `json:"remote,omitempty"`
	Conflict *struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Reason string `json:"reason"` // name | remote
	} `json:"conflict,omitempty"`
	NewName string `json:"new_name"` // the name "new" would give
}

// projectsImportPreview unpacks an uploaded archive and says which projects
// clash with this office's.
func (s *server) projectsImportPreview(w http.ResponseWriter, r *http.Request) {
	if !fullBusy.TryLock() {
		writeError(w, http.StatusConflict, "đang có một lần xuất/nhập, chờ nó xong")
		return
	}
	defer fullBusy.Unlock()
	s.dropOldStages()
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	st, err := fullexport.StageProjects(r.Context(), r.Body, r.Header.Get(passphraseHeader), s.stagingDir(token))
	if err != nil {
		_ = os.RemoveAll(s.stagingDir(token))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.cfg.Store.Repos().List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := previewProjects(st.Manifest.Projects, list)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "exported_at": st.Manifest.ExportedAt, "projects": out})
}

// previewProjects marks the clashes with existing and the name a new copy gets.
func previewProjects(in []fullexport.Project, existing []storage.Repo) []stagedProject {
	names := map[string]bool{}
	for _, r := range existing {
		names[strings.ToLower(r.Name)] = true
	}
	out := make([]stagedProject, 0, len(in))
	for _, p := range in {
		sp := stagedProject{Slug: p.Slug, Name: p.Name, Kind: p.Kind, Remote: p.Remote}
		for _, r := range existing {
			reason := ""
			switch {
			case strings.EqualFold(r.Name, p.Name):
				reason = "name"
			case p.Remote != "" && sameRemote(r.GitRemote, p.Remote):
				reason = "remote"
			}
			if reason != "" {
				sp.Conflict = &struct {
					ID     string `json:"id"`
					Name   string `json:"name"`
					Reason string `json:"reason"`
				}{r.ID, r.Name, reason}
				break
			}
		}
		sp.NewName = p.Name
		if names[strings.ToLower(p.Name)] {
			sp.NewName = numbered(p.Name, func(n string) bool { return names[strings.ToLower(n)] })
		}
		names[strings.ToLower(sp.NewName)] = true // two of the archive must not get the same name either
		out = append(out, sp)
	}
	return out
}

// numbered: "<name> 2", "<name> 3"… the first one taken says is free.
func numbered(name string, taken func(string) bool) string {
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s %d", name, i); !taken(n) {
			return n
		}
	}
}

// sameRemote compares git remotes without scheme, user, ".git" and case
// (git@host:a/b.git is https://host/a/b).
func sameRemote(a, b string) bool {
	norm := func(u string) string {
		u = strings.ToLower(strings.TrimSpace(u))
		u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
		if i := strings.Index(u, "://"); i >= 0 {
			u = u[i+3:]
		}
		if i := strings.Index(u, "@"); i >= 0 {
			u = u[i+1:]
		}
		return strings.Replace(u, ":", "/", 1)
	}
	return a != "" && norm(a) == norm(b)
}

// projectsImportApply carries out the person's choice for each staged project.
func (s *server) projectsImportApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token       string            `json:"token"`
		Decisions   map[string]string `json:"decisions"` // slug → import | overwrite | new | skip
		ProjectsDir string            `json:"projects_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !stageToken.MatchString(in.Token) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !fullBusy.TryLock() {
		writeError(w, http.StatusConflict, "đang có một lần xuất/nhập, chờ nó xong")
		return
	}
	defer fullBusy.Unlock()
	dir := s.stagingDir(in.Token)
	st, err := fullexport.OpenStaged(dir)
	if err != nil {
		writeError(w, http.StatusNotFound, "bản xem trước không còn (quá 1 giờ?), tải file lên lại")
		return
	}
	defer os.RemoveAll(dir)
	ctx := r.Context()
	projectsDir := in.ProjectsDir
	if projectsDir == "" {
		projectsDir = filepath.Dir(s.cfg.System.ProjectRoot)
		if s.cfg.System.ProjectRoot == "" {
			projectsDir = filepath.Join(s.cfg.System.HomeDir, "projects")
		}
	}
	existing, err := s.cfg.Store.Repos().List(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	preview := previewProjects(st.Manifest.Projects, existing)
	type result struct {
		Name   string `json:"name"`
		Action string `json:"action"`
		ID     string `json:"id,omitempty"`
		Path   string `json:"path,omitempty"`
		OK     bool   `json:"ok"`
		Note   string `json:"note,omitempty"`
	}
	var results []result
	for i, p := range st.Manifest.Projects {
		pv := preview[i]
		action := in.Decisions[p.Slug]
		if action == "" || action == "import" {
			action = "new"
			if pv.Conflict != nil {
				action = "skip" // a clash never overwrites unless the person said so
			}
		}
		res := result{Name: p.Name, Action: action}
		raw, err := st.Pack(p.Slug)
		var pack transfer.Pack
		if err == nil {
			pack, err = transfer.ReadPack(raw)
		}
		switch {
		case action == "skip":
			res.OK, res.Note = true, "bỏ qua"
		case err != nil:
			res.Note = "gói project hỏng: " + err.Error()
		case action == "overwrite":
			if pv.Conflict == nil {
				res.Note = "không có project trùng để ghi đè"
				break
			}
			target, gerr := s.cfg.Store.Repos().Get(ctx, pv.Conflict.ID)
			if gerr != nil {
				res.Note = gerr.Error()
				break
			}
			if _, ierr := s.cfg.Transfer.ImportPack(ctx, pack, target, pack.Include); ierr != nil {
				res.Note = ierr.Error()
				break
			}
			res.OK, res.ID, res.Path = true, target.ID, target.Path
			res.Note = "đã thay agent, workflow, tự động hóa; thêm chat và ghi chú; code ở " + target.Path + " giữ nguyên"
		case action == "new":
			res.ID, res.Path, res.OK, res.Note = s.importNewProject(r, st, p, pack, pv.NewName, projectsDir)
		default:
			res.Note = "lựa chọn không hợp lệ: " + action
		}
		results = append(results, res)
	}
	s.audit(r, audit.Change{Action: "office.projects_import", Resource: "office", Detail: map[string]any{"decisions": in.Decisions}})
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// importNewProject makes p a new project called name: its code in a fresh
// folder under projectsDir, then its pack.
func (s *server) importNewProject(r *http.Request, st fullexport.Staged, p fullexport.Project, pack transfer.Pack, name, projectsDir string) (id, path string, ok bool, note string) {
	ctx := r.Context()
	repo := storage.Repo{Name: name, Description: pack.Project.Description, GitRemote: p.Remote}
	if p.Kind != fullexport.KindHelper {
		base := filepath.Base(p.Path)
		if base == "." || base == "/" || base == "" {
			base = transfer.DirName(name)
		}
		to := filepath.Join(projectsDir, base)
		for i := 2; exists(to); i++ {
			to = filepath.Join(projectsDir, fmt.Sprintf("%s-%d", base, i))
		}
		okCode, codeNote := st.RestoreCode(ctx, p, to)
		if !okCode && !exists(to) {
			return "", "", false, codeNote
		}
		note = codeNote
		if info, err := repos.Detect(to); err == nil {
			repo.Path, repo.GitRemote = info.Path, info.GitRemote
		} else {
			repo.Path = to
		}
	}
	created, err := s.cfg.Store.Repos().Create(ctx, repo)
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return "", repo.Path, false, "thư mục hoặc tên đã có project khác dùng"
		}
		return "", repo.Path, false, err.Error()
	}
	if _, err := s.cfg.Transfer.ImportPack(ctx, pack, created, pack.Include); err != nil {
		return created.ID, created.Path, false, "đã tạo project nhưng nhập cấu hình lỗi: " + err.Error()
	}
	return created.ID, created.Path, true, note
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// dropOldStages removes previews nobody applied within the hour.
func (s *server) dropOldStages() {
	old, _ := filepath.Glob(filepath.Join(s.exportsDir(), "import-*"))
	for _, d := range old {
		if st, err := os.Stat(d); err == nil && time.Since(st.ModTime()) > offerTTL {
			_ = os.RemoveAll(d)
		}
	}
}
