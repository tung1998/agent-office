package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/fullexport"
	"github.com/tung1998/agent-office/internal/peer"
)

// The whole office from the dashboard and through a paired office (ADR-275).
func (s *server) fullExportRoutes(mux *http.ServeMux, admin func(http.HandlerFunc) http.Handler) {
	if s.cfg.Snapshot == nil {
		return
	}
	mux.Handle("POST /api/transfer/full-export", admin(s.fullExport))
	mux.Handle("GET /api/transfer/full-export/{name}", admin(s.fullExportFile))
	mux.Handle("POST /api/transfer/full-import", admin(s.fullImport))
	mux.Handle("POST /api/transfer/projects-export", admin(s.projectsExport))        // ADR-276
	mux.Handle("POST /api/transfer/projects-import", admin(s.projectsImportPreview)) // raw body
	mux.Handle("POST /api/transfer/projects-import/apply", admin(s.projectsImportApply))
	if s.cfg.Peers != nil {
		mux.Handle("POST /api/peers/{id}/full-offer", admin(s.fullOffer))
		mux.Handle("POST /api/peers/{id}/full-pull", admin(s.fullPull))
		mux.HandleFunc("GET /api/peer/full-export", s.peerCall(s.peerFullExport))
	}
}

// fullImportPath takes a raw body (the archive), not JSON (csrf).
const fullImportPath = "/api/transfer/full-import"

// passphraseHeader carries the passphrase of an upload: a header a cross-site
// page cannot set without a preflight office never grants.
const passphraseHeader = "X-Office-Passphrase"

// offerTTL: how long another office may fetch what it was offered.
const offerTTL = time.Hour

var (
	fullBusy   sync.Mutex // one export or import at a time: each may take gigabytes
	fullOffers = struct {
		sync.Mutex
		m map[string]fullOfferEntry // peer id → its offer
	}{m: map[string]fullOfferEntry{}}
	exportName = regexp.MustCompile(`^(office|projects)-[0-9-]+\.tar\.zst\.age$`)
)

type fullOfferEntry struct {
	path    string
	expires time.Time
}

func (s *server) exportsDir() string { return filepath.Join(s.cfg.System.HomeDir, "exports") }

// buildFullExport writes the whole office to a new file in exportsDir and
// drops the files older than offerTTL there.
func (s *server) buildFullExport(ctx context.Context, passphrase string) (string, int64, error) {
	if len([]rune(passphrase)) < fullexport.MinPassphrase {
		return "", 0, fmt.Errorf("passphrase cần ít nhất %d ký tự", fullexport.MinPassphrase)
	}
	if !fullBusy.TryLock() {
		return "", 0, errors.New("đang có một lần xuất/nhập toàn bộ office, chờ nó xong")
	}
	defer fullBusy.Unlock()
	dir := s.exportsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	if old, _ := filepath.Glob(filepath.Join(dir, "office-*")); len(old) > 0 {
		for _, f := range old {
			if st, err := os.Stat(f); err == nil && time.Since(st.ModTime()) > offerTTL {
				_ = os.Remove(f)
			}
		}
	}
	repos, err := s.cfg.Store.Repos().List(ctx)
	if err != nil {
		return "", 0, err
	}
	refs := make([]fullexport.ProjectRef, 0, len(repos))
	for _, r := range repos {
		refs = append(refs, fullexport.ProjectRef{ID: r.ID, Name: r.Name, Path: r.Path})
	}
	path := filepath.Join(dir, "office-"+time.Now().Format("20060102-150405")+".tar.zst.age")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	_, err = fullexport.Export(ctx, f, fullexport.ExportOptions{
		HomeDir: s.cfg.System.HomeDir, ProjectRoot: s.cfg.System.ProjectRoot, OfficeVersion: s.cfg.Version,
		KeyPath: s.cfg.KeyPath, Snapshot: func(dest string) error { return s.cfg.Snapshot(ctx, dest) },
		Projects: refs, Passphrase: passphrase,
	})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", 0, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	return path, st.Size(), nil
}

// fullExport builds the archive; the dashboard then downloads it by name.
func (s *server) fullExport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	path, size, err := s.buildFullExport(r.Context(), in.Passphrase)
	s.audit(r, audit.Change{Action: "office.full_export", Resource: "office", Detail: map[string]any{"size": size}, Err: err})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": filepath.Base(path), "size": size, "expires_at": time.Now().Add(offerTTL)})
}

func (s *server) fullExportFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !exportName.MatchString(name) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	path := filepath.Join(s.exportsDir(), name)
	if _, err := os.Stat(path); err != nil {
		writeError(w, http.StatusNotFound, "bản xuất không còn (quá 1 giờ thì bị xoá), xuất lại")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, path)
}

