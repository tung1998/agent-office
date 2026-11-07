package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type workflowRepo struct{ db dbtx }

func (s *Store) Workflows() storage.WorkflowRepo { return workflowRepo{s.q} }

const workflowCols = `id, project_id, key, name, description, source, source_key, source_hash, bindings, enabled, created_at, updated_at`

func scanWorkflow(row scanner) (storage.Workflow, error) {
	var (
		w                storage.Workflow
		bindings         string
		enabled          int
		created, updated string
	)
	if err := row.Scan(&w.ID, &w.ProjectID, &w.Key, &w.Name, &w.Description, &w.Source, &w.SourceKey, &w.SourceHash, &bindings, &enabled, &created, &updated); err != nil {
		return w, notFound(err)
	}
	w.Enabled = enabled != 0
	_ = json.Unmarshal([]byte(bindings), &w.Bindings)
	if w.Bindings == nil {
		w.Bindings = map[string]string{}
	}
	return w, parseTimes([]*time.Time{&w.CreatedAt, &w.UpdatedAt}, created, updated)
}

func bindingsJSON(m map[string]string) string {
	if m == nil {
		m = map[string]string{}
	}
	return toJSON(m)
}

func (r workflowRepo) Create(ctx context.Context, w storage.Workflow) (storage.Workflow, error) {
	now := time.Now().UTC()
	if w.ID == "" {
		w.ID = ids.New("wfl")
	}
	w.CreatedAt, w.UpdatedAt = now, now
	_, err := r.db.ExecContext(ctx, `INSERT INTO workflows (`+workflowCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		w.ID, w.ProjectID, w.Key, w.Name, w.Description, w.Source, w.SourceKey, w.SourceHash, bindingsJSON(w.Bindings), boolInt(w.Enabled), fmtTime(now), fmtTime(now))
	if isUnique(err) {
		return storage.Workflow{}, storage.ErrConflict
	}
	return w, err
}

