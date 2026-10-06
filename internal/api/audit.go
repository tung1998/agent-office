package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/storage"
)

// audit records a change made through the API (ADR-043): by the signed-in
// person unless the request context already names someone (an approved
// proposal is the agent's change).
func (s *server) audit(r *http.Request, c audit.Change) {
	ctx := r.Context()
	w, ok := audit.From(ctx)
	if !ok {
		u := userFrom(r)
		w = audit.Who{Kind: "human", ID: u.ID, Name: u.Email, Via: "ui"}
	}
	res := c.Resource
	if res == "" {
		res, _, _ = strings.Cut(c.Action, ".")
	}
	if c.ProjectID == "" {
		if p, ok := c.Detail["project"].(string); ok {
			c.ProjectID = p
		} else if res == "project" {
			c.ProjectID = c.ResourceID
		}
	}
	if c.ProjectID == "" && c.ResourceID != "" {
		c.ProjectID = s.projectOf(ctx, res, c.ResourceID)
	}
	switch res {
	case "job":
		w.JobID = c.ResourceID
	case "task":
		if _, err := s.cfg.Store.Tasks().Get(ctx, c.ResourceID); err == nil { // task.queue names a job
			w.TaskID = c.ResourceID
		} else if w.JobID == "" {
			w.JobID = c.ResourceID
		}
	}
	_ = audit.Record(audit.With(ctx, w), s.cfg.Store.Audit(), c)
}

// projectOf finds the project a changed thing belongs to, so the project's
// Nhật ký tab shows it ("" = office-wide or gone).
func (s *server) projectOf(ctx context.Context, resource, id string) string {
	st := s.cfg.Store
	switch resource {
	case "automation":
		if a, err := st.Automations().Get(ctx, id); err == nil {
			return a.ProjectID
		}
	case "job":
		if j, err := st.Jobs().Get(ctx, id); err == nil {
			return j.ProjectID
		}
	case "task":
		if t, err := st.Tasks().Get(ctx, id); err == nil {
			return t.ProjectID
		}
		if j, err := st.Jobs().Get(ctx, id); err == nil {
			return j.ProjectID
		}
	case "process":
		if p, err := st.Processes().Get(ctx, id); err == nil {
			return p.ProjectID
		}
	case "monitor":
		if m, err := st.Monitors().Get(ctx, id); err == nil {
			return m.ProjectID
		}
	case "org_model":
		if m, err := st.OrgModels().Get(ctx, id); err == nil {
			return m.RepoID
		}
	case "action", "git":
		if a, err := st.Actions().Get(ctx, id); err == nil {
			return a.ProjectID
		}
	case "agent":
		if a, err := st.Agents().Get(ctx, id); err == nil {
			return s.agentProject(ctx, a)
		}
	case "compose":
		return id
	}
	return ""
}

// agentProject is the project an agent belongs to ("" = a template's agent).
func (s *server) agentProject(ctx context.Context, a storage.Agent) string {
	m, err := s.cfg.Store.OrgModels().Get(ctx, a.OrgModelID)
	if err != nil {
		return ""
	}
	return m.RepoID
}

// orgModelProjectPath is the filesystem path of the project an org model
// belongs to ("" = a template, or a project without a path) — for validating
// an agent's extra read dirs against it (ADR-074).
func (s *server) orgModelProjectPath(ctx context.Context, orgModelID string) string {
	m, err := s.cfg.Store.OrgModels().Get(ctx, orgModelID)
	if err != nil || m.RepoID == "" {
		return ""
	}
	repo, err := s.cfg.Store.Repos().Get(ctx, m.RepoID)
	if err != nil {
		return ""
	}
	return repo.Path
}

// repoSnapshot / modelSnapshot: the fields a person edits (no timestamps, so
// a diff shows only what changed).
func repoSnapshot(x storage.Repo) map[string]any {
	return map[string]any{"name": x.Name, "path": x.Path, "git_remote": x.GitRemote, "description": x.Description}
}

func modelSnapshot(m storage.OrgModel) map[string]any {
	return map[string]any{"key": m.Key, "name": m.Name, "description": m.Description, "kind": m.Kind, "governance": m.Governance}
}

func auditFilter(r *http.Request) storage.AuditFilter {
	q := r.URL.Query()
	f := storage.AuditFilter{ProjectID: q.Get("project"), Resource: q.Get("resource"), ResourceID: q.Get("resource_id"),
		ActorKind: q.Get("actor_kind"), ActorName: q.Get("actor"), Via: q.Get("via"),
		ConversationID: q.Get("conversation_id"), JobID: q.Get("job_id"), TaskID: q.Get("task_id"), BeforeID: q.Get("before")}
	if d, err := time.ParseDuration(q.Get("since")); err == nil && d > 0 {
		f.From = time.Now().Add(-d)
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	return f
}

type auditDTO struct {
	ID             string         `json:"id"`
	Action         string         `json:"action"`
	ActorKind      string         `json:"actor_kind"`
	ActorID        string         `json:"actor_id"`
	ActorName      string         `json:"actor_name"`
	ApprovedBy     string         `json:"approved_by"`
	Via            string         `json:"via"`
	ProjectID      string         `json:"project_id"`
	ProjectName    string         `json:"project_name"`
	ConversationID string         `json:"conversation_id"`
	JobID          string         `json:"job_id"`
	TaskID         string         `json:"task_id"`
	ActionID       string         `json:"action_id"`
	Resource       string         `json:"resource"`
	ResourceID     string         `json:"resource_id"`
	Target         string         `json:"target"`
	Detail         map[string]any `json:"detail"`
	Before         map[string]any `json:"before"`
	After          map[string]any `json:"after"`
	OK             bool           `json:"ok"`
	At             time.Time      `json:"at"`
}

func (s *server) listAudit(w http.ResponseWriter, r *http.Request) {
	f := auditFilter(r)
	entries, err := s.cfg.Store.Audit().List(r.Context(), f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	names := map[string]string{}
	out := make([]auditDTO, 0, len(entries))
	for _, e := range entries {
		if _, ok := names[e.ProjectID]; !ok && e.ProjectID != "" {
			if p, err := s.cfg.Store.Repos().Get(r.Context(), e.ProjectID); err == nil {
				names[e.ProjectID] = p.Name
			} else {
				names[e.ProjectID] = ""
			}
		}
		out = append(out, auditDTO{e.ID, e.Action, e.ActorKind, e.ActorID, e.ActorName, e.ApprovedBy, e.Via, e.ProjectID, names[e.ProjectID],
			e.ConversationID, e.JobID, e.TaskID, e.ActionID, e.Resource, e.ResourceID, e.Target, e.Detail, e.Before, e.After, e.OK, e.At})
	}
	next := ""
	if len(entries) == f.Limit {
		next = entries[len(entries)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "next_before": next})
}

func (s *server) auditStats(w http.ResponseWriter, r *http.Request) {
	by := r.URL.Query().Get("by")
	rows, err := s.cfg.Store.Audit().Count(r.Context(), auditFilter(r), by)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	type row struct {
		Key    string `json:"key"`
		Count  int    `json:"count"`
		Failed int    `json:"failed"`
	}
	out := make([]row, 0, len(rows))
	for _, c := range rows {
		out = append(out, row{c.Key, c.Count, c.Failed})
	}
	writeJSON(w, http.StatusOK, map[string]any{"by": by, "rows": out})
}
