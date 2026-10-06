package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type tokenRepo struct{ db dbtx }

const tokenCols = `id, user_id, name, token_hash, created_at, last_used_at, revoked, expires_at`

func scanToken(row scanner) (storage.UserToken, error) {
	var (
		t             storage.UserToken
		created       string
		used, expires sql.NullString
		revoked       int
	)
	if err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &created, &used, &revoked, &expires); err != nil {
		return t, notFound(err)
	}
	t.Revoked = revoked == 1
	var err error
	if t.CreatedAt, err = parseTime(created); err != nil {
		return t, err
	}
	if t.LastUsedAt, err = optParse(used); err != nil {
		return t, err
	}
	t.ExpiresAt, err = optParse(expires)
	return t, err
}

func (r tokenRepo) Create(ctx context.Context, t storage.UserToken) (storage.UserToken, error) {
	t.ID, t.CreatedAt = ids.New("tok"), time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `INSERT INTO user_tokens (`+tokenCols+`) VALUES (?,?,?,?,?,NULL,0,?)`,
		t.ID, t.UserID, t.Name, t.TokenHash, fmtTime(t.CreatedAt), optTime(t.ExpiresAt))
	return t, err
}

func (r tokenRepo) List(ctx context.Context, userID string) ([]storage.UserToken, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+tokenCols+` FROM user_tokens WHERE user_id=? AND revoked=0 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.UserToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r tokenRepo) GetByHash(ctx context.Context, hash string) (storage.UserToken, error) {
	return scanToken(r.db.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM user_tokens WHERE token_hash=?`, hash))
}

func (r tokenRepo) Touch(ctx context.Context, id string, at time.Time) error {
	return execOne(ctx, r.db, `UPDATE user_tokens SET last_used_at=? WHERE id=?`, fmtTime(at), id)
}

func (r tokenRepo) Revoke(ctx context.Context, id, userID string) error {
	return execOne(ctx, r.db, `UPDATE user_tokens SET revoked=1 WHERE id=? AND user_id=?`, id, userID)
}