func (r workflowRepo) Update(ctx context.Context, w storage.Workflow) error {
	err := execOne(ctx, r.db, `UPDATE workflows SET key=?, name=?, description=?, source=?, source_key=?, source_hash=?, bindings=?, enabled=?, updated_at=? WHERE id=?`,
		w.Key, w.Name, w.Description, w.Source, w.SourceKey, w.SourceHash, bindingsJSON(w.Bindings), boolInt(w.Enabled), fmtTime(time.Now()), w.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

func (r workflowRepo) UpdateContent(ctx context.Context, w storage.Workflow) error {
	err := execOne(ctx, r.db, `UPDATE workflows SET key=?, name=?, description=?, source=?, bindings=?, updated_at=? WHERE id=?`,
		w.Key, w.Name, w.Description, w.Source, bindingsJSON(w.Bindings), fmtTime(time.Now()), w.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

func (r workflowRepo) UpdateContentIf(ctx context.Context, w storage.Workflow, expectSource string, expectBindings map[string]string) error {
	err := execOne(ctx, r.db, `UPDATE workflows SET key=?, name=?, description=?, source=?, bindings=?, updated_at=? WHERE id=? AND source=? AND bindings=?`,
		w.Key, w.Name, w.Description, w.Source, bindingsJSON(w.Bindings), fmtTime(time.Now()), w.ID, expectSource, bindingsJSON(expectBindings))
	if isUnique(err) {
		return storage.ErrConflict
	}
	if errors.Is(err, storage.ErrNotFound) {
		return storage.ErrConflict
	}
	return err
}

func (r workflowRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return execOne(ctx, r.db, `UPDATE workflows SET enabled=?, updated_at=? WHERE id=?`, boolInt(enabled), fmtTime(time.Now()), id)
}

func (r workflowRepo) UpdateContentAndSourceIf(ctx context.Context, w storage.Workflow, expectSource string, expectBindings map[string]string) error {
	err := execOne(ctx, r.db, `UPDATE workflows SET key=?, name=?, description=?, source=?, bindings=?, source_key=?, source_hash=?, updated_at=? WHERE id=? AND source=? AND bindings=?`,
		w.Key, w.Name, w.Description, w.Source, bindingsJSON(w.Bindings), w.SourceKey, w.SourceHash, fmtTime(time.Now()), w.ID, expectSource, bindingsJSON(expectBindings))
	if isUnique(err) {
		return storage.ErrConflict
	}
	if errors.Is(err, storage.ErrNotFound) {
		return storage.ErrConflict
	}
	return err
}

func (r workflowRepo) Get(ctx context.Context, id string) (storage.Workflow, error) {
	return scanWorkflow(r.db.QueryRowContext(ctx, `SELECT `+workflowCols+` FROM workflows WHERE id=?`, id))
}

func (r workflowRepo) GetByKey(ctx context.Context, projectID, key string) (storage.Workflow, error) {
	return scanWorkflow(r.db.QueryRowContext(ctx, `SELECT `+workflowCols+` FROM workflows WHERE project_id=? AND key=?`, projectID, key))
}

func (r workflowRepo) List(ctx context.Context, projectID string) ([]storage.Workflow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+workflowCols+` FROM workflows WHERE project_id=? OR ?='' ORDER BY created_at, key`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Workflow{}
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r workflowRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM workflows WHERE id=?`, id)
}

// ---- runs ----

type workflowRunRepo struct{ db dbtx }

func (s *Store) WorkflowRuns() storage.WorkflowRunRepo { return workflowRunRepo{s.q} }

const workflowRunCols = `id, project_id, conversation_id, caller_conversation_id, parent_run_id, depth, workflow_id, workflow_key, workflow_name, body_hash, coordinator_id, coordinator_name,
	input, status, turns, cost_usd, result, error, roles, gates, log, actor, started_at, finished_at, outputs, checkpoint`

func scanWorkflowRun(row scanner) (storage.WorkflowRun, error) {
	var (
		r                  storage.WorkflowRun
		roles, gates, logs string
		outputs            string
		checkpoint         string
		started            string
		finished           sql.NullString
	)
	if err := row.Scan(&r.ID, &r.ProjectID, &r.ConversationID, &r.CallerConversationID, &r.ParentRunID, &r.Depth, &r.WorkflowID, &r.WorkflowKey, &r.WorkflowName, &r.BodyHash, &r.CoordinatorID, &r.CoordinatorName,
		&r.Input, &r.Status, &r.Turns, &r.CostUSD, &r.Result, &r.Error, &roles, &gates, &logs, &r.Actor, &started, &finished, &outputs, &checkpoint); err != nil {
		return r, notFound(err)
	}
	_ = json.Unmarshal([]byte(roles), &r.Roles)
	_ = json.Unmarshal([]byte(gates), &r.Gates)
	_ = json.Unmarshal([]byte(logs), &r.Log)
	_ = json.Unmarshal([]byte(outputs), &r.Outputs)
	if checkpoint != "" {
		var cp storage.RunCheckpoint
		if json.Unmarshal([]byte(checkpoint), &cp) == nil {
			r.Checkpoint = &cp
		}
	}
	if r.Roles == nil {
		r.Roles = []storage.RunRole{}
	}
	if r.Gates == nil {
		r.Gates = []storage.RunGate{}
	}
	if r.Log == nil {
		r.Log = []storage.RunLog{}
	}
	var err error
	if r.StartedAt, err = parseTime(started); err != nil {
		return r, err
	}
	if finished.Valid && finished.String != "" {
		t, err := parseTime(finished.String)
		if err != nil {
			return r, err
		}
		r.FinishedAt = &t
	}
	return r, nil
}

func mapJSON(m map[string]string) string {
	if m == nil {
		m = map[string]string{}
	}
	return toJSON(m)
}

func listJSON[T any](v []T) string {
	if v == nil {
		v = []T{}
	}
	return toJSON(v)
}

func (r workflowRunRepo) Create(ctx context.Context, x storage.WorkflowRun) (storage.WorkflowRun, error) {
	if x.ID == "" {
		x.ID = ids.New("wfr")
	}
	if x.StartedAt.IsZero() {
		x.StartedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO workflow_runs (`+workflowRunCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		x.ID, x.ProjectID, x.ConversationID, x.CallerConversationID, x.ParentRunID, x.Depth, x.WorkflowID, x.WorkflowKey, x.WorkflowName, x.BodyHash, x.CoordinatorID, x.CoordinatorName,
		x.Input, x.Status, x.Turns, x.CostUSD, x.Result, x.Error, listJSON(x.Roles), listJSON(x.Gates), listJSON(x.Log), x.Actor,
		fmtTime(x.StartedAt), optTime(x.FinishedAt), mapJSON(x.Outputs), checkpointJSON(x.Checkpoint))
	return x, err
}

func (r workflowRunRepo) Update(ctx context.Context, x storage.WorkflowRun) error {
	return execOne(ctx, r.db, `UPDATE workflow_runs SET status=?, turns=?, cost_usd=?, result=?, error=?, roles=?, gates=?, log=?, coordinator_id=?, coordinator_name=?, finished_at=?, outputs=?, checkpoint=? WHERE id=?`,
		x.Status, x.Turns, x.CostUSD, x.Result, x.Error, listJSON(x.Roles), listJSON(x.Gates), listJSON(x.Log), x.CoordinatorID, x.CoordinatorName, optTime(x.FinishedAt), mapJSON(x.Outputs), checkpointJSON(x.Checkpoint), x.ID)
}

// checkpointJSON is a run's checkpoint as stored ("" = none).
func checkpointJSON(c *storage.RunCheckpoint) string {
	if c == nil {
		return ""
	}
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(b)
}

func (r workflowRunRepo) Get(ctx context.Context, id string) (storage.WorkflowRun, error) {
	return scanWorkflowRun(r.db.QueryRowContext(ctx, `SELECT `+workflowRunCols+` FROM workflow_runs WHERE id=?`, id))
}

func (r workflowRunRepo) List(ctx context.Context, projectID, conversationID string, limit int) ([]storage.WorkflowRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+workflowRunCols+` FROM workflow_runs
		WHERE (?='' OR project_id=?) AND (?='' OR conversation_id=? OR caller_conversation_id=?) ORDER BY started_at DESC, id DESC LIMIT ?`,
		projectID, projectID, conversationID, conversationID, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.WorkflowRun{}
	for rows.Next() {
		x, err := scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r workflowRunRepo) ListByWorkflow(ctx context.Context, projectID, conversationID, workflowID string, limit int) ([]storage.WorkflowRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+workflowRunCols+` FROM workflow_runs
		WHERE (?='' OR project_id=?) AND (?='' OR conversation_id=? OR caller_conversation_id=?) AND workflow_id=? ORDER BY started_at DESC, id DESC LIMIT ?`,
		projectID, projectID, conversationID, conversationID, conversationID, workflowID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.WorkflowRun{}
	for rows.Next() {
		x, err := scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r workflowRunRepo) FailRunning(ctx context.Context, detail string, at time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE workflow_runs SET status=?, error=?, finished_at=? WHERE status=?`,
		storage.RunFailed, detail, fmtTime(at), storage.RunRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
