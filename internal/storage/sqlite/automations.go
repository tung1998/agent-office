package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type automationRepo struct{ db dbtx }

const automationCols = `id, project_id, name, enabled, source, config, action, agent_id, prompt, edit_mode, keep_context, limits,
	failures, disabled_code, disabled_reason, last_run_at, next_run_at, created_by, created_at, updated_at, script, escalate, model_tier,
	permission_mode, override_full_access, override_admin_by, override_extra_dirs`

func scanAutomation(row scanner) (storage.Automation, error) {
	var (
		a                storage.Automation
		cfg, limits      string
		script, escalate string
		last, next       sql.NullString
		created, updated string
		extraDirs        sql.NullString
	)
	if err := row.Scan(&a.ID, &a.ProjectID, &a.Name, &a.Enabled, &a.Source, &cfg, &a.Action, &a.AgentID, &a.Prompt, &a.EditMode, &a.KeepContext,
		&limits, &a.Failures, &a.DisabledCode, &a.DisabledReason, &last, &next, &a.CreatedBy, &created, &updated, &script, &escalate, &a.ModelTier,
		&a.PermissionMode, &a.OverrideFullAccess, &a.OverrideAdminBy, &extraDirs); err != nil {
		return a, notFound(err)
	}
	if err := json.Unmarshal([]byte(script), &a.Script); err != nil {
		return a, err
	}
	if err := json.Unmarshal([]byte(escalate), &a.Escalate); err != nil {
		return a, err
	}
	if err := json.Unmarshal([]byte(cfg), &a.Config); err != nil {
		return a, err
	}
	if err := json.Unmarshal([]byte(limits), &a.Limits); err != nil {
		return a, err
	}
	if extraDirs.Valid && extraDirs.String != "" {
		if err := json.Unmarshal([]byte(extraDirs.String), &a.OverrideExtraDirs); err != nil {
			return a, err
		}
	}
	if a.PermissionMode == "" {
		a.PermissionMode = "agent"
	}
	for _, x := range []struct {
		src sql.NullString
		dst **time.Time
	}{{last, &a.LastRunAt}, {next, &a.NextRunAt}} {
		if x.src.Valid && x.src.String != "" {
			t, err := parseTime(x.src.String)
			if err != nil {
				return a, err
			}
			*x.dst = &t
		}
	}
	return a, parseTimes([]*time.Time{&a.CreatedAt, &a.UpdatedAt}, created, updated)
}

func (r automationRepo) Create(ctx context.Context, a storage.Automation) (storage.Automation, error) {
	if a.ID == "" {
		a.ID = ids.New("aut")
	}
	now := time.Now().UTC()
	a.CreatedAt, a.UpdatedAt = now, now
	if a.EditMode == "" {
		a.EditMode = "worktree"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO automations (`+automationCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ProjectID, a.Name, a.Enabled, a.Source, toJSON(a.Config), a.Action, a.AgentID, a.Prompt, a.EditMode, a.KeepContext, toJSON(a.Limits),
		a.Failures, a.DisabledCode, a.DisabledReason, optTime(a.LastRunAt), optTime(a.NextRunAt), a.CreatedBy, fmtTime(now), fmtTime(now),
		toJSON(a.Script), toJSON(a.Escalate), a.ModelTier, a.PermissionMode, a.OverrideFullAccess, a.OverrideAdminBy, toJSON(a.OverrideExtraDirs))
	return a, err
}

func (r automationRepo) Get(ctx context.Context, id string) (storage.Automation, error) {
	return scanAutomation(r.db.QueryRowContext(ctx, `SELECT `+automationCols+` FROM automations WHERE id=?`, id))
}

func (r automationRepo) Update(ctx context.Context, a storage.Automation) error {
	return execOne(ctx, r.db, `UPDATE automations SET name=?, enabled=?, source=?, config=?, action=?, agent_id=?, prompt=?, edit_mode=?,
		keep_context=?, limits=?, failures=?, disabled_code=?, disabled_reason=?, last_run_at=?, next_run_at=?, updated_at=?, script=?, escalate=?, model_tier=?,
		permission_mode=?, override_full_access=?, override_admin_by=?, override_extra_dirs=? WHERE id=?`,
		a.Name, a.Enabled, a.Source, toJSON(a.Config), a.Action, a.AgentID, a.Prompt, a.EditMode, a.KeepContext, toJSON(a.Limits),
		a.Failures, a.DisabledCode, a.DisabledReason, optTime(a.LastRunAt), optTime(a.NextRunAt), fmtTime(time.Now()), toJSON(a.Script), toJSON(a.Escalate), a.ModelTier,
		a.PermissionMode, a.OverrideFullAccess, a.OverrideAdminBy, toJSON(a.OverrideExtraDirs), a.ID)
}

func (r automationRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM automations WHERE id=?`, id)
}

func (r automationRepo) list(ctx context.Context, q string, args ...any) ([]storage.Automation, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Automation{}
	for rows.Next() {
		a, err := scanAutomation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r automationRepo) List(ctx context.Context, projectID string) ([]storage.Automation, error) {
	return r.list(ctx, `SELECT `+automationCols+` FROM automations WHERE project_id=? ORDER BY created_at`, projectID)
}

func (r automationRepo) Due(ctx context.Context, now time.Time) ([]storage.Automation, error) {
	return r.list(ctx, `SELECT `+automationCols+` FROM automations
		WHERE enabled=1 AND source='schedule' AND next_run_at IS NOT NULL AND next_run_at <= ? ORDER BY next_run_at`, fmtTime(now))
}
