package api

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/audit"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// Workflows (spec 2026-10-07-workflows-design): the library (files) and each
// project's copies, and their runs in chats.

func (s *server) workflowRoutes(mux *http.ServeMux) {
	auth := func(h http.HandlerFunc) http.Handler { return s.requireAuth(h) }
	admin := func(h http.HandlerFunc) http.Handler { return s.requireRole(storage.RoleAdmin, h) }
	mux.Handle("GET /api/projects/{id}/workflows", auth(s.listWorkflows))
	mux.Handle("POST /api/projects/{id}/workflows", admin(s.createWorkflow))
	mux.Handle("GET /api/workflows/{id}", auth(s.getWorkflow))
	mux.Handle("PATCH /api/workflows/{id}", admin(s.updateWorkflow))
	mux.Handle("DELETE /api/workflows/{id}", admin(s.deleteWorkflow))
	mux.Handle("POST /api/workflows/{id}/from-library", admin(s.workflowFromLibrary))
	mux.Handle("POST /api/workflows/{id}/to-library", admin(s.workflowToLibrary))
	mux.Handle("GET /api/workflow-library", auth(s.listWorkflowLibrary))
	mux.Handle("POST /api/workflow-library/validate", auth(s.validateWorkflow))
	mux.Handle("POST /api/workflow-library/format", auth(s.formatWorkflow))
	mux.Handle("GET /api/workflow-library/{key}", auth(s.getLibraryWorkflow))
	mux.Handle("PUT /api/workflow-library/{key}", admin(s.saveLibraryWorkflow))
	mux.Handle("DELETE /api/workflow-library/{key}", admin(s.deleteLibraryWorkflow))
	mux.Handle("GET /api/projects/{id}/workflow-runs", auth(s.listWorkflowRuns))
	mux.Handle("GET /api/workflow-runs/{id}", auth(s.getWorkflowRun))
	mux.Handle("POST /api/workflow-runs/{id}/stop", auth(s.stopWorkflowRun))
	mux.Handle("POST /api/workflow-runs/{id}/resume", auth(s.resumeWorkflowRun))
}

type workflowDTO struct {
	ID          string            `json:"id"`
	ProjectID   string            `json:"project_id"`
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Input       string            `json:"input"`
	Enabled     bool              `json:"enabled"`
	Source      string            `json:"source"`
	Bindings    map[string]string `json:"bindings"`
	Roles       []workflow.Role   `json:"roles"`
	Parallel    [][]string        `json:"parallel"`
	Gates       []workflow.Gate   `json:"gates"`
	Vote        *workflow.Vote    `json:"vote"`
	Limits      workflow.Limits   `json:"limits"`
	Brief       []string          `json:"brief"`
	Inputs      []workflow.Field  `json:"inputs"`
	Outputs     []workflow.Field  `json:"outputs"`
	Callable    string            `json:"callable"`
	Error       string            `json:"error,omitempty"`
	SourceKey   string            `json:"source_key"`
	HasUpdate   bool              `json:"has_update"`
	UpdatedAt   time.Time         `json:"updated_at"`
	LastRun     *workflowRunDTO   `json:"last_run"`
	Version     string            `json:"version"`
}

type workflowRunDTO struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	ConversationID string `json:"conversation_id"` // the run's own chat (a run from before: the chat it ran in)
	// the chat that called it, and its title ("" = a run from before, in ConversationID)
	CallerConversationID string `json:"caller_conversation_id"`
	CallerTitle          string `json:"caller_title"`
	// the run whose role called it (a sub-workflow, ADR-102) and how deep
	ParentRunID     string            `json:"parent_run_id"`
	Depth           int               `json:"depth"`
	WorkflowID      string            `json:"workflow_id"`
	WorkflowKey     string            `json:"workflow_key"`
	WorkflowName    string            `json:"workflow_name"`
	CoordinatorID   string            `json:"coordinator_id"`
	CoordinatorName string            `json:"coordinator_name"`
	Input           string            `json:"input"`
	Status          string            `json:"status"`
	Turns           int               `json:"turns"`
	MaxTurns        int               `json:"max_turns"`
	CostUSD         float64           `json:"cost_usd"`
	Result          string            `json:"result"`
	Error           string            `json:"error"`
	Roles           []storage.RunRole `json:"roles"`
	Gates           []storage.RunGate `json:"gates"`
	Log             []storage.RunLog  `json:"log"`
	Outputs         map[string]string `json:"outputs"`
	StartedAt       time.Time         `json:"started_at"`
	FinishedAt      *time.Time        `json:"finished_at"`
	// where a graph of steps is (ADR-256); nil: a coordinator's run, a run from before
	Checkpoint *runCheckpointDTO `json:"checkpoint"`
}

