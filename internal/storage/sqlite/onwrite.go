package sqlite

import (
	"context"
	"database/sql"
	"regexp"
	"slices"
	"strings"

	"github.com/tung1998/agent-office/internal/storage"
)

// OnWrite calls fn with the table of every write (INSERT, UPDATE, DELETE,
// also through a transaction): the dashboard's live updates follow it.
func (s *Store) OnWrite(fn func(table string)) {
	s.onWrite = fn
	s.q = hooked{s.q, fn}
}

// OnChat calls fn with each chat written: a message (with it), a
// conversation made, changed or deleted. Set before anything writes.
func (s *Store) OnChat(fn func(storage.Change)) { s.onChat = fn }

// hooked is a dbtx that tells what it wrote.
type hooked struct {
	dbtx
	on func(table string)
}

var writeRe = regexp.MustCompile(`(?is)^\s*(?:INSERT\s+(?:OR\s+\w+\s+)?INTO|UPDATE(?:\s+OR\s+\w+)?|DELETE\s+FROM|REPLACE\s+INTO)\s+"?([A-Za-z_][A-Za-z0-9_]*)`)

func (h hooked) tell(q string) {
	if m := writeRe.FindStringSubmatch(q); m != nil {
		h.on(strings.ToLower(m[1]))
	}
}

func (h hooked) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	res, err := h.dbtx.ExecContext(ctx, q, args...)
	if err == nil {
		h.tell(q)
	}
	return res, err
}

func (h hooked) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	rows, err := h.dbtx.QueryContext(ctx, q, args...) // UPDATE … RETURNING
	if err == nil {
		h.tell(q)
	}
	return rows, err
}

func (h hooked) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	row := h.dbtx.QueryRowContext(ctx, q, args...)
	h.tell(q)
	return row
}

func isHookedTx(q dbtx) bool {
	h, ok := q.(hooked)
	if !ok {
		return false
	}
	_, tx := h.dbtx.(*sql.Tx)
	return tx
}

// wrap puts the hook back around db (none: db as is).
func wrap(db dbtx, on func(string)) dbtx {
	if on == nil {
		return db
	}
	return hooked{db, on}
}

// tables written in a transaction: told once it commits, never if it rolls back.
type written struct {
	on     func(string)
	tables []string
}

func deferred(on func(string)) *written { return &written{on: on} }

func (w *written) add(t string) {
	if w.on != nil && !slices.Contains(w.tables, t) {
		w.tables = append(w.tables, t)
	}
}

func (w *written) flush() {
	for _, t := range w.tables {
		w.on(t)
	}
}
