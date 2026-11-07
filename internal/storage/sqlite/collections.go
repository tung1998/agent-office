package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

// Collections (ADR-172). Field names go into SQL as JSON paths; the service
// checks them and so does this file (safeName), so a name is never SQL.

type collectionRepo struct{ db dbtx }

func (s *Store) Collections() storage.CollectionRepo { return collectionRepo{s.q} }

var (
	safeName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	safeID   = regexp.MustCompile(`^[a-z0-9_]{1,64}$`) // ids.New
)

// fieldExpr is the field's value in a record (the same text as the indexes',
// so SQLite uses them).
func fieldExpr(name string) (string, error) {
	if !safeName.MatchString(name) {
		return "", fmt.Errorf("tên trường không hợp lệ: %q", name)
	}
	return `json_extract(data, '$.` + name + `')`, nil
}

const collectionCols = `id, project_id, key, name, description, pack, fields, created_at, updated_at`

func scanCollection(row scanner) (storage.Collection, error) {
	var (
		c                storage.Collection
		fields           string
		created, updated string
	)
	if err := row.Scan(&c.ID, &c.ProjectID, &c.Key, &c.Name, &c.Description, &c.Pack, &fields, &created, &updated); err != nil {
		return c, notFound(err)
	}
	_ = json.Unmarshal([]byte(fields), &c.Fields)
	if c.Fields == nil {
		c.Fields = []storage.Field{}
	}
	return c, parseTimes([]*time.Time{&c.CreatedAt, &c.UpdatedAt}, created, updated)
}

func (r collectionRepo) Create(ctx context.Context, c storage.Collection) (storage.Collection, error) {
	now := time.Now().UTC()
	c.ID, c.CreatedAt, c.UpdatedAt = ids.New("col"), now, now
	if c.Fields == nil {
		c.Fields = []storage.Field{}
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO collections (`+collectionCols+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		c.ID, c.ProjectID, c.Key, c.Name, c.Description, c.Pack, toJSON(c.Fields), fmtTime(now), fmtTime(now))
	if isUnique(err) {
		return c, storage.ErrConflict
	}
	return c, err
}

func (r collectionRepo) Update(ctx context.Context, c storage.Collection) error {
	if c.Fields == nil {
		c.Fields = []storage.Field{}
	}
	res, err := r.db.ExecContext(ctx, `UPDATE collections SET name=?, description=?, fields=?, updated_at=? WHERE id=?`,
		c.Name, c.Description, toJSON(c.Fields), fmtTime(time.Now()), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (r collectionRepo) Get(ctx context.Context, projectID, key string) (storage.Collection, error) {
	return scanCollection(r.db.QueryRowContext(ctx, `SELECT `+collectionCols+` FROM collections WHERE project_id=? AND key=?`, projectID, key))
}

func (r collectionRepo) ByID(ctx context.Context, id string) (storage.Collection, error) {
	return scanCollection(r.db.QueryRowContext(ctx, `SELECT `+collectionCols+` FROM collections WHERE id=?`, id))
}

func (r collectionRepo) List(ctx context.Context, projectID string) ([]storage.Collection, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+collectionCols+` FROM collections WHERE project_id=? ORDER BY key`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Collection{}
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Index: an indexed field shares one index across collections (the same
// expression); a unique one is partial, to its collection.
func (r collectionRepo) Index(ctx context.Context, c storage.Collection) error {
	if !safeID.MatchString(c.ID) {
		return fmt.Errorf("mã collection không hợp lệ: %q", c.ID)
	}
	for _, f := range c.Fields {
		expr, err := fieldExpr(f.Name)
		if err != nil {
			return err
		}
		if f.Indexed || f.Unique {
			if _, err := r.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS records_f_`+f.Name+` ON records(collection_id, `+expr+`)`); err != nil {
				return err
			}
		}
		if f.Unique {
			_, err := r.db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS records_u_`+c.ID+`_`+f.Name+` ON records(`+expr+`) WHERE collection_id='`+c.ID+`'`)
			if isUnique(err) {
				return storage.ErrConflict
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

const recordCols = `id, collection_id, project_id, data, text, created_by, updated_by, created_at, updated_at`

func scanRecord(row scanner) (storage.Record, error) {
	var (
		x                storage.Record
		data             string
		created, updated string
	)
	if err := row.Scan(&x.ID, &x.CollectionID, &x.ProjectID, &data, &x.Text, &x.CreatedBy, &x.UpdatedBy, &created, &updated); err != nil {
		return x, notFound(err)
	}
	_ = json.Unmarshal([]byte(data), &x.Data)
	if x.Data == nil {
		x.Data = map[string]any{}
	}
	return x, parseTimes([]*time.Time{&x.CreatedAt, &x.UpdatedAt}, created, updated)
}

func (r collectionRepo) history(ctx context.Context, x storage.Record, op, by string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO record_history (id, record_id, collection_id, op, data, by, at) VALUES (?,?,?,?,?,?,?)`,
		ids.New("rch"), x.ID, x.CollectionID, op, toJSON(x.Data), by, fmtTime(at))
	return err
}

