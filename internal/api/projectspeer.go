package api

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/fullexport"
	"github.com/tung1998/agent-office/internal/peer"
)

// Projects through a paired office (ADR-278):
//   - offer / fetch: this office lets a paired one fetch chosen projects once,
//     within the hour; there a person previews and picks what each becomes;
//   - sync: this office pushes chosen projects, the other applies them at
//     once (a clash updates the project there, the rest are made new). Only
//     when that office gave this one Admin: nobody there presses a button.

const peerSyncPath = "/api/peer/projects-sync"

var projectOffers = struct {
	sync.Mutex
	m map[string]fullOfferEntry // peer id → its offer
}{m: map[string]fullOfferEntry{}}

type projectsPick struct {
	ProjectIDs []string `json:"project_ids"`
	Include    []string `json:"include"`
	Passphrase string   `json:"passphrase"`
}

func (s *server) projectsOffer(w http.ResponseWriter, r *http.Request) {
	p, err := s.cfg.Peers.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "office liên kết không còn")
		return
	}
	var in projectsPick
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	path, size, status, err := s.buildProjectsArchive(r, in.ProjectIDs, in.Include, in.Passphrase)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	exp := time.Now().Add(offerTTL)
	projectOffers.Lock()
	if old, ok := projectOffers.m[p.ID]; ok {
		_ = os.Remove(old.path)
	}
	projectOffers.m[p.ID] = fullOfferEntry{path: path, expires: exp}
	projectOffers.Unlock()
	s.audit(r, audit.Change{Action: "office.projects_offer", Resource: "peer", ResourceID: p.ID, Detail: map[string]any{"projects": in.ProjectIDs, "peer": p.Name}})
	writeJSON(w, http.StatusOK, map[string]any{"size": size, "expires_at": exp})
}

// peerProjectsExport hands the offered projects to the office they were offered to, once.
func (s *server) peerProjectsExport(w http.ResponseWriter, r *http.Request, p peer.Peer) {
	projectOffers.Lock()
	o, ok := projectOffers.m[p.ID]
	if ok {
		delete(projectOffers.m, p.ID)
	}
	projectOffers.Unlock()
	if !ok || time.Now().After(o.expires) {
		if ok {
			_ = os.Remove(o.path)
		}
		writeError(w, http.StatusNotFound, "office bên kia chưa cho office này tải project (hoặc lời mời đã hết hạn / đã dùng)")
		return
	}
	defer os.Remove(o.path)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, o.path)
}

// projectsFetch fetches what paired office {id} offered and previews it.
func (s *server) projectsFetch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	body, err := s.cfg.Peers.Stream(r.Context(), r.PathValue("id"), http.MethodGet, "/api/peer/projects-export", nil, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer body.Close()
	s.stagePreview(w, r, body, in.Passphrase)
}

// projectsSync pushes the chosen projects to paired office {id}, which
// applies them at once. The archive's password is made here and travels
// with it: the link between the offices is what protects it.
func (s *server) projectsSync(w http.ResponseWriter, r *http.Request) {
	p, err := s.cfg.Peers.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "office liên kết không còn")
		return
	}
	var in projectsPick
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	pass := randomHex(24)
	path, _, status, err := s.buildProjectsArchive(r, in.ProjectIDs, in.Include, pass)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	defer os.Remove(path)
	f, err := os.Open(path)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer f.Close()
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/octet-stream")
	hdr.Set(passphraseHeader, pass)
	answer, err := s.cfg.Peers.Stream(r.Context(), p.ID, http.MethodPost, peerSyncPath, f, hdr)
	s.audit(r, audit.Change{Action: "office.projects_sync", Resource: "peer", ResourceID: p.ID, Detail: map[string]any{"projects": in.ProjectIDs, "peer": p.Name}, Err: err})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer answer.Close()
	var out struct {
		Results []importResult `json:"results"`
	}
	if err := json.NewDecoder(answer).Decode(&out); err != nil {
		writeError(w, http.StatusBadGateway, "office bên kia trả lời không đọc được")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// peerProjectsSync applies the projects a paired office with Admin here
// pushes: a clash updates the project here, the rest are made new.
func (s *server) peerProjectsSync(w http.ResponseWriter, r *http.Request, p peer.Peer) {
	if !p.Admin {
		writeError(w, http.StatusForbidden, "office này chưa cấp quyền Admin cho office gửi; cấp ở trang Office liên kết, hoặc dùng \"Cho office này tải\"")
		return
	}
	if !fullBusy.TryLock() {
		writeError(w, http.StatusConflict, "office bên kia đang xuất/nhập, thử lại sau")
		return
	}
	defer fullBusy.Unlock()
	dir := s.stagingDir(randomHex(12))
	defer os.RemoveAll(dir)
	st, err := fullexport.StageProjects(r.Context(), r.Body, r.Header.Get(passphraseHeader), dir)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	results, err := s.applyStaged(r, st, nil, "", "overwrite")
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.log.Info("peer: projects synced", "peer", p.Name, "projects", len(results))
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
