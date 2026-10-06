package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type burnRepo struct{ db dbtx }

func (s *Store) Burn() storage.BurnRepo { return burnRepo{s.q} }

const burnSessionCols = `id, project_id, conversation_id, agent_id, model_tier, max_subagents, result_mode, focus, work_order, ends_at, state, waiting_until, started_by, started_at, created_at, updated_at`

func scanBurnSession(row interface{ Scan(...any) error }) (storage.BurnSession, error) {
	var s storage.BurnSession
	var ends, waiting, started sql.NullString
	var created, updated string
	err := row.Scan(&s.ID, &s.ProjectID, &s.ConversationID, &s.AgentID, &s.ModelTier, &s.MaxSubagents, &s.ResultMode, &s.Focus, &s.Order, &ends, &s.State, &waiting, &s.StartedBy, &started, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return s, storage.ErrNotFound
	}
	if err != nil {
		return s, err
	}
	s.EndsAt, s.WaitingUntil, s.StartedAt = burnOpt(ends), burnOpt(waiting), burnOpt(started)
	return s, parseTimes([]*time.Time{&s.CreatedAt, &s.UpdatedAt}, created, updated)
}

func burnOpt(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := parseTime(v.String)
	if err != nil {
		return nil
	}
	return &t
}

func (r burnRepo) Session(ctx context.Context, projectID string) (storage.BurnSession, error) {
	return scanBurnSession(r.db.QueryRowContext(ctx, `SELECT `+burnSessionCols+` FROM burn_sessions WHERE project_id=?`, projectID))
}

func (r burnRepo) SessionByID(ctx context.Context, id string) (storage.BurnSession, error) {
	return scanBurnSession(r.db.QueryRowContext(ctx, `SELECT `+burnSessionCols+` FROM burn_sessions WHERE id=?`, id))
}

func (r burnRepo) SessionByConversation(ctx context.Context, conversationID string) (storage.BurnSession, error) {
	if conversationID == "" {
		return storage.BurnSession{}, storage.ErrNotFound
	}
	return scanBurnSession(r.db.QueryRowContext(ctx, `SELECT `+burnSessionCols+` FROM burn_sessions WHERE conversation_id=?`, conversationID))
}

func (r burnRepo) SaveSession(ctx context.Context, s storage.BurnSession) (storage.BurnSession, error) {
	now := time.Now().UTC()
	if s.ID == "" {
		s.ID, s.CreatedAt = ids.New("brn"), now
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	s.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `INSERT INTO burn_sessions (`+burnSessionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET conversation_id=excluded.conversation_id, agent_id=excluded.agent_id, model_tier=excluded.model_tier,
		max_subagents=excluded.max_subagents, result_mode=excluded.result_mode, focus=excluded.focus, work_order=excluded.work_order, ends_at=excluded.ends_at, state=excluded.state,
		waiting_until=excluded.waiting_until, started_by=excluded.started_by, started_at=excluded.started_at, updated_at=excluded.updated_at`,
		s.ID, s.ProjectID, s.ConversationID, s.AgentID, s.ModelTier, s.MaxSubagents, s.ResultMode, s.Focus, cmp.Or(s.Order, "roadmap"), optTime(s.EndsAt), s.State, optTime(s.WaitingUntil),
		s.StartedBy, optTime(s.StartedAt), fmtTime(s.CreatedAt), fmtTime(s.UpdatedAt))
	if isUnique(err) {
		return s, storage.ErrConflict
	}
	return s, err
}

func (r burnRepo) Running(ctx context.Context) ([]storage.BurnSession, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+burnSessionCols+` FROM burn_sessions WHERE state IN ('running','waiting_limit')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.BurnSession
	for rows.Next() {
		s, err := scanBurnSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const burnItemCols = `id, session_id, title, kind, detail, status, priority, branch, worktree, summary, attempts, subagents, cost_usd, created_at, updated_at`

func scanBurnItem(row interface{ Scan(...any) error }) (storage.BurnItem, error) {
	var it storage.BurnItem
	var created, updated string
	err := row.Scan(&it.ID, &it.SessionID, &it.Title, &it.Kind, &it.Detail, &it.Status, &it.Priority, &it.Branch, &it.Worktree, &it.Summary, &it.Attempts, &it.Subagents, &it.CostUSD, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return it, storage.ErrNotFound
	}
	if err != nil {
		return it, err
	}
	return it, parseTimes([]*time.Time{&it.CreatedAt, &it.UpdatedAt}, created, updated)
}

func (r burnRepo) AddItem(ctx context.Context, it storage.BurnItem) (storage.BurnItem, error) {
	now := time.Now().UTC()
	it.ID, it.CreatedAt, it.UpdatedAt = ids.New("bit"), now, now
	if it.Status == "" {
		it.Status = "found"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO burn_items (`+burnItemCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.ID, it.SessionID, it.Title, it.Kind, it.Detail, it.Status, it.Priority, it.Branch, it.Worktree, it.Summary, it.Attempts, it.Subagents, it.CostUSD, fmtTime(now), fmtTime(now))
	return it, err
}

func (r burnRepo) UpdateItem(ctx context.Context, it storage.BurnItem) error {
	return execOne(ctx, r.db, `UPDATE burn_items SET title=?, kind=?, detail=?, status=?, priority=?, branch=?, worktree=?, summary=?, attempts=?, subagents=?, cost_usd=?, updated_at=? WHERE id=?`,
		it.Title, it.Kind, it.Detail, it.Status, it.Priority, it.Branch, it.Worktree, it.Summary, it.Attempts, it.Subagents, it.CostUSD, fmtTime(time.Now()), it.ID)
}

func (r burnRepo) UpdateItemFrom(ctx context.Context, it storage.BurnItem, fromStatus string) error {
	if err := execOne(ctx, r.db, `UPDATE burn_items SET title=?, kind=?, detail=?, status=?, priority=?, branch=?, worktree=?, summary=?, attempts=?, subagents=?, cost_usd=?, updated_at=? WHERE id=? AND status=?`,
		it.Title, it.Kind, it.Detail, it.Status, it.Priority, it.Branch, it.Worktree, it.Summary, it.Attempts, it.Subagents, it.CostUSD, fmtTime(time.Now()), it.ID, fromStatus); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.ErrConflict
		}
		return err
	}
	return nil
}

func (r burnRepo) Item(ctx context.Context, id string) (storage.BurnItem, error) {
	return scanBurnItem(r.db.QueryRowContext(ctx, `SELECT `+burnItemCols+` FROM burn_items WHERE id=?`, id))
}

func (r burnRepo) Items(ctx context.Context, sessionID string) ([]storage.BurnItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+burnItemCols+` FROM burn_items WHERE session_id=? ORDER BY priority DESC, created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.BurnItem{}
	for rows.Next() {
		it, err := scanBurnItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