// runCheckpointDTO is what the dashboard shows of a run's checkpoint.
type runCheckpointDTO struct {
	Running    []runStepDTO `json:"running"`     // the steps started, not ended
	Pending    []runStepDTO `json:"pending"`     // reached, waiting their turn
	Resumes    int          `json:"resumes"`     // how often it went on after a restart that cut a step
	MaxResumes int          `json:"max_resumes"` // past it, it ends
	// Parked: it only waits, a restart would not hurt it; Resumable: the
	// restart ended it but it may go on from where it was (Chạy tiếp từ bước dở)
	Parked    bool   `json:"parked"`
	Resumable bool   `json:"resumable"`
	Why       string `json:"why,omitempty"` // why it cannot go on, when it ended by a restart
}

type runStepDTO struct {
	ID        string     `json:"id"`
	Label     string     `json:"label"`
	Type      string     `json:"type"`
	WaitUntil *time.Time `json:"wait_until,omitempty"`
	ActionID  string     `json:"action_id,omitempty"`
}

func (s *server) toCheckpointDTO(ctx context.Context, r storage.WorkflowRun, def *workflow.Def) *runCheckpointDTO {
	cp := r.Checkpoint
	if cp == nil {
		return nil
	}
	step := func(id string) runStepDTO {
		x := runStepDTO{ID: id, Label: id, Type: cp.Running[id]}
		if def != nil {
			if st, ok := def.Step(id); ok {
				x.Label, x.Type = workflow.StepLabel(st), st.Type
			}
		}
		m := cp.Marks[id]
		x.WaitUntil, x.ActionID = m.WaitUntil, m.ActionID
		return x
	}
	d := &runCheckpointDTO{Running: []runStepDTO{}, Pending: []runStepDTO{}, Resumes: cp.Resumes, MaxResumes: workflow.MaxResumes}
	for _, id := range slices.Sorted(maps.Keys(cp.Running)) {
		d.Running = append(d.Running, step(id))
	}
	for _, id := range cp.Pending {
		d.Pending = append(d.Pending, step(id))
	}
	switch {
	case r.Status == storage.RunRunning:
		d.Parked = workflow.Parked(ctx, s.cfg.Store, r)
	case r.Status == storage.RunFailed && strings.HasPrefix(r.Error, workflow.InterruptedDetail) && r.ParentRunID == "":
		if _, _, err := workflow.CanResume(ctx, s.cfg.Store, r); err != nil {
			d.Why = err.Error()
		} else {
			d.Resumable = true
		}
	}
	return d
}

