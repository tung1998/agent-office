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

type channelRepo struct{ db dbtx }

const channelCols = `id, project_id, kind, name, token_enc, agent_id, mode, enabled, allow, scope, filter_enabled, refusal,
	bot_name, last_error, last_message_at, created_at, updated_at, approvers, approval, header, reply_mode`

func scanChannel(row scanner) (storage.Channel, error) {
	var (
		c                storage.Channel
		allow, approvers string
		last             sql.NullString
		created, updated string
	)
	if err := row.Scan(&c.ID, &c.ProjectID, &c.Kind, &c.Name, &c.TokenEnc, &c.AgentID, &c.Mode, &c.Enabled, &allow, &c.Scope, &c.FilterEnabled, &c.Refusal,
		&c.BotName, &c.LastError, &last, &created, &updated, &approvers, &c.Approval, &c.Header, &c.ReplyMode); err != nil {
		return c, notFound(err)
	}
	_ = json.Unmarshal([]byte(allow), &c.Allow)
	_ = json.Unmarshal([]byte(approvers), &c.Approvers)
	var err error
	if c.LastMessageAt, err = optParse(last); err != nil {
		return c, err
	}
	return c, parseTimes([]*time.Time{&c.CreatedAt, &c.UpdatedAt}, created, updated)
}

func (r channelRepo) Create(ctx context.Context, c storage.Channel) (storage.Channel, error) {
	now := time.Now().UTC()
	c.ID, c.CreatedAt, c.UpdatedAt = ids.New("chn"), now, now
	if c.Mode == "" {
		c.Mode = "read"
	}
	normChannel(&c)
	_, err := r.db.ExecContext(ctx, `INSERT INTO channels (`+channelCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,?,?,?,?,?,?)`,
		c.ID, c.ProjectID, c.Kind, c.Name, c.TokenEnc, c.AgentID, c.Mode, c.Enabled, toJSON(c.Allow), c.Scope, c.FilterEnabled, c.Refusal,
		c.BotName, c.LastError, fmtTime(now), fmtTime(now), toJSON(c.Approvers), c.Approval, c.Header, c.ReplyMode)
	return c, err
}

func (r channelRepo) Update(ctx context.Context, c storage.Channel) error {
	normChannel(&c)
	return execOne(ctx, r.db, `UPDATE channels SET name=?, token_enc=?, agent_id=?, mode=?, enabled=?, allow=?, scope=?, filter_enabled=?, refusal=?, approvers=?, approval=?, header=?, reply_mode=?, updated_at=? WHERE id=?`,
		c.Name, c.TokenEnc, c.AgentID, c.Mode, c.Enabled, toJSON(c.Allow), c.Scope, c.FilterEnabled, c.Refusal, toJSON(c.Approvers), c.Approval, c.Header, c.ReplyMode, fmtTime(time.Now()), c.ID)
}

func (r channelRepo) Get(ctx context.Context, id string) (storage.Channel, error) {
	return scanChannel(r.db.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE id=?`, id))
}

func (r channelRepo) List(ctx context.Context, projectID string) ([]storage.Channel, error) {
	q, args := `SELECT `+channelCols+` FROM channels ORDER BY created_at`, []any{}
	if projectID != "" {
		q, args = `SELECT `+channelCols+` FROM channels WHERE project_id=? ORDER BY created_at`, []any{projectID}
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r channelRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM channels WHERE id=?`, id)
}

func (r channelRepo) SetStatus(ctx context.Context, id, botName, lastError string, lastMessage *time.Time) error {
	if lastMessage != nil {
		return execOne(ctx, r.db, `UPDATE channels SET bot_name=?, last_error=?, last_message_at=? WHERE id=?`, botName, lastError, fmtTime(*lastMessage), id)
	}
	return execOne(ctx, r.db, `UPDATE channels SET bot_name=?, last_error=? WHERE id=?`, botName, lastError, id)
}

func (r channelRepo) Thread(ctx context.Context, channelID, chatID string) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT conversation_id FROM channel_threads WHERE channel_id=? AND chat_id=?`, channelID, chatID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (r channelRepo) SetThread(ctx context.Context, channelID, chatID, conversationID string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO channel_threads (channel_id, chat_id, conversation_id) VALUES (?,?,?)
		ON CONFLICT (channel_id, chat_id) DO UPDATE SET conversation_id=excluded.conversation_id`, channelID, chatID, conversationID)
	return err
}

func normChannel(c *storage.Channel) {
	if c.Allow == nil {
		c.Allow = []string{}
	}
	if c.Approvers == nil {
		c.Approvers = []string{}
	}
	if c.Approval != "direct" && c.Approval != "admin" { // admin: the bot set up that way (ADR-071)
		c.Approval = "ask"
	}
	if c.ReplyMode != storage.ReplySteps {
		c.ReplyMode = ""
	}
}

func (r channelRepo) Prune(ctx context.Context, before time.Time) (int, error) {
	n := 0
	for _, q := range []string{
		`DELETE FROM channel_threads WHERE conversation_id IN (SELECT id FROM conversations WHERE updated_at < ?)`,
		`DELETE FROM settings WHERE (key LIKE 'channel_rule/%' OR key LIKE 'channel_pending/%') AND updated_at < ?`,
	} {
		res, err := r.db.ExecContext(ctx, q, fmtTime(before))
		if err != nil {
			return n, err
		}
		k, _ := res.RowsAffected()
		n += int(k)
	}
	return n, nil
}
