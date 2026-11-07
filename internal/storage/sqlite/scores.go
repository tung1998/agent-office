package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

type scoreRepo struct{ db dbtx }

const scoreCols = `job_id, project_id, conversation_id, message_id, agent_id, provider_id, model, effort, task_kind, difficulty,
	label_manual, status, q, parts, feedback, cost_usd, duration_ms, finished_at, updated_at, frozen`

func scanScore(row scanner) (storage.JobScore, error) {
	var (
		s                   storage.JobScore
		manual, frozen      int
		q                   sql.NullInt64
		parts, fin, updated string
	)
	if err := row.Scan(&s.JobID, &s.ProjectID, &s.ConversationID, &s.MessageID, &s.AgentID, &s.ProviderID, &s.Model, &s.Effort,
		&s.TaskKind, &s.Difficulty, &manual, &s.Status, &q, &parts, &s.Feedback, &s.CostUSD, &s.DurationMS, &fin, &updated, &frozen); err != nil {
		return s, notFound(err)
	}
	s.LabelManual, s.Frozen = manual != 0, frozen != 0
	if q.Valid {
		v := int(q.Int64)
		s.Q = &v
	}
	if err := json.Unmarshal([]byte(parts), &s.Parts); err != nil {
		return s, err
	}
	var err error
	if s.FinishedAt, err = parseTime(fin); err != nil {
		return s, err
	}
	s.UpdatedAt, err = parseTime(updated)
	return s, err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r scoreRepo) Upsert(ctx context.Context, s storage.JobScore) error {
	if s.UpdatedAt.IsZero() {
		s.UpdatedAt = time.Now().UTC()
	}
	parts, err := json.Marshal(s.Parts)
	if err != nil {
		return err
	}
	var q any
	if s.Q != nil {
		q = *s.Q
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO job_scores (`+scoreCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (job_id) DO UPDATE SET conversation_id=excluded.conversation_id, message_id=excluded.message_id,
			agent_id=excluded.agent_id, provider_id=excluded.provider_id, model=excluded.model, effort=excluded.effort,
			task_kind=CASE WHEN job_scores.label_manual THEN job_scores.task_kind ELSE excluded.task_kind END,
			difficulty=CASE WHEN job_scores.label_manual THEN job_scores.difficulty ELSE excluded.difficulty END,
			label_manual=MAX(job_scores.label_manual, excluded.label_manual),
			status=excluded.status, q=excluded.q, parts=excluded.parts, feedback=excluded.feedback, cost_usd=excluded.cost_usd,
			duration_ms=excluded.duration_ms, finished_at=excluded.finished_at, updated_at=excluded.updated_at, frozen=excluded.frozen`,
		s.JobID, s.ProjectID, s.ConversationID, s.MessageID, s.AgentID, s.ProviderID, s.Model, s.Effort, s.TaskKind, s.Difficulty,
		b2i(s.LabelManual), s.Status, q, string(parts), s.Feedback, s.CostUSD, s.DurationMS, fmtTime(s.FinishedAt), fmtTime(s.UpdatedAt), b2i(s.Frozen))
	return err
}

func (r scoreRepo) Update(ctx context.Context, s storage.JobScore, prevUpdated time.Time) error {
	if !s.UpdatedAt.After(prevUpdated) { // the write must move the version on
		s.UpdatedAt = prevUpdated.Add(time.Millisecond)
	}
	parts, err := json.Marshal(s.Parts)
	if err != nil {
		return err
	}
	var q any
	if s.Q != nil {
		q = *s.Q
	}
	res, err := r.db.ExecContext(ctx, `UPDATE job_scores SET q=?, parts=?, feedback=?, frozen=?, updated_at=? WHERE job_id=? AND updated_at=?`,
		q, string(parts), s.Feedback, b2i(s.Frozen), fmtTime(s.UpdatedAt), s.JobID, fmtTime(prevUpdated))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return err
	}
	if _, err := r.Get(ctx, s.JobID); err != nil {
		return err
	}
	return storage.ErrStale
}

func (r scoreRepo) Get(ctx context.Context, jobID string) (storage.JobScore, error) {
	return scanScore(r.db.QueryRowContext(ctx, `SELECT `+scoreCols+` FROM job_scores WHERE job_id=?`, jobID))
}

func (r scoreRepo) ByMessage(ctx context.Context, messageID string) (storage.JobScore, error) {
	if messageID == "" {
		return storage.JobScore{}, storage.ErrNotFound
	}
	return scanScore(r.db.QueryRowContext(ctx, `SELECT `+scoreCols+` FROM job_scores WHERE message_id=? ORDER BY finished_at DESC LIMIT 1`, messageID))
}

func (r scoreRepo) list(ctx context.Context, q string, args ...any) ([]storage.JobScore, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.JobScore{}
	for rows.Next() {
		s, err := scanScore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r scoreRepo) List(ctx context.Context, f storage.ScoreFilter) ([]storage.JobScore, error) {
	q := `SELECT ` + scoreCols + ` FROM job_scores WHERE finished_at >= ?`
	args := []any{fmtTime(f.Since)}
	if f.ProjectID != "" {
		q += ` AND project_id = ?`
		args = append(args, f.ProjectID)
	}
	if f.ConversationID != "" {
		q += ` AND conversation_id = ?`
		args = append(args, f.ConversationID)
	}
	if f.AgentID != "" {
		q += ` AND agent_id = ?`
		args = append(args, f.AgentID)
	}
	return r.list(ctx, q+` ORDER BY finished_at DESC, job_id DESC LIMIT 5000`, args...)
}

func (r scoreRepo) Open(ctx context.Context, before time.Time) ([]storage.JobScore, error) {
	return r.list(ctx, `SELECT `+scoreCols+` FROM job_scores WHERE frozen = 0 AND finished_at < ? ORDER BY finished_at`, fmtTime(before))
}

func (r scoreRepo) SetLabels(ctx context.Context, jobID, kind, difficulty string) error {
	return execOne(ctx, r.db, `UPDATE job_scores SET task_kind=?, difficulty=?, label_manual=1, updated_at=? WHERE job_id=?`,
		kind, difficulty, fmtTime(time.Now().UTC()), jobID)
}
