package sqlite

import (
	"context"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type processRepo struct{ db dbtx }

const processCols = `id, project_id, name, command, cwd, kind, source, autostart, autorestart, created_at, updated_at`

func scanProcess(row scanner) (storage.Process, error) {
	var (
		p                storage.Process
		created, updated string
	)
	if err := row.Scan(&p.ID, &p.ProjectID, &p.Name, &p.Command, &p.Cwd, &p.Kind, &p.Source, &p.Autostart, &p.Autorestart, &created, &updated); err != nil {
		return p, notFound(err)
	}
	var err error
	if p.CreatedAt, err = parseTime(created); err != nil {
		return p, err
	}
	p.UpdatedAt, err = parseTime(updated)
	return p, err
}

func (r processRepo) Create(ctx context.Context, p storage.Process) (storage.Process, error) {
	if p.ID == "" {
		p.ID = ids.New("proc")
	}
	if p.Kind == "" {
		p.Kind = "service"
	}
	p.CreatedAt = time.Now().UTC()
	p.UpdatedAt = p.CreatedAt
	_, err := r.db.ExecContext(ctx, `INSERT INTO processes (`+processCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.ProjectID, p.Name, p.Command, p.Cwd, p.Kind, p.Source, boolInt(p.Autostart), boolInt(p.Autorestart), fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt))
	if isUnique(err) {
		return storage.Process{}, storage.ErrConflict
	}
	return p, err
}

func (r processRepo) Update(ctx context.Context, p storage.Process) error {
	p.UpdatedAt = time.Now().UTC()
	err := execOne(ctx, r.db, `UPDATE processes SET name=?, command=?, cwd=?, kind=?, autostart=?, autorestart=?, updated_at=? WHERE id=?`,
		p.Name, p.Command, p.Cwd, p.Kind, boolInt(p.Autostart), boolInt(p.Autorestart), fmtTime(p.UpdatedAt), p.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

func (r processRepo) Get(ctx context.Context, id string) (storage.Process, error) {
	return scanProcess(r.db.QueryRowContext(ctx, `SELECT `+processCols+` FROM processes WHERE id=?`, id))
}

func (r processRepo) List(ctx context.Context, projectID string) ([]storage.Process, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+processCols+` FROM processes WHERE (?='' OR project_id=?) ORDER BY created_at, name`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Process{}
	for rows.Next() {
		p, err := scanProcess(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r processRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM processes WHERE id=?`, id)
}
