package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/collections"
	"github.com/tung1998/agent-office/internal/storage"
)

// Data collections (ADR-172): a project's structured data, for the dashboard
// and scripts (a personal token). Agents use the data_* tools; a schema an
// agent wants is a propose_change of kind "collection".
func (s *server) collectionRoutes(mux *http.ServeMux, auth, admin func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/projects/{id}/collections", auth(s.listCollections))
	mux.Handle("POST /api/projects/{id}/collections", admin(s.createCollection))
	mux.Handle("PATCH /api/collections/{id}", admin(s.updateCollection))
	mux.Handle("GET /api/projects/{id}/collections/{key}/records", auth(s.listRecords))
	mux.Handle("POST /api/projects/{id}/collections/{key}/records", admin(s.upsertRecord))
	mux.Handle("GET /api/projects/{id}/collections/{key}/records/{rid}", auth(s.getRecord))
	mux.Handle("DELETE /api/projects/{id}/collections/{key}/records/{rid}", admin(s.deleteRecord))
	mux.Handle("GET /api/projects/{id}/collections/{key}/records/{rid}/history", auth(s.recordHistory))
	mux.Handle("POST /api/projects/{id}/files/{rid}/keep", admin(s.keepFile))
}

// keepFile moves a temporary file of the project's files table to the kept
// folder (ADR-233), as an agent's file_keep does.
func (s *server) keepFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note string `json:"note"`
	}
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	x, err := s.coll().KeepFile(r.Context(), r.PathValue("id"), r.PathValue("rid"), in.Note, Actor(r))
	if err != nil {
		s.collectionError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "file.keep", Resource: "record", ResourceID: x.ID, ProjectID: x.ProjectID, After: x.Data})
	writeJSON(w, http.StatusOK, map[string]any{"record": x})
}

func (s *server) coll() *collections.Service { return collections.New(s.cfg.Store) }

// collectionError: a request error is 400 with its text.
func (s *server) collectionError(w http.ResponseWriter, r *http.Request, err error) {
	if collections.IsInvalid(err) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeDomainError(w, r, err)
}

type collectionInput struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Fields      []storage.Field `json:"fields"`
}

func (s *server) listCollections(w http.ResponseWriter, r *http.Request) {
	list, err := s.coll().List(r.Context(), r.PathValue("id"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": list})
}

func (s *server) defineCollection(w http.ResponseWriter, r *http.Request, projectID string, in collectionInput, status int) {
	before, _ := s.cfg.Store.Collections().Get(r.Context(), projectID, in.Key)
	c, err := s.coll().Define(r.Context(), projectID, storage.Collection{Key: in.Key, Name: in.Name, Description: in.Description, Fields: in.Fields})
	if err != nil {
		s.collectionError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "collection.define", ResourceID: c.ID, ProjectID: projectID, Before: before.Fields, After: c.Fields,
		Detail: map[string]any{"key": c.Key}})
	if s.cfg.Events != nil {
		s.cfg.Events.Wrote("collections")
	}
	writeJSON(w, status, c)
}

func (s *server) createCollection(w http.ResponseWriter, r *http.Request) {
	var in collectionInput
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if _, err := s.cfg.Store.Repos().Get(r.Context(), id); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if _, err := s.cfg.Store.Collections().Get(r.Context(), id, in.Key); err == nil {
		writeError(w, http.StatusConflict, "project đã có collection "+in.Key)
		return
	}
	s.defineCollection(w, r, id, in, http.StatusCreated)
}

// updateCollection changes a collection's name, description, fields (its key stays).
func (s *server) updateCollection(w http.ResponseWriter, r *http.Request) {
	c, err := s.cfg.Store.Collections().ByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	in := collectionInput{Key: c.Key, Name: c.Name, Description: c.Description, Fields: c.Fields}
	if !decode(w, r, &in) {
		return
	}
	in.Key = c.Key
	s.defineCollection(w, r, c.ProjectID, in, http.StatusOK)
}

// listRecords: ?search= &sort= &desc=1 &limit= &offset= &where=[{"field","op","value"}]
func (s *server) listRecords(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := collections.Query{Search: qs.Get("search"), Sort: qs.Get("sort"), Desc: qs.Get("desc") == "1" || qs.Get("desc") == "true"}
	q.Limit, _ = strconv.Atoi(qs.Get("limit"))
	q.Offset, _ = strconv.Atoi(qs.Get("offset"))
	if raw := qs.Get("where"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &q.Where); err != nil {
			writeError(w, http.StatusBadRequest, "where: cần JSON [{field, op, value}]")
			return
		}
	}
	list, total, err := s.coll().Query(r.Context(), r.PathValue("id"), r.PathValue("key"), q)
	if err != nil {
		s.collectionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": list, "total": total})
}

func (s *server) upsertRecord(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID   string         `json:"id"`
		Data map[string]any `json:"data"`
	}
	if !decode(w, r, &in) {
		return
	}
	x, created, err := s.coll().Upsert(r.Context(), r.PathValue("id"), r.PathValue("key"), in.ID, in.Data, "human:"+userFrom(r).Email)
	if err != nil {
		s.collectionError(w, r, err)
		return
	}
	if s.cfg.Events != nil {
		s.cfg.Events.Wrote("records")
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, x)
}

func (s *server) getRecord(w http.ResponseWriter, r *http.Request) {
	x, refs, err := s.coll().Get(r.Context(), r.PathValue("id"), r.PathValue("key"), r.PathValue("rid"))
	if err != nil {
		s.collectionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"record": x, "refs": refs})
}

func (s *server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	err := s.coll().Delete(r.Context(), r.PathValue("id"), r.PathValue("key"), r.PathValue("rid"), "human:"+userFrom(r).Email)
	if err != nil {
		s.collectionError(w, r, err)
		return
	}
	if s.cfg.Events != nil {
		s.cfg.Events.Wrote("records")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) recordHistory(w http.ResponseWriter, r *http.Request) {
	h, err := s.coll().History(r.Context(), r.PathValue("id"), r.PathValue("key"), r.PathValue("rid"))
	if err != nil {
		s.collectionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": h})
}