func (r collectionRepo) Insert(ctx context.Context, x storage.Record) (storage.Record, error) {
	now := time.Now().UTC()
	x.ID, x.CreatedAt, x.UpdatedAt = ids.New("rec"), now, now
	if x.UpdatedBy == "" {
		x.UpdatedBy = x.CreatedBy
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO records (`+recordCols+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		x.ID, x.CollectionID, x.ProjectID, toJSON(x.Data), x.Text, x.CreatedBy, x.UpdatedBy, fmtTime(now), fmtTime(now))
	if isUnique(err) {
		return x, storage.ErrConflict
	}
	if err != nil {
		return x, err
	}
	return x, r.history(ctx, x, "create", x.CreatedBy, now)
}

// Save is CAS'd on the updated_at x was read with (x.UpdatedAt): a write
// landing between the caller's read and Save loses with storage.ErrStale
// instead of silently overwriting (mirrors processRepo.Update).
func (r collectionRepo) Save(ctx context.Context, x storage.Record) error {
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, `UPDATE records SET data=?, text=?, updated_by=?, updated_at=? WHERE id=? AND collection_id=? AND updated_at=?`,
		toJSON(x.Data), x.Text, x.UpdatedBy, fmtTime(now), x.ID, x.CollectionID, fmtTime(x.UpdatedAt))
	if isUnique(err) {
		return storage.ErrConflict
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, getErr := r.Record(ctx, x.CollectionID, x.ID); errors.Is(getErr, storage.ErrNotFound) {
			return storage.ErrNotFound
		}
		return storage.ErrStale
	}
	return r.history(ctx, x, "update", x.UpdatedBy, now)
}

func (r collectionRepo) Delete(ctx context.Context, x storage.Record, by string) error {
	if err := r.history(ctx, x, "delete", by, time.Now().UTC()); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM records WHERE id=? AND collection_id=?`, x.ID, x.CollectionID)
	return err
}

func (r collectionRepo) Record(ctx context.Context, collectionID, id string) (storage.Record, error) {
	return scanRecord(r.db.QueryRowContext(ctx, `SELECT `+recordCols+` FROM records WHERE collection_id=? AND id=?`, collectionID, id))
}

func (r collectionRepo) FindBy(ctx context.Context, collectionID, field string, value any) (storage.Record, error) {
	expr, err := fieldExpr(field)
	if err != nil {
		return storage.Record{}, err
	}
	return scanRecord(r.db.QueryRowContext(ctx, `SELECT `+recordCols+` FROM records WHERE collection_id=? AND `+expr+`=? LIMIT 1`, collectionID, value))
}

func (r collectionRepo) Query(ctx context.Context, q storage.RecordQuery) ([]storage.Record, int, error) {
	where, args := []string{"collection_id=?"}, []any{q.CollectionID}
	for _, c := range q.Where {
		expr, err := fieldExpr(c.Field)
		if err != nil {
			return nil, 0, err
		}
		switch c.Op {
		case "eq", "":
			where, args = append(where, expr+"=?"), append(args, c.Value)
		case "ne":
			where, args = append(where, expr+" IS NOT ?"), append(args, c.Value)
		case "lt", "lte", "gt", "gte":
			op := map[string]string{"lt": "<", "lte": "<=", "gt": ">", "gte": ">="}[c.Op]
			where, args = append(where, expr+op+"?"), append(args, c.Value)
		case "like": // the value is the pattern
			where, args = append(where, expr+" LIKE ?"), append(args, c.Value)
		case "has": // a tags field holds the value
			where = append(where, `EXISTS (SELECT 1 FROM json_each(records.data, '$.`+c.Field+`') WHERE value=?)`)
			args = append(args, c.Value)
		case "in":
			vs, _ := c.Value.([]any)
			if len(vs) == 0 {
				where = append(where, "0")
				continue
			}
			where = append(where, expr+" IN (?"+strings.Repeat(",?", len(vs)-1)+")")
			args = append(args, vs...)
		default:
			return nil, 0, fmt.Errorf("phép so sánh không hợp lệ: %q", c.Op)
		}
	}
	if s := ftsQuery(q.Search); s != "" {
		where, args = append(where, "rowid IN (SELECT rowid FROM records_fts WHERE records_fts MATCH ?)"), append(args, s)
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "updated_at"
	switch q.Sort {
	case "", "updated_at":
	case "created_at":
		order = "created_at"
	default:
		expr, err := fieldExpr(q.Sort)
		if err != nil {
			return nil, 0, err
		}
		order = expr
	}
	if q.Desc || q.Sort == "" {
		order += " DESC"
	}
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+recordCols+` FROM records WHERE `+cond+` ORDER BY `+order+`, id LIMIT ? OFFSET ?`,
		append(args, q.Limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []storage.Record{}
	for rows.Next() {
		x, err := scanRecord(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, x)
	}
	return out, total, rows.Err()
}

func (r collectionRepo) Count(ctx context.Context, projectID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE project_id=?`, projectID).Scan(&n)
	return n, err
}

func (r collectionRepo) History(ctx context.Context, recordID string, limit int) ([]storage.RecordChange, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, record_id, op, data, by, at FROM record_history WHERE record_id=? ORDER BY at DESC, id DESC LIMIT ?`, recordID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.RecordChange{}
	for rows.Next() {
		var (
			c        storage.RecordChange
			data, at string
		)
		if err := rows.Scan(&c.ID, &c.RecordID, &c.Op, &data, &c.By, &at); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(data), &c.Data)
		if c.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