func (s *server) toWorkflowRunDTO(ctx context.Context, r storage.WorkflowRun) workflowRunDTO {
	roles := make([]storage.RunRole, len(r.Roles))
	for i, x := range r.Roles {
		x.SessionID, x.Runtime = "", "" // its sessions stay on the server
		roles[i] = x
	}
	d := workflowRunDTO{ID: r.ID, ProjectID: r.ProjectID, ConversationID: r.ConversationID, CallerConversationID: r.CallerConversationID, ParentRunID: r.ParentRunID, Depth: r.Depth, WorkflowID: r.WorkflowID, WorkflowKey: r.WorkflowKey,
		WorkflowName: r.WorkflowName, CoordinatorID: r.CoordinatorID, CoordinatorName: r.CoordinatorName, Input: r.Input, Status: r.Status, Turns: r.Turns,
		CostUSD: r.CostUSD, Result: r.Result, Error: r.Error, Roles: roles, Gates: r.Gates, Log: r.Log, Outputs: r.Outputs, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
	if c, err := s.cfg.Store.Chat().GetConversation(ctx, cmp.Or(r.CallerConversationID, r.ConversationID)); err == nil {
		d.CallerTitle = c.Title
	}
	var def *workflow.Def
	if w, err := s.cfg.Store.Workflows().Get(ctx, r.WorkflowID); err == nil {
		if x, err := workflow.Parse(w.Source); err == nil {
			d.MaxTurns, def = x.Limits.Turns, &x
		}
	}
	d.Checkpoint = s.toCheckpointDTO(ctx, r, def)
	return d
}

func (s *server) toWorkflowDTO(ctx context.Context, w storage.Workflow, withRun bool) workflowDTO {
	d := workflowDTO{ID: w.ID, ProjectID: w.ProjectID, Key: w.Key, Name: w.Name, Description: w.Description, Enabled: w.Enabled, Source: w.Source,
		Bindings: w.Bindings, SourceKey: w.SourceKey, UpdatedAt: w.UpdatedAt, Roles: []workflow.Role{}, Inputs: []workflow.Field{}, Outputs: []workflow.Field{}, Gates: []workflow.Gate{}, Parallel: [][]string{}, Brief: []string{},
		Version: workflowVersion(w)}
	if def, err := workflow.Parse(w.Source); err != nil {
		d.Error = err.Error()
	} else {
		d.Input, d.Roles, d.Vote, d.Limits, d.Callable = def.Input, def.Roles, def.Vote, def.Limits, def.Callable
		d.Inputs, d.Outputs = append([]workflow.Field{}, def.Inputs...), append([]workflow.Field{}, def.Outputs...)
		if def.Gates != nil {
			d.Gates = def.Gates
		}
		if def.Parallel != nil {
			d.Parallel = def.Parallel
		}
		if def.Brief != nil {
			d.Brief = def.Brief
		}
	}
	if s.cfg.Workflows != nil {
		d.HasUpdate = s.cfg.Workflows.HasUpdate(w)
	}
	if withRun {
		if runs, err := s.cfg.Store.WorkflowRuns().ListByWorkflow(ctx, w.ProjectID, "", w.ID, 1); err == nil && len(runs) > 0 {
			x := s.toWorkflowRunDTO(ctx, runs[0])
			d.LastRun = &x
		}
	}
	return d
}

func (s *server) workflowsOn(w http.ResponseWriter) bool {
	if s.cfg.Workflows == nil {
		writeError(w, http.StatusNotFound, "office chưa bật quy trình")
		return false
	}
	return true
}

func (s *server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	pid := r.PathValue("id")
	if _, err := s.cfg.Store.Repos().Get(r.Context(), pid); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	list, err := s.cfg.Store.Workflows().List(r.Context(), pid)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]workflowDTO, 0, len(list))
	for _, x := range list {
		out = append(out, s.toWorkflowDTO(r.Context(), x, true))
	}
	lib, _ := s.cfg.Workflows.Lib.List()
	writeJSON(w, http.StatusOK, map[string]any{"workflows": out, "library": lib})
}

type workflowInput struct {
	Key      string            `json:"key"`    // create: the library workflow to copy, when there is no Source
	Source   *string           `json:"source"` // the whole file
	Bindings map[string]string `json:"bindings"`
	Enabled  *bool             `json:"enabled"`
	Version  string            `json:"version"` // ADR-072: the version read when the edit started; "" skips the check
	// ConversationID: the chat that wrote it (read back by the config registry; unused)
	ConversationID string `json:"conversation_id"`
}

func (s *server) workflowError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, workflow.ErrNotFound), errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, workflow.ErrBuiltinImmutable):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, workflow.ErrConflict):
		writeSaveConflict(w)
	case errors.Is(err, storage.ErrConflict):
		writeError(w, http.StatusConflict, "Project đã có quy trình cùng key")
	case errors.Is(err, workflow.ErrBadBinding), errors.Is(err, workflow.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error()) // the person's to fix: say why
	default:
		s.internal(w, r, err)
	}
}

