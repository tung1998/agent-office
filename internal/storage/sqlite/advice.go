package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type adviceRepo struct{ db dbtx }

const adviceCols = `id, project_id, conversation_id, job_id, message_id, agent_id, agent_name, advisor_id, advisor_name,
	provider, model, trigger, command, error, answer, objection, status, reason, cost_usd, created_at`

func scanAdvice(row scanner) (storage.Advice, error) {
	var (
		a       storage.Advice
		obj     int
		cost    sql.NullFloat64
		created string
	)
	if err := row.Scan(&a.ID, &a.ProjectID, &a.ConversationID, &a.JobID, &a.MessageID, &a.AgentID, &a.AgentName, &a.AdvisorID, &a.AdvisorName,
		&a.Provider, &a.Model, &a.Trigger, &a.Command, &a.Error, &a.Answer, &obj, &a.Status, &a.Reason, &cost, &created); err != nil {
		return a, notFound(err)
	}
	a.Objection = obj != 0
	if cost.Valid {
		v := cost.Float64
		a.CostUSD = &v
	}
	var err error
	a.CreatedAt, err = parseTime(created)
	return a, err
}

func (r adviceRepo) Create(ctx context.Context, a storage.Advice) (storage.Advice, error) {
	if a.ID == "" {
		a.ID = ids.New("adv")
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	obj := 0
	if a.Objection {
		obj = 1
	}
	var cost any
	if a.CostUSD != nil {
		cost = *a.CostUSD
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO advice (`+adviceCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ProjectID, a.ConversationID, a.JobID, a.MessageID, a.AgentID, a.AgentName, a.AdvisorID, a.AdvisorName,
		a.Provider, a.Model, a.Trigger, a.Command, a.Error, a.Answer, obj, a.Status, a.Reason, cost, fmtTime(a.CreatedAt))
	return a, err
}

func (r adviceRepo) Conversation(ctx context.Context, conversationID string, limit int) ([]storage.Advice, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+adviceCols+` FROM (SELECT * FROM advice WHERE conversation_id = ? ORDER BY created_at DESC, id DESC LIMIT ?) ORDER BY created_at, id`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Advice
	for rows.Next() {
		a, err := scanAdvice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r adviceRepo) SetMessage(ctx context.Context, jobID, messageID string) error {
	if jobID == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `UPDATE advice SET message_id = ? WHERE job_id = ? AND message_id = ''`, messageID, jobID)
	return err
}

func (r adviceRepo) SpentSince(ctx context.Context, t time.Time) (float64, error) {
	var v sql.NullFloat64
	err := r.db.QueryRowContext(ctx, `SELECT SUM(cost_usd) FROM advice WHERE created_at >= ?`, fmtTime(t)).Scan(&v)
	return v.Float64, err
}

func (r adviceRepo) Stats(ctx context.Context, since time.Time) (storage.AdviceStats, error) {
	var (
		s    storage.AdviceStats
		cost sql.NullFloat64
	)
	err := r.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(status = 'answered'), 0),
		COALESCE(SUM(status <> 'answered'), 0),
		COALESCE(SUM(objection), 0),
		SUM(cost_usd)
		FROM advice WHERE created_at >= ?`, fmtTime(since)).Scan(&s.Asked, &s.Skipped, &s.Objected, &cost)
	if err != nil {
		return s, err
	}
	s.CostUSD = cost.Float64
	// the runs an answered advice went to, and how many of them ended done
	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(j.status = 'done'), 0)
		FROM (SELECT DISTINCT job_id FROM advice WHERE created_at >= ? AND status = 'answered' AND trigger = 'repeat' AND job_id <> '') a
		LEFT JOIN jobs j ON j.id = a.job_id`, fmtTime(since)).Scan(&s.Runs, &s.RunsDone)
	return s, err
}