// fullImport takes an archive uploaded as the raw body. The database waits
// for the office's next start (it runs on the current one).
func (s *server) fullImport(w http.ResponseWriter, r *http.Request) {
	s.runFullImport(w, r, func(ctx context.Context) (rc readCloser, err error) { return r.Body, nil },
		r.Header.Get(passphraseHeader), r.URL.Query().Get("mode"), r.URL.Query().Get("projects_dir"), "")
}

type readCloser interface {
	Read(p []byte) (int, error)
	Close() error
}

func (s *server) runFullImport(w http.ResponseWriter, r *http.Request, open func(ctx context.Context) (readCloser, error), passphrase, mode, projectsDir, from string) {
	if !fullBusy.TryLock() {
		writeError(w, http.StatusConflict, "đang có một lần xuất/nhập toàn bộ office, chờ nó xong")
		return
	}
	defer fullBusy.Unlock()
	if projectsDir == "" && s.cfg.System.ProjectRoot != "" {
		projectsDir = filepath.Dir(s.cfg.System.ProjectRoot)
	}
	body, err := open(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer body.Close()
	rep, err := fullexport.Import(r.Context(), body, fullexport.ImportOptions{
		HomeDir: s.cfg.System.HomeDir, ProjectRoot: s.cfg.System.ProjectRoot, KeyPath: s.cfg.KeyPath,
		ProjectsDir: projectsDir, Passphrase: passphrase, Mode: mode, Defer: true,
	})
	s.audit(r, audit.Change{Action: "office.full_import", Resource: "office", Detail: map[string]any{"mode": mode, "from": from, "projects": len(rep.Projects)}, Err: err})
	if err != nil {
		status := http.StatusBadRequest
		if !errors.Is(err, fullexport.ErrPassphrase) {
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, err.Error())
		return
	}
	projects := make([]map[string]any, 0, len(rep.Projects))
	for _, p := range rep.Projects {
		projects = append(projects, map[string]any{"name": p.Name, "to": p.To, "kind": p.Kind, "ok": p.OK, "note": p.Note})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exported_at": rep.Manifest.ExportedAt, "files": rep.Files, "projects": projects,
		"warnings": rep.Warnings, "pending": rep.Pending,
	})
}

// fullOffer lets paired office {id} fetch the whole office once, within the
// hour. Only an office given Admin here: what it gets opens every key.
func (s *server) fullOffer(w http.ResponseWriter, r *http.Request) {
	p, err := s.cfg.Peers.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "office liên kết không còn")
		return
	}
	if !p.Admin {
		writeError(w, http.StatusForbidden, "chỉ mời được office đã được cấp quyền Admin ở đây (nó sẽ có mọi khóa của office này)")
		return
	}
	var in struct {
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	path, size, err := s.buildFullExport(r.Context(), in.Passphrase)
	s.audit(r, audit.Change{Action: "office.full_offer", Resource: "peer", ResourceID: p.ID, Detail: map[string]any{"size": size, "peer": p.Name}, Err: err})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	exp := time.Now().Add(offerTTL)
	fullOffers.Lock()
	if old, ok := fullOffers.m[p.ID]; ok {
		_ = os.Remove(old.path)
	}
	fullOffers.m[p.ID] = fullOfferEntry{path: path, expires: exp}
	fullOffers.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"size": size, "expires_at": exp})
}

// peerFullExport hands the offered archive to the office it was offered to,
// once.
func (s *server) peerFullExport(w http.ResponseWriter, r *http.Request, p peer.Peer) {
	fullOffers.Lock()
	o, ok := fullOffers.m[p.ID]
	if ok {
		delete(fullOffers.m, p.ID) // one fetch: a second needs a new offer
	}
	fullOffers.Unlock()
	if !ok || !p.Admin || time.Now().After(o.expires) {
		if ok {
			_ = os.Remove(o.path)
		}
		writeError(w, http.StatusNotFound, "không có lời mời tải toàn bộ office cho office này")
		return
	}
	defer os.Remove(o.path)
	s.log.Info("peer: full export fetched", "peer", p.Name)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, o.path)
}

// fullPull fetches the whole office paired office {id} offered and imports it.
func (s *server) fullPull(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Passphrase  string `json:"passphrase"`
		Mode        string `json:"mode"`
		ProjectsDir string `json:"projects_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	id := r.PathValue("id")
	s.runFullImport(w, r, func(ctx context.Context) (readCloser, error) { return s.cfg.Peers.FetchFull(ctx, id) },
		in.Passphrase, in.Mode, in.ProjectsDir, id)
}