func (s *server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	var in workflowInput
	if !decode(w, r, &in) {
		return
	}
	pid := r.PathValue("id")
	if _, err := s.cfg.Store.Repos().Get(r.Context(), pid); err != nil {
		s.writeDomainError(w, r, err)
		return
	}
	enabled := in.Enabled == nil || *in.Enabled
	var (
		x   storage.Workflow
		err error
	)
	switch { // a source is the whole file: it wins over a key (its own key is inside)
	case in.Source != nil:
		x, err = s.cfg.Workflows.CreateEnabled(r.Context(), pid, *in.Source, in.Bindings, enabled)
	case in.Key != "":
		x, err = s.cfg.Workflows.InstallEnabled(r.Context(), pid, in.Key, in.Bindings, enabled)
	default:
		writeError(w, http.StatusBadRequest, "cần key (quy trình trong thư viện) hoặc source")
		return
	}
	s.audit(r, audit.Change{Action: "workflow.create", Resource: "workflow", ResourceID: x.ID, ProjectID: pid,
		Detail: map[string]any{"key": x.Key, "from": in.Key}, After: map[string]any{"key": x.Key, "bindings": x.Bindings}, Err: err})
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"workflow": s.toWorkflowDTO(r.Context(), x, false)})
}

func (s *server) getWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	x, err := s.cfg.Store.Workflows().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": s.toWorkflowDTO(r.Context(), x, true)})
}

func (s *server) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	var in workflowInput
	if !decode(w, r, &in) {
		return
	}
	before, err := s.cfg.Store.Workflows().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	if conflicted(w, in.Version, workflowVersion(before)) {
		return
	}
	change := workflow.Change{Source: in.Source, Bindings: in.Bindings, Enabled: in.Enabled}
	if in.Version != "" {
		change.Expect = &before // CAS on the row this request's version check actually saw
	}
	x, err := s.cfg.Workflows.Update(r.Context(), before.ID, change)
	s.audit(r, audit.Change{Action: "workflow.update", Resource: "workflow", ResourceID: before.ID, ProjectID: before.ProjectID,
		Before: map[string]any{"source": before.Source, "bindings": before.Bindings, "enabled": before.Enabled},
		After:  map[string]any{"source": x.Source, "bindings": x.Bindings, "enabled": x.Enabled}, Err: err})
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": s.toWorkflowDTO(r.Context(), x, false)})
}

func (s *server) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	x, err := s.cfg.Store.Workflows().Get(r.Context(), r.PathValue("id"))
	if err == nil {
		err = s.cfg.Store.Workflows().Delete(r.Context(), x.ID)
	}
	s.audit(r, audit.Change{Action: "workflow.delete", Resource: "workflow", ResourceID: r.PathValue("id"), ProjectID: x.ProjectID,
		Before: map[string]any{"key": x.Key, "source": x.Source}, Err: err})
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) workflowFromLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	x, err := s.cfg.Workflows.FromLibrary(r.Context(), r.PathValue("id"))
	s.audit(r, audit.Change{Action: "workflow.update", Resource: "workflow", ResourceID: r.PathValue("id"), ProjectID: x.ProjectID,
		Detail: map[string]any{"from_library": x.SourceKey}, Err: err})
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": s.toWorkflowDTO(r.Context(), x, false)})
}

func (s *server) workflowToLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	x, err := s.cfg.Store.Workflows().Get(r.Context(), r.PathValue("id"))
	if err == nil {
		_, err = s.cfg.Workflows.Lib.Save(x.Key, x.Source)
	}
	s.audit(r, audit.Change{Action: "workflow.to_library", Resource: "workflow", ResourceID: r.PathValue("id"), ProjectID: x.ProjectID,
		Detail: map[string]any{"key": x.Key}, Err: err})
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) listWorkflowLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	list, err := s.cfg.Workflows.Lib.List()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": list})
}

func (s *server) getLibraryWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	it, err := s.cfg.Workflows.Lib.Get(r.PathValue("key"))
	if errors.Is(err, workflow.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": it}) // one that does not parse comes with its error
}

func (s *server) validateWorkflow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source string `json:"source"`
	}
	if !decode(w, r, &in) {
		return
	}
	d, err := workflow.Parse(in.Source)
	if err != nil {
		out := map[string]any{"ok": false, "error": err.Error()}
		if d.Key != "" || len(d.Roles) > 0 || len(d.Steps) > 0 { // it reads, it only does not check: the canvas can still show it
			out["draft"] = d
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "def": d})
}

