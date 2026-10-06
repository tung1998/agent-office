// Package sqlite is the local storage driver (modernc.org/sqlite, no CGO).
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/migrations"
)

// Times are stored as text in a fixed-width UTC layout (always 9 fraction
// digits) so ORDER BY on the column sorts chronologically; RFC3339Nano drops
// trailing zeros and ".874Z" would sort after ".874208Z". Parsing accepts both.
const (
	timeLayout      = time.RFC3339Nano
	timeWriteLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

// Store implements storage.Store on a single SQLite file.
type Store struct {
	db      *sql.DB
	q       dbtx // db, or the open transaction inside InTx
	onWrite func(table string)
	onChat  func(storage.Change) // a chat written, with what (ADR-078)
}

// dbtx is what repositories need; *sql.DB and *sql.Tx both satisfy it.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Open opens (and creates if needed) the database at path.
// Use ":memory:" only in tests; it is forced to a single connection.
func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("sqlite: create data dir: %w", err)
		}
	}
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	// One writer at a time is how SQLite works; a single connection also keeps
	// an in-memory database alive across calls.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}
	return &Store{db: db, q: db}, nil
}

// Migrate applies all pending goose migrations.
func (s *Store) Migrate(ctx context.Context) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db, mustSub(migrations.SQLite, "sqlite"))
	if err != nil {
		return fmt.Errorf("sqlite: migrate init: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("sqlite: migrate up: %w", err)
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Users() storage.UserRepo             { return userRepo{s.q} }
func (s *Store) Tokens() storage.TokenRepo           { return tokenRepo{s.q} }
func (s *Store) Channels() storage.ChannelRepo       { return channelRepo{s.q} }
func (s *Store) Sessions() storage.SessionRepo       { return sessionRepo{s.q} }
func (s *Store) Audit() storage.AuditRepo            { return auditRepo{s.q} }
func (s *Store) Providers() storage.ProviderRepo     { return providerRepo{s.q} }
func (s *Store) OrgModels() storage.OrgModelRepo     { return orgModelRepo{s.q} }
func (s *Store) Agents() storage.AgentRepo           { return agentRepo{s.q} }
func (s *Store) Repos() storage.RepoRepo             { return repoRepo{s.q} }
func (s *Store) Revisions() storage.RevisionRepo     { return revisionRepo{s.q} }
func (s *Store) Runs() storage.RunRepo               { return runRepo{s.q} }
func (s *Store) Settings() storage.SettingRepo       { return settingRepo{s.q} }
func (s *Store) Chat() storage.ChatRepo              { return chatRepo{s.q, s.onChat} }
func (s *Store) Tasks() storage.TaskRepo             { return taskRepo{s.q} }
func (s *Store) Processes() storage.ProcessRepo      { return processRepo{s.q} }
func (s *Store) Monitors() storage.MonitorRepo       { return monitorRepo{s.q} }
func (s *Store) Actions() storage.ActionRepo         { return actionRepo{s.q} }
func (s *Store) Jobs() storage.JobRepo               { return jobRepo{s.q} }
func (s *Store) Automations() storage.AutomationRepo { return automationRepo{s.q} }
func (s *Store) Memories() storage.MemoryRepo        { return memoryRepo{s.q} }

// InTx runs fn inside one transaction. Nested calls reuse the outer one.
func (s *Store) InTx(ctx context.Context, fn func(storage.Store) error) error {
	if _, nested := s.q.(*sql.Tx); nested || isHookedTx(s.q) {
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	w := deferred(s.onWrite)
	inner := &Store{db: s.db, q: wrap(tx, w.add), onWrite: s.onWrite, onChat: s.onChat}
	if err := fn(inner); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.flush()
	return nil
}

// ---- helpers ----

func fmtTime(t time.Time) string { return t.UTC().Format(timeWriteLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// ---- users ----

type userRepo struct{ db dbtx }

const userCols = `id, email, name, role, password_hash, disabled, must_change, created_at, updated_at, last_login_at`

func scanUser(row interface{ Scan(...any) error }) (storage.User, error) {
	var (
		u                    storage.User
		role                 string
		disabled, mustChange int
		created, updated     string
		lastLogin            sql.NullString
	)
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &role, &u.PasswordHash, &disabled, &mustChange, &created, &updated, &lastLogin); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, storage.ErrNotFound
		}
		return u, err
	}
	u.Role = storage.Role(role)
	u.Disabled = disabled != 0
	u.MustChange = mustChange != 0
	var err error
	if u.CreatedAt, err = parseTime(created); err != nil {
		return u, err
	}
	if u.UpdatedAt, err = parseTime(updated); err != nil {
		return u, err
	}
	if lastLogin.Valid {
		t, err := parseTime(lastLogin.String)
		if err != nil {
			return u, err
		}
		u.LastLoginAt = &t
	}
	return u, nil
}

func (r userRepo) Create(ctx context.Context, u storage.User) (storage.User, error) {
	now := time.Now().UTC()
	if u.ID == "" {
		u.ID = ids.New("usr")
	}
	u.CreatedAt, u.UpdatedAt = now, now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO users (id, email, name, role, password_hash, disabled, must_change, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.Name, string(u.Role), u.PasswordHash, boolInt(u.Disabled), boolInt(u.MustChange), fmtTime(now), fmtTime(now))
	if isUnique(err) {
		return storage.User{}, storage.ErrConflict
	}
	if err != nil {
		return storage.User{}, err
	}
	return u, nil
}

func (r userRepo) GetByID(ctx context.Context, id string) (storage.User, error) {
	return scanUser(r.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (r userRepo) GetByEmail(ctx context.Context, email string) (storage.User, error) {
	return scanUser(r.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, email))
}

func (r userRepo) List(ctx context.Context) ([]storage.User, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r userRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (r userRepo) UpdatePassword(ctx context.Context, id, hash string) error {
	return execOne(ctx, r.db, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, fmtTime(time.Now()), id)
}

func (r userRepo) SetCredentials(ctx context.Context, id, email, name, hash string) error {
	err := execOne(ctx, r.db, `UPDATE users SET email = ?, name = ?, password_hash = ?, must_change = 0, updated_at = ? WHERE id = ?`,
		email, name, hash, fmtTime(time.Now()), id)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

func (r userRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM users WHERE id = ?`, id)
}

func (r userRepo) SetDisabled(ctx context.Context, id string, disabled bool) error {
	return execOne(ctx, r.db, `UPDATE users SET disabled = ?, updated_at = ? WHERE id = ?`, boolInt(disabled), fmtTime(time.Now()), id)
}

func (r userRepo) TouchLogin(ctx context.Context, id string, at time.Time) error {
	return execOne(ctx, r.db, `UPDATE users SET last_login_at = ? WHERE id = ?`, fmtTime(at), id)
}

// ---- sessions ----

type sessionRepo struct{ db dbtx }

func (r sessionRepo) Create(ctx context.Context, s storage.Session) (storage.Session, error) {
	if s.ID == "" {
		s.ID = ids.New("ses")
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, created_at, expires_at, last_seen_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.TokenHash, fmtTime(s.CreatedAt), fmtTime(s.ExpiresAt), fmtTime(s.LastSeenAt), s.UserAgent, s.IP)
	if isUnique(err) {
		return storage.Session{}, storage.ErrConflict
	}
	return s, err
}

func (r sessionRepo) GetByTokenHash(ctx context.Context, tokenHash string) (storage.Session, error) {
	var (
		s                      storage.Session
		created, expires, seen string
	)
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, token_hash, created_at, expires_at, last_seen_at, user_agent, ip
		 FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&s.ID, &s.UserID, &s.TokenHash, &created, &expires, &seen, &s.UserAgent, &s.IP)
	if errors.Is(err, sql.ErrNoRows) {
		return s, storage.ErrNotFound
	}
	if err != nil {
		return s, err
	}
	if s.CreatedAt, err = parseTime(created); err != nil {
		return s, err
	}
	if s.ExpiresAt, err = parseTime(expires); err != nil {
		return s, err
	}
	if s.LastSeenAt, err = parseTime(seen); err != nil {
		return s, err
	}
	return s, nil
}

func (r sessionRepo) Touch(ctx context.Context, id string, at time.Time) error {
	return execOne(ctx, r.db, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, fmtTime(at), id)
}

func (r sessionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (r sessionRepo) DeleteForUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (r sessionRepo) DeleteForUserExcept(ctx context.Context, userID, keepID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keepID)
	return err
}

func (r sessionRepo) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, fmtTime(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- audit ----

type auditRepo struct{ db dbtx }

const auditCols = `id, actor, action, target, detail, at, actor_kind, actor_id, actor_name, approved_by, via,
	project_id, conversation_id, job_id, task_id, action_id, resource, resource_id, before_json, after_json, ok`

func (r auditRepo) Append(ctx context.Context, e storage.AuditEntry) error {
	if e.ID == "" {
		e.ID = ids.New("aud")
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	before, err := nullJSON(e.Before)
	if err != nil {
		return err
	}
	after, err := nullJSON(e.After)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO audit_log (`+auditCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Actor, e.Action, e.Target, string(raw), fmtTime(e.At), e.ActorKind, e.ActorID, e.ActorName, e.ApprovedBy, e.Via,
		e.ProjectID, e.ConversationID, e.JobID, e.TaskID, e.ActionID, e.Resource, e.ResourceID, before, after, boolInt(e.OK))
	return err
}

// nullJSON: nil map = SQL NULL (no snapshot), otherwise its JSON.
func nullJSON(m map[string]any) (any, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	return string(b), err
}

func auditWhere(f storage.AuditFilter) (string, []any) {
	var conds []string
	var args []any
	eq := func(col, v string) {
		if v != "" {
			conds, args = append(conds, col+" = ?"), append(args, v)
		}
	}
	eq("project_id", f.ProjectID)
	eq("resource", f.Resource)
	eq("resource_id", f.ResourceID)
	eq("actor_kind", f.ActorKind)
	eq("actor_id", f.ActorID)
	eq("actor_name", f.ActorName)
	eq("via", f.Via)
	eq("conversation_id", f.ConversationID)
	eq("job_id", f.JobID)
	eq("task_id", f.TaskID)
	if !f.From.IsZero() {
		conds, args = append(conds, "at >= ?"), append(args, fmtTime(f.From))
	}
	if !f.To.IsZero() {
		conds, args = append(conds, "at < ?"), append(args, fmtTime(f.To))
	}
	if f.BeforeID != "" {
		conds = append(conds, "(at, id) < (SELECT at, id FROM audit_log WHERE id = ?)")
		args = append(args, f.BeforeID)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (r auditRepo) List(ctx context.Context, f storage.AuditFilter) ([]storage.AuditEntry, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	where, args := auditWhere(f)
	rows, err := r.db.QueryContext(ctx, `SELECT `+auditCols+` FROM audit_log`+where+` ORDER BY at DESC, id DESC LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.AuditEntry
	for rows.Next() {
		var (
			e             storage.AuditEntry
			raw, at       string
			before, after sql.NullString
			ok            int
		)
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Target, &raw, &at, &e.ActorKind, &e.ActorID, &e.ActorName, &e.ApprovedBy, &e.Via,
			&e.ProjectID, &e.ConversationID, &e.JobID, &e.TaskID, &e.ActionID, &e.Resource, &e.ResourceID, &before, &after, &ok); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &e.Detail); err != nil {
			return nil, err
		}
		if before.Valid {
			if err := json.Unmarshal([]byte(before.String), &e.Before); err != nil {
				return nil, err
			}
		}
		if after.Valid {
			if err := json.Unmarshal([]byte(after.String), &e.After); err != nil {
				return nil, err
			}
		}
		e.OK = ok == 1
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

var auditGroups = map[string]string{
	"day": "substr(at, 1, 10)", "kind": "actor_kind", "actor": "actor_kind || ':' || actor_name",
	"resource": "resource", "via": "via", "project": "project_id",
}

func (r auditRepo) Count(ctx context.Context, f storage.AuditFilter, by string) ([]storage.AuditCount, error) {
	expr, ok := auditGroups[by]
	if !ok {
		return nil, fmt.Errorf("không nhóm được theo %q", by)
	}
	f.BeforeID = ""
	where, args := auditWhere(f)
	rows, err := r.db.QueryContext(ctx, `SELECT `+expr+` AS k, COUNT(*), SUM(ok = 0) FROM audit_log`+where+` GROUP BY k ORDER BY COUNT(*) DESC, k LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.AuditCount
	for rows.Next() {
		var c storage.AuditCount
		if err := rows.Scan(&c.Key, &c.Count, &c.Failed); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- misc ----

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func execOne(ctx context.Context, db dbtx, q string, args ...any) error {
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// Backup writes a consistent copy of the database to dest (which must not exist),
// safe to run while the server is serving requests.
func (s *Store) Backup(ctx context.Context, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("sqlite: %s đã tồn tại", dest)
	}
	_, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, dest)
	return err
}
