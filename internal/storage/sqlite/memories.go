package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type memoryRepo struct{ db dbtx }

const memoryCols = `id, project_id, agent_id, text, source, created_by, created_at, updated_at`

func scanMemory(row scanner) (storage.Memory, error) {
	var (
		m                storage.Memory
		created, updated string
	)
	if err := row.Scan(&m.ID, &m.ProjectID, &m.AgentID, &m.Text, &m.Source, &m.CreatedBy, &created, &updated); err != nil {
		return m, notFound(err)
	}
	return m, parseTimes([]*time.Time{&m.CreatedAt, &m.UpdatedAt}, created, updated)
}

func (r memoryRepo) List(ctx context.Context, projectID, agentID string) ([]storage.Memory, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+memoryCols+` FROM agent_memories WHERE project_id=? AND agent_id=? ORDER BY created_at, id`, projectID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Memory{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r memoryRepo) Get(ctx context.Context, id string) (storage.Memory, error) {
	return scanMemory(r.db.QueryRowContext(ctx, `SELECT `+memoryCols+` FROM agent_memories WHERE id=?`, id))
}

func (r memoryRepo) Create(ctx context.Context, m storage.Memory) (storage.Memory, error) {
	now := time.Now().UTC()
	m.ID, m.CreatedAt, m.UpdatedAt = ids.New("mem"), now, now
	if m.Source == "" {
		m.Source = "person"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO agent_memories (`+memoryCols+`) VALUES (?,?,?,?,?,?,?,?)`,
		m.ID, m.ProjectID, m.AgentID, m.Text, m.Source, m.CreatedBy, fmtTime(now), fmtTime(now))
	return m, err
}

func (r memoryRepo) Update(ctx context.Context, id, text string) error {
	return execOne(ctx, r.db, `UPDATE agent_memories SET text=?, updated_at=? WHERE id=?`, text, fmtTime(time.Now()), id)
}

func (r memoryRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM agent_memories WHERE id=?`, id)
}

func (r memoryRepo) Replace(ctx context.Context, projectID, agentID string, items []storage.Memory) error {
	return inTx(ctx, r.db, func(db dbtx) error {
		if _, err := db.ExecContext(ctx, `DELETE FROM agent_memories WHERE project_id=? AND agent_id=?`, projectID, agentID); err != nil {
			return err
		}
		for i, m := range items {
			m.ProjectID, m.AgentID = projectID, agentID
			if m.CreatedAt.IsZero() { // kept in their order
				m.CreatedAt = time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
			}
			if m.ID == "" {
				m.ID = ids.New("mem")
			}
			if m.Source == "" {
				m.Source = "person"
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO agent_memories (`+memoryCols+`) VALUES (?,?,?,?,?,?,?,?)`,
				m.ID, projectID, agentID, m.Text, m.Source, m.CreatedBy, fmtTime(m.CreatedAt), fmtTime(time.Now())); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r memoryRepo) SaveRevision(ctx context.Context, rev storage.MemoryRevision) (storage.MemoryRevision, error) {
	rev.ID, rev.CreatedAt = ids.New("mrv"), time.Now().UTC()
	if rev.Items == nil {
		rev.Items = []storage.Memory{}
	}
	raw, _ := json.Marshal(rev.Items)
	_, err := r.db.ExecContext(ctx, `INSERT INTO agent_memory_revisions (id, project_id, agent_id, items, reason, created_at) VALUES (?,?,?,?,?,?)`,
		rev.ID, rev.ProjectID, rev.AgentID, string(raw), rev.Reason, fmtTime(rev.CreatedAt))
	return rev, err
}

func scanMemoryRevision(row scanner) (storage.MemoryRevision, error) {
	var (
		rev          storage.MemoryRevision
		raw, created string
	)
	if err := row.Scan(&rev.ID, &rev.ProjectID, &rev.AgentID, &raw, &rev.Reason, &created); err != nil {
		return rev, notFound(err)
	}
	_ = json.Unmarshal([]byte(raw), &rev.Items)
	var err error
	rev.CreatedAt, err = parseTime(created)
	return rev, err
}

func (r memoryRepo) Revisions(ctx context.Context, projectID, agentID string) ([]storage.MemoryRevision, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, project_id, agent_id, items, reason, created_at FROM agent_memory_revisions
		WHERE project_id=? AND agent_id=? ORDER BY created_at DESC LIMIT 30`, projectID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.MemoryRevision{}
	for rows.Next() {
		rev, err := scanMemoryRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

func (r memoryRepo) GetRevision(ctx context.Context, id string) (storage.MemoryRevision, error) {
	return scanMemoryRevision(r.db.QueryRowContext(ctx, `SELECT id, project_id, agent_id, items, reason, created_at FROM agent_memory_revisions WHERE id=?`, id))
}

// inTx runs fn in a transaction (the one db already is, or a new one).
func inTx(ctx context.Context, db dbtx, fn func(dbtx) error) error {
	var on func(string)
	if h, ok := db.(hooked); ok { // the live updates' wrapper: the db is inside
		db, on = h.dbtx, h.on
	}
	if _, ok := db.(*sql.Tx); ok {
		return fn(wrap(db, on))
	}
	d, ok := db.(*sql.DB)
	if !ok {
		return fn(wrap(db, on))
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	w := deferred(on)
	if err := fn(wrap(tx, w.add)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.flush()
	return nil
}
