package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type monitorRepo struct{ db dbtx }

const monitorCols = `id, project_id, name, type, target, config, interval_s, enabled, ai_enabled, ai_budget_usd, token, status, fails,
	last_checked_at, last_change_at, last_latency_ms, last_message, last_ping_at, created_at, updated_at`

func optParse(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	return &t, err
}

func scanMonitor(row scanner) (storage.Monitor, error) {
	var (
		m                     storage.Monitor
		cfg, created, updated string
		checked, change, ping sql.NullString
	)
	if err := row.Scan(&m.ID, &m.ProjectID, &m.Name, &m.Type, &m.Target, &cfg, &m.IntervalS, &m.Enabled, &m.AIEnabled, &m.AIBudgetUSD,
		&m.Token, &m.Status, &m.Fails, &checked, &change, &m.LastLatencyMS, &m.LastMessage, &ping, &created, &updated); err != nil {
		return m, notFound(err)
	}
	if err := json.Unmarshal([]byte(cfg), &m.Config); err != nil {
		return m, err
	}
	var err error
	if m.LastCheckedAt, err = optParse(checked); err != nil {
		return m, err
	}
	if m.LastChangeAt, err = optParse(change); err != nil {
		return m, err
	}
	if m.LastPingAt, err = optParse(ping); err != nil {
		return m, err
	}
	if m.CreatedAt, err = parseTime(created); err != nil {
		return m, err
	}
	m.UpdatedAt, err = parseTime(updated)
	return m, err
}

func (r monitorRepo) Create(ctx context.Context, m storage.Monitor) (storage.Monitor, error) {
	if m.ID == "" {
		m.ID = ids.New("mon")
	}
	if m.Status == "" {
		m.Status = "pending"
	}
	m.CreatedAt = time.Now().UTC()
	m.UpdatedAt = m.CreatedAt
	_, err := r.db.ExecContext(ctx, `INSERT INTO monitors (`+monitorCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ProjectID, m.Name, m.Type, m.Target, toJSON(m.Config), m.IntervalS, boolInt(m.Enabled), boolInt(m.AIEnabled), m.AIBudgetUSD,
		m.Token, m.Status, m.Fails, optTime(m.LastCheckedAt), optTime(m.LastChangeAt), m.LastLatencyMS, m.LastMessage, optTime(m.LastPingAt),
		fmtTime(m.CreatedAt), fmtTime(m.UpdatedAt))
	if isUnique(err) {
		return storage.Monitor{}, storage.ErrConflict
	}
	return m, err
}

