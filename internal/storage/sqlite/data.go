package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

// Data cleanup (ADR-095): sizes are the bytes of the text kept (content,
// tool calls, attachments' records, diffs), not of the database file.

type dataRepo struct{ db dbtx }

func (s *Store) Data() storage.DataRepo { return dataRepo{s.q} }

func lb(col string) string { return "length(CAST(" + col + " AS BLOB))" }

// per chat: its messages and its diffs
var convSizes = `LEFT JOIN (SELECT conversation_id, COUNT(*) n, SUM(` + lb("content") + `+` + lb("tools") + `+` + lb("attachments") + `+` + lb("context") + `) b
		FROM messages GROUP BY conversation_id) m ON m.conversation_id = c.id
	LEFT JOIN (SELECT conversation_id, SUM(` + lb("diff") + `) b FROM patches WHERE conversation_id IS NOT NULL GROUP BY conversation_id) p ON p.conversation_id = c.id`

// per task: its steps, its diffs and its follow-up chat's messages
var taskSizes = `LEFT JOIN (SELECT task_id, COUNT(*) n, SUM(` + lb("instruction") + `+` + lb("output") + `+` + lb("tools") + `+` + lb("data") + `) b
		FROM task_steps GROUP BY task_id) s ON s.task_id = t.id
	LEFT JOIN (SELECT task_id, SUM(` + lb("diff") + `) b FROM patches WHERE task_id IS NOT NULL GROUP BY task_id) p ON p.task_id = t.id
	LEFT JOIN (SELECT c.task_id, COUNT(m.id) n, SUM(` + lb("m.content") + `+` + lb("m.tools") + `) b
		FROM conversations c JOIN messages m ON m.conversation_id = c.id WHERE c.task_id IS NOT NULL GROUP BY c.task_id) cm ON cm.task_id = t.id`

const convKind = `CASE WHEN c.purpose = 'burn' THEN 'burn' ELSE 'chat' END`

