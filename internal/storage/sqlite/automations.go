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
	res, err := r.db.ExecContext(ctx, `UPDATE automations SET name=?, enabled=?, source=?, config=?, action=?, agent_id=?, prompt=?, edit_mode=?,
		keep_context=?, limits=?, failures=?, disabled_code=?, disabled_reason=?, last_run_at=?, next_run_at=?, updated_at=?, script=?, escalate=?, model_tier=?,
		permission_mode=?, override_full_access=?, override_admin_by=?, override_extra_dirs=? WHERE id=? AND updated_at=?`,
		a.Name, a.Enabled, a.Source, toJSON(a.Config), a.Action, a.AgentID, a.Prompt, a.EditMode, a.KeepContext, toJSON(a.Limits),
		a.Failures, a.DisabledCode, a.DisabledReason, optTime(a.LastRunAt), optTime(a.NextRunAt), fmtTime(time.Now()), toJSON(a.Script), toJSON(a.Escalate), a.ModelTier,
		a.PermissionMode, a.OverrideFullAccess, a.OverrideAdminBy, toJSON(a.OverrideExtraDirs), a.ID, fmtTime(a.UpdatedAt))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return storage.ErrConflict
	}
	return nil
}

// SetNextRun writes next_run_at only (see AutomationRepo.SetNextRun).
func (r automationRepo) SetNextRun(ctx context.Context, id string, nextRunAt *time.Time) error {
	return execOne(ctx, r.db, `UPDATE automations SET next_run_at=?, updated_at=? WHERE id=?`,
		optTime(nextRunAt), fmtTime(time.Now()), id)
}

// SetWake writes next_run_at and config.wake_at (see AutomationRepo.SetWake).
func (r automationRepo) SetWake(ctx context.Context, id string, at *time.Time) error {
	if at == nil {
		return execOne(ctx, r.db, `UPDATE automations SET config=json_remove(config, '$.wake_at'), updated_at=? WHERE id=?`, fmtTime(time.Now()), id)
	}
	return execOne(ctx, r.db, `UPDATE automations SET next_run_at=?, config=json_set(config, '$.wake_at', ?), updated_at=? WHERE id=?`,
		optTime(at), at.UTC().Format(time.RFC3339Nano), fmtTime(time.Now()), id)
}

// Disable turns an automation off with a reason (see AutomationRepo.Disable).
func (r automationRepo) Disable(ctx context.Context, id, code, reason string, nextRunAt *time.Time) error {
	return execOne(ctx, r.db, `UPDATE automations SET enabled=0, disabled_code=?, disabled_reason=?, next_run_at=?, updated_at=? WHERE id=?`,
		code, reason, optTime(nextRunAt), fmtTime(time.Now()), id)
}

// UpdateSpec writes the proposal-editable columns only (storage.AutomationRepo,
// limits included: trigger.Spec.Apply keeps the old ones when a spec has none),
// leaving failures/enabled/disabled_code/disabled_reason/last_run_at as
// RecordRun or Tick last set them. config's conversation_id and secret_hash
// are read from the row's current config (not the possibly-stale a.Config),
// so a conversation kept by a just-finished run, or a secret just rotated by
// UpdateSecretHash, survives this write.
func (r automationRepo) UpdateSpec(ctx context.Context, a storage.Automation) error {
	cfg := a.Config
	cfg.ConversationID, cfg.SecretHash = "", ""
	return execOne(ctx, r.db, `UPDATE automations SET name=?, source=?, action=?, agent_id=?, prompt=?,
		config = json_set(?, '$.conversation_id', json_extract(config, '$.conversation_id'), '$.secret_hash', json_extract(config, '$.secret_hash')),
		script=?, escalate=?, limits=?, next_run_at=?, updated_at=? WHERE id=?`,
		a.Name, a.Source, a.Action, a.AgentID, a.Prompt, toJSON(cfg), toJSON(a.Script), toJSON(a.Escalate), toJSON(a.Limits), optTime(a.NextRunAt), fmtTime(time.Now()), a.ID)
}

func (r automationRepo) RecordRun(ctx context.Context, id string, now time.Time, result, failReason string, limit int, keptConv string) error {
	if limit <= 0 {
		limit = 5
	}
	ok, fail := result == "ok", result == "fail"
	return execOne(ctx, r.db, `UPDATE automations SET
		last_run_at = ?,
		failures = CASE WHEN ? THEN 0 WHEN ? THEN failures + 1 ELSE failures END,
		enabled = CASE WHEN ? AND failures + 1 >= ? THEN 0 ELSE enabled END,
		disabled_code = CASE WHEN ? AND failures + 1 >= ? THEN 'failures' ELSE disabled_code END,
		disabled_reason = CASE WHEN ? AND failures + 1 >= ? THEN ? ELSE disabled_reason END,
		config = CASE WHEN ? <> '' THEN json_set(config, '$.conversation_id', ?) ELSE config END,
		updated_at = ?
		WHERE id = ?`,
		optTime(&now), ok, fail,
		fail, limit, fail, limit, fail, limit, failReason,
		keptConv, keptConv,
		fmtTime(time.Now()), id)
}

func (r automationRepo) UpdateSecretHash(ctx context.Context, id, secretHash string) error {
	return execOne(ctx, r.db, `UPDATE automations SET config = json_set(config, '$.secret_hash', ?), updated_at = ? WHERE id = ?`,
		secretHash, fmtTime(time.Now()), id)
}

func (r automationRepo) Reenable(ctx context.Context, id string, nextRunAt *time.Time) error {
	return execOne(ctx, r.db, `UPDATE automations SET enabled=1, failures=0, disabled_code='', disabled_reason='', next_run_at=?, updated_at=? WHERE id=?`,
		optTime(nextRunAt), fmtTime(time.Now()), id)
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
