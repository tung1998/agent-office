package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type taskRepo struct{ db dbtx }

const taskCols = `id, project_id, title, goal, mode, status, result, detail, budget_usd, cost_usd, mode_level, attachments, created_by, created_at, finished_at, edit_mode, assignee_id`

// taskRead: what is read back (cleaned is set by the data cleanup only)
const taskRead = taskCols + `, cleaned`

func scanTask(row scanner) (storage.Task, error) {
	var (
		t             storage.Task
		atts, created string
		finished      sql.NullString
	)
	if err := row.Scan(&t.ID, &t.ProjectID, &t.Title, &t.Goal, &t.Mode, &t.Status, &t.Result, &t.Detail, &t.BudgetUSD, &t.CostUSD,
		&t.ModeLevel, &atts, &t.CreatedBy, &created, &finished, &t.EditMode, &t.AssigneeID, &t.Cleaned); err != nil {
		return t, notFound(err)
	}
	if err := json.Unmarshal([]byte(atts), &t.Attachments); err != nil {
		return t, err
	}
	var err error
	if t.CreatedAt, err = parseTime(created); err != nil {
		return t, err
	}
	if finished.Valid {
		f, err := parseTime(finished.String)
		if err != nil {
			return t, err
		}
		t.FinishedAt = &f
	}
	return t, nil
}

func optTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

func (r taskRepo) Create(ctx context.Context, t storage.Task) (storage.Task, error) {
	if t.ID == "" {
		t.ID = ids.New("tsk")
	}
	t.CreatedAt = time.Now().UTC()
	if t.Attachments == nil {
		t.Attachments = []storage.Attachment{}
	}
	if t.ModeLevel == "" {
		t.ModeLevel = "propose"
	}
	if t.EditMode == "" {
		t.EditMode = "worktree"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO tasks (`+taskCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.ProjectID, t.Title, t.Goal, t.Mode, t.Status, t.Result, t.Detail, t.BudgetUSD, t.CostUSD, t.ModeLevel, toJSON(t.Attachments), t.CreatedBy, fmtTime(t.CreatedAt), optTime(t.FinishedAt), t.EditMode, t.AssigneeID)
	return t, err
}

func (r taskRepo) Update(ctx context.Context, t storage.Task) error {
	return execOne(ctx, r.db, `UPDATE tasks SET title=?, status=?, result=?, detail=?, cost_usd=?, finished_at=? WHERE id=?`,
		t.Title, t.Status, t.Result, t.Detail, t.CostUSD, optTime(t.FinishedAt), t.ID)
}

func (r taskRepo) Get(ctx context.Context, id string) (storage.Task, error) {
	return scanTask(r.db.QueryRowContext(ctx, `SELECT `+taskRead+` FROM tasks WHERE id=?`, id))
}

func (r taskRepo) List(ctx context.Context, projectID string, limit int) ([]storage.Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// an empty projectID lists tasks of every project (the Jobs page)
	rows, err := r.db.QueryContext(ctx, `SELECT `+taskRead+` FROM tasks WHERE (?='' OR project_id=?) ORDER BY created_at DESC LIMIT ?`, projectID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r taskRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM tasks WHERE id=?`, id)
}

const stepCols = `id, task_id, seq, phase, agent_id, agent_key, agent_name, instruction, output, data, tools, status, error, cost_usd, run_id, started_at, finished_at`

func (r taskRepo) AddStep(ctx context.Context, s storage.TaskStep) (storage.TaskStep, error) {
	if s.ID == "" {
		s.ID = ids.New("stp")
	}
	if s.StartedAt.IsZero() {
		s.StartedAt = time.Now().UTC()
	}
	if s.Data == nil {
		s.Data = map[string]any{}
	}
	if s.Tools == nil {
		s.Tools = []storage.ToolCall{}
	}
	var cost any
	if s.CostUSD != nil {
		cost = *s.CostUSD
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO task_steps (`+stepCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.ID, s.TaskID, s.Seq, s.Phase, nullStr(s.AgentID), s.AgentKey, s.AgentName, s.Instruction, s.Output, toJSON(s.Data), toJSON(s.Tools),
		s.Status, s.Error, cost, nullStr(s.RunID), fmtTime(s.StartedAt), optTime(s.FinishedAt))
	return s, err
}

func (r taskRepo) UpdateStep(ctx context.Context, s storage.TaskStep) error {
	if s.Data == nil {
		s.Data = map[string]any{}
	}
	if s.Tools == nil {
		s.Tools = []storage.ToolCall{}
	}
	var cost any
	if s.CostUSD != nil {
		cost = *s.CostUSD
	}
	return execOne(ctx, r.db, `UPDATE task_steps SET output=?, data=?, tools=?, status=?, error=?, cost_usd=?, run_id=?, finished_at=? WHERE id=?`,
		s.Output, toJSON(s.Data), toJSON(s.Tools), s.Status, s.Error, cost, nullStr(s.RunID), optTime(s.FinishedAt), s.ID)
}

func (r taskRepo) ListSteps(ctx context.Context, taskID string) ([]storage.TaskStep, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+stepCols+` FROM task_steps WHERE task_id=? ORDER BY seq, started_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.TaskStep
	for rows.Next() {
		var (
			s                    storage.TaskStep
			agent, run           sql.NullString
			data, tools, started string
			finished             sql.NullString
			cost                 sql.NullFloat64
		)
		if err := rows.Scan(&s.ID, &s.TaskID, &s.Seq, &s.Phase, &agent, &s.AgentKey, &s.AgentName, &s.Instruction, &s.Output, &data, &tools,
			&s.Status, &s.Error, &cost, &run, &started, &finished); err != nil {
			return nil, err
		}
		s.AgentID, s.RunID = agent.String, run.String
		_ = json.Unmarshal([]byte(data), &s.Data)
		_ = json.Unmarshal([]byte(tools), &s.Tools)
		if cost.Valid {
			c := cost.Float64
			s.CostUSD = &c
		}
		var err error
		if s.StartedAt, err = parseTime(started); err != nil {
			return nil, err
		}
		if finished.Valid {
			f, err := parseTime(finished.String)
			if err != nil {
				return nil, err
			}
			s.FinishedAt = &f
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r taskRepo) ListPatches(ctx context.Context, taskID string) ([]storage.Patch, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+patchCols+` FROM patches WHERE task_id=? ORDER BY created_at, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Patch
	for rows.Next() {
		p, err := scanPatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r taskRepo) FailRunning(ctx context.Context, detail string, at time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE tasks SET status='failed', detail=?, finished_at=? WHERE status='running'`, detail, fmtTime(at))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
