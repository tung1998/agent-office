package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/memory"
)

// An agent's long-term notes (ADR-068): seen by the project's people, kept by admins.

type memoryDTO struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Source    string    `json:"source"`
	CreatedBy string    `json:"created_by"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   string    `json:"version"` // what an edit is made from (ADR-072)
}

type memoryRevisionDTO struct {
	ID        string    `json:"id"`
	Reason    string    `json:"reason"`
	Count     int       `json:"count"`
	Items     []string  `json:"items"`
	CreatedAt time.Time `json:"created_at"`
}

// projectAgent: the agent {aid} is one of project {id}'s.
func (s *server) projectAgent(w http.ResponseWriter, r *http.Request) (projectID, agentID string, ok bool) {
	projectID, agentID = r.PathValue("id"), r.PathValue("aid")
	agents, err := s.cfg.Chat.Agents(r.Context(), projectID)
	if err != nil {
		s.writeDomainError(w, r, err)
		return "", "", false
	}
	for _, a := range agents {
		if a.ID == agentID {
			return projectID, agentID, true
		}
	}
	writeError(w, http.StatusNotFound, "agent không thuộc project này")
	return "", "", false
}

func (s *server) listMemories(w http.ResponseWriter, r *http.Request) {
	pid, aid, ok := s.projectAgent(w, r)
	if !ok {
		return
	}
	list, err := s.cfg.Store.Memories().List(r.Context(), pid, aid)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := make([]memoryDTO, 0, len(list))
	size := 0
	for _, m := range list {
		items = append(items, memoryDTO{m.ID, m.Text, m.Source, m.CreatedBy, m.UpdatedAt, versionOf(m.Text)})
		size += len([]rune(m.Text))
	}
	revs, _ := s.cfg.Store.Memories().Revisions(r.Context(), pid, aid)
	out := make([]memoryRevisionDTO, 0, len(revs))
	for _, rv := range revs {
		texts := make([]string, 0, len(rv.Items))
		for _, m := range rv.Items {
			texts = append(texts, m.Text)
		}
		out = append(out, memoryRevisionDTO{rv.ID, rv.Reason, len(rv.Items), texts, rv.CreatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "revisions": out, "auto": s.cfg.Memory.Auto(r.Context(), pid),
		"size": size, "limit": s.cfg.Memory.Limit})
}

func (s *server) addMemory(w http.ResponseWriter, r *http.Request) {
	pid, aid, ok := s.projectAgent(w, r)
	if !ok {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	m, err := s.cfg.Memory.Add(r.Context(), pid, aid, in.Text, "person", "human:"+userFrom(r).Email)
	if errors.Is(err, memory.ErrEmpty) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "memory.add", Resource: "memory", ResourceID: m.ID, ProjectID: pid, After: m.Text})
	writeJSON(w, http.StatusCreated, map[string]any{"memory": memoryDTO{m.ID, m.Text, m.Source, m.CreatedBy, m.UpdatedAt, versionOf(m.Text)}})
}

func (s *server) editMemory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text    string `json:"text"`
		Version string `json:"version"` // the note as it was read (409 when changed since)
	}
	if !decode(w, r, &in) {
		return
	}
	old, err := s.cfg.Store.Memories().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if conflicted(w, in.Version, versionOf(old.Text)) {
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, memory.ErrEmpty.Error())
		return
	}
	if utf8.RuneCountInString(text) > memory.MaxNote {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Một ghi nhớ tối đa %d ký tự.", memory.MaxNote))
		return
	}
	if err := s.cfg.Store.Memories().Update(r.Context(), old.ID, text); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "memory.edit", Resource: "memory", ResourceID: old.ID, ProjectID: old.ProjectID, Before: old.Text, After: text})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	old, err := s.cfg.Store.Memories().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Store.Memories().Delete(r.Context(), old.ID); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "memory.delete", Resource: "memory", ResourceID: old.ID, ProjectID: old.ProjectID, Before: old.Text})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) compactMemories(w http.ResponseWriter, r *http.Request) {
	pid, aid, ok := s.projectAgent(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Memory.Compact(r.Context(), pid, aid, "rút gọn bởi "+userFrom(r).Email); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, audit.Change{Action: "memory.compact", Resource: "memory", ResourceID: aid, ProjectID: pid})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) restoreMemories(w http.ResponseWriter, r *http.Request) {
	rev, err := s.cfg.Store.Memories().GetRevision(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Memory.Restore(r.Context(), rev.ID, userFrom(r).Email); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "memory.restore", Resource: "memory", ResourceID: rev.AgentID, ProjectID: rev.ProjectID, Detail: map[string]any{"revision": rev.ID}})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) memorySettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Auto bool `json:"auto"`
	}
	if !decode(w, r, &in) {
		return
	}
	pid := r.PathValue("id")
	if _, err := s.cfg.Store.Repos().Get(r.Context(), pid); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	if err := s.cfg.Memory.SetAuto(r.Context(), pid, in.Auto); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, audit.Change{Action: "memory.settings", Resource: "project", ResourceID: pid, ProjectID: pid, After: map[string]any{"auto": in.Auto}})
	writeJSON(w, http.StatusOK, map[string]any{"auto": in.Auto})
}
