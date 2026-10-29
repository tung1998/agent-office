package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

type harnessRepo struct{ db dbtx }

func (s *Store) Harness() storage.HarnessRepo { return harnessRepo{s.q} }

// each list read is capped: a busy project's month stays a few MB
const harnessCap = 20000

func (r harnessRepo) Facts(ctx context.Context, projectID string, since time.Time) (storage.HarnessFacts, error) {
	var f storage.HarnessFacts
	from := fmtTime(since)
	var created string
	if err := r.each(ctx, func(rows *sql.Rows) error {
		var p storage.HarnessPatch
		if err := rows.Scan(&p.ID, &p.ConversationID, &p.Status, &p.DecidedBy, &p.Detail, &p.Files, &created); err != nil {
			return err
		}
		f.Patches = append(f.Patches, p)
		return setTime(&f.Patches[len(f.Patches)-1].CreatedAt, created)
	}, `SELECT p.id, COALESCE(p.conversation_id, ''), p.status, p.decided_by, substr(p.detail, 1, 300), json_array_length(p.files), p.created_at
		FROM patches p LEFT JOIN conversations c ON c.id = p.conversation_id LEFT JOIN tasks t ON t.id = p.task_id
		WHERE COALESCE(c.project_id, t.project_id) = ? AND p.created_at >= ? ORDER BY p.created_at LIMIT ?`, projectID, from, harnessCap); err != nil {
		return f, err
	}
	if err := r.each(ctx, func(rows *sql.Rows) error {
		var a storage.HarnessAction
		var decided string
		if err := rows.Scan(&a.ID, &a.ConversationID, &a.Kind, &a.Target, &a.Status, &a.DecidedBy, &a.Detail, &created, &decided); err != nil {
			return err
		}
		if decided != "" {
			if err := setTime(&a.DecidedAt, decided); err != nil {
				return err
			}
		}
		f.Actions = append(f.Actions, a)
		return setTime(&f.Actions[len(f.Actions)-1].CreatedAt, created)
	}, `SELECT id, COALESCE(conversation_id, ''), kind, substr(target, 1, 300), status, decided_by, substr(detail, 1, 300), created_at, COALESCE(decided_at, '')
		FROM actions WHERE project_id = ? AND created_at >= ? ORDER BY created_at LIMIT ?`, projectID, from, harnessCap); err != nil {
		return f, err
	}
	if err := r.each(ctx, func(rows *sql.Rows) error {
		var j storage.HarnessJob
		if err := rows.Scan(&j.ID, &j.ConversationID, &j.Kind, &j.Status, &j.ErrorCode, &j.Error, &j.AgentID, &created); err != nil {
			return err
		}
		f.Jobs = append(f.Jobs, j)
		return setTime(&f.Jobs[len(f.Jobs)-1].CreatedAt, created)
	}, `SELECT id, COALESCE(conversation_id, ''), kind, status, error_code, substr(error, 1, 300), agent_id, created_at
		FROM jobs WHERE project_id = ? AND created_at >= ? ORDER BY created_at LIMIT ?`, projectID, from, harnessCap); err != nil {
		return f, err
	}
	if err := r.each(ctx, func(rows *sql.Rows) error {
		var t storage.HarnessTools
		if err := rows.Scan(&t.ConversationID, &t.Tools, &created); err != nil {
			return err
		}
		f.Tools = append(f.Tools, t)
		return setTime(&f.Tools[len(f.Tools)-1].CreatedAt, created)
	}, `SELECT m.conversation_id, m.tools, m.created_at FROM messages m JOIN conversations c ON c.id = m.conversation_id
		WHERE c.project_id = ? AND m.created_at >= ? AND m.role = 'assistant' AND m.tools != '[]' ORDER BY m.created_at LIMIT ?`, projectID, from, harnessCap); err != nil {
		return f, err
	}
	if err := r.each(ctx, func(rows *sql.Rows) error {
		var m storage.HarnessMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Content, &created); err != nil {
			return err
		}
		f.UserMessages = append(f.UserMessages, m)
		return setTime(&f.UserMessages[len(f.UserMessages)-1].CreatedAt, created)
	}, `SELECT m.id, m.conversation_id, substr(m.content, 1, 400), m.created_at FROM messages m JOIN conversations c ON c.id = m.conversation_id
		WHERE c.project_id = ? AND m.created_at >= ? AND m.role = 'user'
		AND (m.author LIKE 'human:%' OR m.author LIKE 'discord:%' OR m.author LIKE 'telegram:%') ORDER BY m.created_at LIMIT ?`, projectID, from, harnessCap); err != nil {
		return f, err
	}
	if err := r.each(ctx, func(rows *sql.Rows) error {
		var b storage.HarnessBurnItem
		if err := rows.Scan(&b.Title, &b.Kind, &b.Status, &b.Reviewed, &b.ReviewNote, &b.Attempts); err != nil {
			return err
		}
		f.Burn = append(f.Burn, b)
		return nil
	}, `SELECT i.title, i.kind, i.status, i.reviewed, substr(i.review_note, 1, 300), i.attempts
		FROM burn_items i JOIN burn_sessions s ON s.id = i.session_id
		WHERE s.project_id = ? AND i.created_at >= ? ORDER BY i.created_at LIMIT ?`, projectID, from, harnessCap); err != nil {
		return f, err
	}
	if err := r.db.QueryRowContext(ctx, `SELECT lessons FROM burn_sessions WHERE project_id = ?`, projectID).Scan(&f.BurnLessons); err != nil && err != sql.ErrNoRows {
		return f, err
	}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(created_at >= ?), 0) FROM agent_memories WHERE project_id = ?`, from, projectID).Scan(&f.Memories, &f.NewMemories); err != nil {
		return f, err
	}
	err := r.each(ctx, func(rows *sql.Rows) error {
		var t string
		if err := rows.Scan(&t); err != nil {
			return err
		}
		f.MemoryTopics = append(f.MemoryTopics, t)
		return nil
	}, `SELECT DISTINCT topic FROM agent_memories WHERE project_id = ? AND topic != '' ORDER BY topic`, projectID)
	return f, err
}

func (r harnessRepo) each(ctx context.Context, fn func(*sql.Rows) error, q string, args ...any) error {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func setTime(dst *time.Time, s string) error {
	t, err := parseTime(s)
	*dst = t
	return err
}
