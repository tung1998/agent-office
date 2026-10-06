package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type runRepo struct{ db dbtx }

const runCols = `id, kind, project_id, agent_id, provider_id, provider_name, model, status, input_tokens, output_tokens,
	cost_usd, cost_source, duration_ms, error, actor, created_at, job_id`

func (r runRepo) Create(ctx context.Context, x storage.Run) (storage.Run, error) {
	if x.ID == "" {
		x.ID = ids.New("run")
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now().UTC()
	}
	if x.CostSource == "" {
		x.CostSource = "unknown"
	}
	var cost any
	if x.CostUSD != nil {
		cost = *x.CostUSD
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO runs (`+runCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		x.ID, x.Kind, nullStr(x.ProjectID), nullStr(x.AgentID), nullStr(x.ProviderID), x.ProviderName, x.Model, x.Status,
		x.InputTokens, x.OutputTokens, cost, x.CostSource, x.DurationMS, x.Error, x.Actor, fmtTime(x.CreatedAt), x.JobID)
	return x, err
}

func (r runRepo) List(ctx context.Context, f storage.RunFilter) ([]storage.Run, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	return r.query(ctx, f, f.Limit)
}

// query lists runs; limit 0 means all (used for aggregation).
func (r runRepo) query(ctx context.Context, f storage.RunFilter, limit int) ([]storage.Run, error) {
	q := `SELECT ` + runCols + ` FROM runs WHERE created_at >= ?`
	args := []any{fmtTime(f.Since)}
	if f.ProjectID != "" {
		q += ` AND project_id = ?`
		args = append(args, f.ProjectID)
	}
	if f.Before != "" {
		q += ` AND (created_at, id) < (SELECT created_at, id FROM runs WHERE id = ?)`
		args = append(args, f.Before)
	}
	q += ` ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Run
	for rows.Next() {
		var (
			x                        storage.Run
			project, agent, provider sql.NullString
			cost                     sql.NullFloat64
			created                  string
		)
		if err := rows.Scan(&x.ID, &x.Kind, &project, &agent, &provider, &x.ProviderName, &x.Model, &x.Status, &x.InputTokens,
			&x.OutputTokens, &cost, &x.CostSource, &x.DurationMS, &x.Error, &x.Actor, &created, &x.JobID); err != nil {
			return nil, err
		}
		x.ProjectID, x.AgentID, x.ProviderID = project.String, agent.String, provider.String
		if cost.Valid {
			c := cost.Float64
			x.CostUSD = &c
		}
		if x.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r runRepo) Spent(ctx context.Context, since time.Time, projectID string) (float64, error) {
	q := `SELECT COALESCE(SUM(cost_usd), 0) FROM runs WHERE created_at >= ?`
	args := []any{fmtTime(since)}
	if projectID != "" {
		q += ` AND project_id = ?`
		args = append(args, projectID)
	}
	var v float64
	err := r.db.QueryRowContext(ctx, q, args...).Scan(&v)
	return v, err
}

func (r runRepo) Aggregate(ctx context.Context, since time.Time, by string, loc *time.Location) ([]storage.UsageRow, error) {
	if loc == nil {
		loc = time.UTC
	}
	switch by {
	case "day":
		// Group in Go so days follow the office's timezone, not UTC.
		runs, err := r.all(ctx, since)
		if err != nil {
			return nil, err
		}
		idx := map[string]int{}
		var out []storage.UsageRow
		for _, x := range runs {
			day := x.CreatedAt.In(loc).Format("2006-01-02")
			i, ok := idx[day]
			if !ok {
				i = len(out)
				idx[day] = i
				out = append(out, storage.UsageRow{Key: day, Label: day})
			}
			addRun(&out[i], x)
		}
		return out, nil
	case "project":
		return r.group(ctx, since, `COALESCE(r.project_id, '')`, `COALESCE(p.name, '')`, `LEFT JOIN repos p ON p.id = r.project_id`)
	case "model":
		return r.group(ctx, since, `r.model`, `r.provider_name`, ``)
	}
	return nil, fmt.Errorf("aggregate by %q", by)
}

func (r runRepo) group(ctx context.Context, since time.Time, key, label, join string) ([]storage.UsageRow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+key+`, MAX(`+label+`), COUNT(*), SUM(r.input_tokens), SUM(r.output_tokens),
		COALESCE(SUM(r.cost_usd), 0), SUM(CASE WHEN r.cost_usd IS NULL AND r.status = 'ok' THEN 1 ELSE 0 END)
		FROM runs r `+join+` WHERE r.created_at >= ? GROUP BY 1 ORDER BY 6 DESC`, fmtTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.UsageRow
	for rows.Next() {
		var u storage.UsageRow
		if err := rows.Scan(&u.Key, &u.Label, &u.Runs, &u.InputTokens, &u.OutputTokens, &u.CostUSD, &u.UnknownCost); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r runRepo) Since(ctx context.Context, t time.Time) ([]storage.Run, error) {
	return r.all(ctx, t)
}

func (r runRepo) all(ctx context.Context, since time.Time) ([]storage.Run, error) {
	return r.query(ctx, storage.RunFilter{Since: since}, 0)
}

func addRun(u *storage.UsageRow, x storage.Run) {
	u.Runs++
	u.InputTokens += x.InputTokens
	u.OutputTokens += x.OutputTokens
	if x.CostUSD != nil {
		u.CostUSD += *x.CostUSD
	} else if x.Status == "ok" {
		u.UnknownCost++
	}
}

type settingRepo struct{ db dbtx }

func (r settingRepo) Get(ctx context.Context, key string, dst any) (bool, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), dst)
}

func (r settingRepo) Set(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, string(raw), fmtTime(time.Now()))
	return err
}