func (r dataRepo) Usage(ctx context.Context) ([]storage.DataUsage, error) {
	q := `SELECT c.project_id, ` + convKind + `, COUNT(*), SUM(c.cleaned <> ''), COALESCE(SUM(m.n), 0), COALESCE(SUM(m.b), 0) + COALESCE(SUM(p.b), 0)
		FROM conversations c ` + convSizes + ` WHERE c.task_id IS NULL GROUP BY 1, 2
	UNION ALL
	SELECT t.project_id, 'task', COUNT(*), SUM(t.cleaned <> ''), COALESCE(SUM(s.n), 0) + COALESCE(SUM(cm.n), 0),
		COALESCE(SUM(s.b), 0) + COALESCE(SUM(p.b), 0) + COALESCE(SUM(cm.b), 0) + COALESCE(SUM(` + lb("t.result") + `+` + lb("t.detail") + `), 0)
		FROM tasks t ` + taskSizes + ` WHERE t.status <> 'running' GROUP BY 1`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.DataUsage{}
	for rows.Next() {
		var u storage.DataUsage
		if err := rows.Scan(&u.ProjectID, &u.Kind, &u.Items, &u.Cleaned, &u.Messages, &u.Bytes); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r dataRepo) Items(ctx context.Context, f storage.DataFilter) ([]storage.DataItem, error) {
	want := func(k string) bool { return len(f.Kinds) == 0 || slices.Contains(f.Kinds, k) }
	var out []storage.DataItem
	if want(storage.DataChat) || want(storage.DataBurn) {
		where, args := []string{"c.task_id IS NULL"}, []any{}
		where, args = dataWhere(where, args, f, "c.project_id", "c.updated_at", "c.cleaned", "c.id")
		rows, err := r.db.QueryContext(ctx, `SELECT `+convKind+`, c.id, c.project_id, c.title, c.cleaned, c.updated_at, COALESCE(m.n, 0), COALESCE(m.b, 0) + COALESCE(p.b, 0)
			FROM conversations c `+convSizes+` WHERE `+strings.Join(where, " AND "), args...)
		if err != nil {
			return nil, err
		}
		got, err := scanItems(rows)
		if err != nil {
			return nil, err
		}
		for _, it := range got {
			if want(it.Kind) {
				out = append(out, it)
			}
		}
	}
	if want(storage.DataTask) {
		where, args := []string{"t.status <> 'running'"}, []any{}
		where, args = dataWhere(where, args, f, "t.project_id", "COALESCE(t.finished_at, t.created_at)", "t.cleaned", "t.id")
		rows, err := r.db.QueryContext(ctx, `SELECT 'task', t.id, t.project_id, CASE WHEN t.title <> '' THEN t.title ELSE substr(t.goal, 1, 120) END, t.cleaned,
			COALESCE(t.finished_at, t.created_at), COALESCE(s.n, 0) + COALESCE(cm.n, 0),
			COALESCE(s.b, 0) + COALESCE(p.b, 0) + COALESCE(cm.b, 0) + `+lb("t.result")+` + `+lb("t.detail")+`
			FROM tasks t `+taskSizes+` WHERE `+strings.Join(where, " AND "), args...)
		if err != nil {
			return nil, err
		}
		got, err := scanItems(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) }) // the oldest first
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func dataWhere(where []string, args []any, f storage.DataFilter, project, updated, cleaned, id string) ([]string, []any) {
	if f.ProjectID != "" {
		where, args = append(where, project+" = ?"), append(args, f.ProjectID)
	}
	if !f.Before.IsZero() {
		where, args = append(where, updated+" < ?"), append(args, fmtTime(f.Before))
	}
	if !f.IncludeCleaned {
		where = append(where, cleaned+" = ''")
	}
	if len(f.IDs) > 0 {
		where = append(where, id+" IN ("+placeholders(len(f.IDs))+")")
		for _, x := range f.IDs {
			args = append(args, x)
		}
	}
	return where, args
}

func scanItems(rows *sql.Rows) ([]storage.DataItem, error) {
	defer rows.Close()
	var out []storage.DataItem
	for rows.Next() {
		var (
			it      storage.DataItem
			updated string
		)
		if err := rows.Scan(&it.Kind, &it.ID, &it.ProjectID, &it.Title, &it.Cleaned, &updated, &it.Messages, &it.Bytes); err != nil {
			return nil, err
		}
		it.UpdatedAt, _ = parseTime(updated)
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r dataRepo) AttachmentIDs(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT json_extract(j.value, '$.id') FROM messages, json_each(messages.attachments) j WHERE messages.attachments <> '[]'
		UNION SELECT json_extract(j.value, '$.id') FROM tasks, json_each(tasks.attachments) j WHERE tasks.attachments <> '[]'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id sql.NullString
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id.Valid && id.String != "" {
			out = append(out, id.String)
		}
	}
	return out, rows.Err()
}

func (r chatRepo) CleanConversation(ctx context.Context, id, state, note string) error {
	if _, err := r.GetConversation(ctx, id); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, q := range []string{
		`DELETE FROM patches WHERE conversation_id = ?`,
		`DELETE FROM messages WHERE conversation_id = ?`,
		`UPDATE conversation_agents SET session_id = '', last_message_id = '', context_tokens = 0 WHERE conversation_id = ?`,
	} {
		if _, err := r.db.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO messages (id, conversation_id, role, content, tools, attachments, author, created_at, context) VALUES (?,?,'assistant',?,'[]','[]','Office',?,'')`,
		ids.New("msg"), id, note, fmtTime(now)); err != nil {
		return err
	}
	if err := execOne(ctx, r.db, `UPDATE conversations SET cleaned = ?, cleaned_at = ?, session_id = '', context_tokens = 0 WHERE id = ?`, state, fmtTime(now), id); err != nil {
		return err
	}
	r.tell(storage.Change{Kind: "conversation", ConversationID: id})
	return nil
}

func (r taskRepo) CleanTask(ctx context.Context, id, state, result string) error {
	for _, q := range []string{`DELETE FROM patches WHERE task_id = ?`, `DELETE FROM task_steps WHERE task_id = ?`} {
		if _, err := r.db.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	return execOne(ctx, r.db, `UPDATE tasks SET result = ?, detail = '', cleaned = ? WHERE id = ?`, result, state, id)
}
