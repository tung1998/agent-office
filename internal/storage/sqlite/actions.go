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

type actionRepo struct{ db dbtx }

const actionCols = `id, project_id, conversation_id, message_id, task_id, run_ref, kind, target, target_id, reason, status, detail,
	proposed_by, decided_by, decided_at, created_at, args, job_id`

func scanAction(row scanner) (storage.Action, error) {
	var (
		a               storage.Action
		conv, msg, task sql.NullString
		decided         sql.NullString
		created, args   string
	)
	if err := row.Scan(&a.ID, &a.ProjectID, &conv, &msg, &task, &a.RunRef, &a.Kind, &a.Target, &a.TargetID, &a.Reason, &a.Status, &a.Detail,
		&a.ProposedBy, &a.DecidedBy, &decided, &created, &args, &a.JobID); err != nil {
		return a, notFound(err)
	}
	_ = json.Unmarshal([]byte(args), &a.Args)
	a.ConversationID, a.MessageID, a.TaskID = conv.String, msg.String, task.String
	var err error
	if a.DecidedAt, err = optParse(decided); err != nil {
		return a, err
	}
	a.CreatedAt, err = parseTime(created)
	return a, err
}

func (r actionRepo) Create(ctx context.Context, a storage.Action) (storage.Action, error) {
	if a.ID == "" {
		a.ID = ids.New("act")
	}
	if a.Status == "" {
		a.Status = "pending"
	}
	a.CreatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `INSERT INTO actions (`+actionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ProjectID, nullStr(a.ConversationID), nullStr(a.MessageID), nullStr(a.TaskID), a.RunRef, a.Kind, a.Target, a.TargetID, a.Reason,
		a.Status, a.Detail, a.ProposedBy, a.DecidedBy, optTime(a.DecidedAt), fmtTime(a.CreatedAt), toJSON(a.Args), a.JobID)
	return a, err
}

func (r actionRepo) Update(ctx context.Context, a storage.Action) error {
	return execOne(ctx, r.db, `UPDATE actions SET message_id=?, status=?, detail=?, decided_by=?, decided_at=? WHERE id=?`,
		nullStr(a.MessageID), a.Status, a.Detail, a.DecidedBy, optTime(a.DecidedAt), a.ID)
}

func (r actionRepo) Get(ctx context.Context, id string) (storage.Action, error) {
	return scanAction(r.db.QueryRowContext(ctx, `SELECT `+actionCols+` FROM actions WHERE id=?`, id))
}

func (r actionRepo) List(ctx context.Context, conversationID, taskID, runRef string) ([]storage.Action, error) {
	var (
		col, val string
	)
	switch {
	case conversationID != "":
		col, val = "conversation_id", conversationID
	case taskID != "":
		col, val = "task_id", taskID
	case runRef != "":
		col, val = "run_ref", runRef
	default:
		return nil, errors.New("actions: missing filter")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+actionCols+` FROM actions WHERE `+col+`=? ORDER BY created_at, id`, val)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Action{}
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r actionRepo) Pending(ctx context.Context, limit int) ([]storage.Action, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+actionCols+` FROM actions WHERE status='pending' ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Action{}
	for rows.Next() {
		a, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