// formatWorkflow writes a workflow's file from its definition (the editor's
// canvas changes the definition): the file, and what is wrong with it.
func (s *server) formatWorkflow(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Def workflow.Def `json:"def"`
	}
	if !decode(w, r, &in) {
		return
	}
	src, err := workflow.Format(in.Def)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out := map[string]any{"source": src}
	if _, err := workflow.Parse(src); err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) saveLibraryWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	var in struct {
		Source  string `json:"source"`
		Version string `json:"version"` // ADR-072: the version read when the edit started; "" skips the check
	}
	if !decode(w, r, &in) {
		return
	}
	key := r.PathValue("key")
	it, err := s.cfg.Workflows.Lib.SaveIf(key, in.Source, in.Version)
	s.audit(r, audit.Change{Action: "workflow_library.save", Resource: "workflow_library", ResourceID: key, Err: err})
	if errors.Is(err, workflow.ErrConflict) {
		writeJSON(w, http.StatusConflict, map[string]any{"code": "conflict", "error": err.Error()})
		return
	}
	if errors.Is(err, workflow.ErrBuiltinImmutable) {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": it})
}

func (s *server) deleteLibraryWorkflow(w http.ResponseWriter, r *http.Request) {
	if !s.workflowsOn(w) {
		return
	}
	key := r.PathValue("key")
	err := s.cfg.Workflows.Lib.Delete(key)
	s.audit(r, audit.Change{Action: "workflow_library.delete", Resource: "workflow_library", ResourceID: key, Err: err})
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	// ?conversation=: a chat's (called from it, or run in it); ?workflow=: one workflow's
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	wf := r.URL.Query().Get("workflow")
	pid, conv := r.PathValue("id"), r.URL.Query().Get("conversation")
	var runs []storage.WorkflowRun
	var err error
	if wf != "" {
		runs, err = s.cfg.Store.WorkflowRuns().ListByWorkflow(r.Context(), pid, conv, wf, limit)
	} else {
		runs, err = s.cfg.Store.WorkflowRuns().List(r.Context(), pid, conv, limit)
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]workflowRunDTO, 0, len(runs))
	for _, x := range runs {
		out = append(out, s.toWorkflowRunDTO(r.Context(), x))
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

func (s *server) getWorkflowRun(w http.ResponseWriter, r *http.Request) {
	x, err := s.cfg.Store.WorkflowRuns().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	// the sub-workflows it called (its own chat called them)
	children := []workflowRunDTO{}
	if list, err := s.cfg.Store.WorkflowRuns().ListChildren(r.Context(), x.ID); err == nil {
		for _, c := range list {
			children = append(children, s.toWorkflowRunDTO(r.Context(), c))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": s.toWorkflowRunDTO(r.Context(), x), "children": children})
}

// resumeWorkflowRun goes on with a run the restart ended from where it was
// (ADR-256): a graph of steps that kept its checkpoint.
func (s *server) resumeWorkflowRun(w http.ResponseWriter, r *http.Request) {
	x, err := s.cfg.Store.WorkflowRuns().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	if s.cfg.Chat == nil {
		writeError(w, http.StatusServiceUnavailable, "office chưa chạy chat")
		return
	}
	if err := s.cfg.Chat.ResumeRun(r.Context(), x.ID); err != nil {
		switch {
		case errors.Is(err, chat.ErrNotResumable):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, chat.ErrWorkflowRunning):
			writeError(w, http.StatusConflict, err.Error())
		default:
			s.internal(w, r, err)
		}
		return
	}
	s.audit(r, audit.Change{Action: "workflow_run.resume", Resource: "workflow_run", ResourceID: x.ID, ProjectID: x.ProjectID})
	if x, err = s.cfg.Store.WorkflowRuns().Get(r.Context(), x.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": s.toWorkflowRunDTO(r.Context(), x)})
}

// stopWorkflowRun stops a run and every answer of its chat (as the chat's Dừng).
func (s *server) stopWorkflowRun(w http.ResponseWriter, r *http.Request) {
	x, err := s.cfg.Store.WorkflowRuns().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.workflowError(w, r, err)
		return
	}
	if x.Status == storage.RunRunning && s.cfg.Chat != nil && s.cfg.Chat.RunningWorkflow(x.ConversationID) == x.ID {
		s.cfg.Chat.StopAll(x.ConversationID)
	}
	s.audit(r, audit.Change{Action: "workflow_run.stop", Resource: "workflow_run", ResourceID: x.ID, ProjectID: x.ProjectID})
	if x, err = s.cfg.Store.WorkflowRuns().Get(r.Context(), x.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": s.toWorkflowRunDTO(r.Context(), x)})
}