func (r monitorRepo) Update(ctx context.Context, m storage.Monitor) error {
	err := execOne(ctx, r.db, `UPDATE monitors SET name=?, type=?, target=?, config=?, interval_s=?, enabled=?, ai_enabled=?, ai_budget_usd=?, token=?, updated_at=? WHERE id=?`,
		m.Name, m.Type, m.Target, toJSON(m.Config), m.IntervalS, boolInt(m.Enabled), boolInt(m.AIEnabled), m.AIBudgetUSD, m.Token, fmtTime(time.Now()), m.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

func (r monitorRepo) SaveStatus(ctx context.Context, m storage.Monitor) error {
	// the same result as last time: only when it was checked, and quietly (the
	// leading comment keeps it from the dashboard's live notices, ADR-072)
	res, err := r.db.ExecContext(ctx, `/* quiet */ UPDATE monitors SET last_checked_at=?, last_latency_ms=?, last_ping_at=? WHERE id=? AND status=? AND fails=? AND last_message=? AND last_change_at IS ?`,
		optTime(m.LastCheckedAt), m.LastLatencyMS, optTime(m.LastPingAt), m.ID, m.Status, m.Fails, m.LastMessage, optTime(m.LastChangeAt))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	return execOne(ctx, r.db, `UPDATE monitors SET status=?, fails=?, last_checked_at=?, last_change_at=?, last_latency_ms=?, last_message=?, last_ping_at=? WHERE id=?`,
		m.Status, m.Fails, optTime(m.LastCheckedAt), optTime(m.LastChangeAt), m.LastLatencyMS, m.LastMessage, optTime(m.LastPingAt), m.ID)
}

func (r monitorRepo) Get(ctx context.Context, id string) (storage.Monitor, error) {
	return scanMonitor(r.db.QueryRowContext(ctx, `SELECT `+monitorCols+` FROM monitors WHERE id=?`, id))
}

func (r monitorRepo) GetByToken(ctx context.Context, token string) (storage.Monitor, error) {
	if token == "" {
		return storage.Monitor{}, storage.ErrNotFound
	}
	return scanMonitor(r.db.QueryRowContext(ctx, `SELECT `+monitorCols+` FROM monitors WHERE token=?`, token))
}

func (r monitorRepo) List(ctx context.Context, projectID string) ([]storage.Monitor, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+monitorCols+` FROM monitors WHERE (?='' OR project_id=?) ORDER BY created_at, name`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r monitorRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM monitors WHERE id=?`, id)
}

func (r monitorRepo) AddCheck(ctx context.Context, c storage.MonitorCheck) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO monitor_checks (monitor_id, at, ok, latency_ms, message) VALUES (?,?,?,?,?)`,
		c.MonitorID, fmtTime(c.At), boolInt(c.OK), c.LatencyMS, c.Message)
	return err
}

func (r monitorRepo) Checks(ctx context.Context, monitorID string, since time.Time) ([]storage.MonitorCheck, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT monitor_id, at, ok, latency_ms, message FROM monitor_checks WHERE monitor_id=? AND at>=? ORDER BY at`,
		monitorID, fmtTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.MonitorCheck{}
	for rows.Next() {
		var (
			c  storage.MonitorCheck
			at string
		)
		if err := rows.Scan(&c.MonitorID, &at, &c.OK, &c.LatencyMS, &c.Message); err != nil {
			return nil, err
		}
		if c.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r monitorRepo) PruneChecks(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM monitor_checks WHERE at<?`, fmtTime(before))
	return err
}

const eventCols = `id, monitor_id, project_id, kind, message, analysis, analysis_status, cost_usd, at`

func (r monitorRepo) AddEvent(ctx context.Context, e storage.MonitorEvent) (storage.MonitorEvent, error) {
	if e.ID == "" {
		e.ID = ids.New("mev")
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO monitor_events (`+eventCols+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		e.ID, e.MonitorID, e.ProjectID, e.Kind, e.Message, e.Analysis, e.AnalysisStatus, e.CostUSD, fmtTime(e.At))
	return e, err
}

func (r monitorRepo) UpdateEvent(ctx context.Context, e storage.MonitorEvent) error {
	return execOne(ctx, r.db, `UPDATE monitor_events SET analysis=?, analysis_status=?, cost_usd=? WHERE id=?`, e.Analysis, e.AnalysisStatus, e.CostUSD, e.ID)
}

func (r monitorRepo) Events(ctx context.Context, projectID string, limit int) ([]storage.MonitorEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+eventCols+` FROM monitor_events WHERE (?='' OR project_id=?) ORDER BY at DESC LIMIT ?`, projectID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.MonitorEvent{}
	for rows.Next() {
		var (
			e  storage.MonitorEvent
			at string
		)
		if err := rows.Scan(&e.ID, &e.MonitorID, &e.ProjectID, &e.Kind, &e.Message, &e.Analysis, &e.AnalysisStatus, &e.CostUSD, &at); err != nil {
			return nil, err
		}
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r monitorRepo) AICostSince(ctx context.Context, monitorID string, since time.Time) (float64, error) {
	var total sql.NullFloat64
	err := r.db.QueryRowContext(ctx, `SELECT SUM(cost_usd) FROM monitor_events WHERE monitor_id=? AND at>=?`, monitorID, fmtTime(since)).Scan(&total)
	return total.Float64, err
}
