package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type passkeyRepo struct{ db dbtx }

const passkeyCols = `id, user_id, name, credential_id, credential, created_at, last_used_at`

func scanPasskey(row scanner) (storage.Passkey, error) {
	var (
		p       storage.Passkey
		created string
		used    sql.NullString
	)
	if err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.CredentialID, &p.Credential, &created, &used); err != nil {
		return p, notFound(err)
	}
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return p, err
	}
	p.LastUsedAt, err = optParse(used)
	return p, err
}

func (r passkeyRepo) Create(ctx context.Context, p storage.Passkey) (storage.Passkey, error) {
	p.ID, p.CreatedAt = ids.New("pky"), time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `INSERT INTO passkeys (`+passkeyCols+`) VALUES (?,?,?,?,?,?,NULL)`,
		p.ID, p.UserID, p.Name, p.CredentialID, p.Credential, fmtTime(p.CreatedAt))
	if isUnique(err) {
		return p, storage.ErrConflict
	}
	return p, err
}

func (r passkeyRepo) List(ctx context.Context, userID string) ([]storage.Passkey, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+passkeyCols+` FROM passkeys WHERE user_id=? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Passkey{}
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r passkeyRepo) GetByCredentialID(ctx context.Context, credentialID string) (storage.Passkey, error) {
	return scanPasskey(r.db.QueryRowContext(ctx, `SELECT `+passkeyCols+` FROM passkeys WHERE credential_id=?`, credentialID))
}

func (r passkeyRepo) Used(ctx context.Context, id, credential string, at time.Time) error {
	return execOne(ctx, r.db, `UPDATE passkeys SET credential=?, last_used_at=? WHERE id=?`, credential, fmtTime(at), id)
}

func (r passkeyRepo) Delete(ctx context.Context, id, userID string) error {
	return execOne(ctx, r.db, `DELETE FROM passkeys WHERE id=? AND user_id=?`, id, userID)
}
