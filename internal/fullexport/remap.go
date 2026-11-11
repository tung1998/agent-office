package fullexport

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // the office's driver
)

// pathColumns: where the database keeps a path that makes something run
// (a project's folder, an agent's extra folders, an AI account's folder, a
// process's or an MCP server's command…). History (messages, audit, runs)
// keeps the paths it had: it tells what happened there.
var pathColumns = [][2]string{
	{"repos", "path"}, {"settings", "value"}, {"processes", "command"}, {"processes", "env"},
	{"agents", "permissions"}, {"agents", "instructions"}, {"providers", "cli_home"},
	{"mcp_servers", "command"}, {"mcp_servers", "args"}, {"automations", "prompt"},
	{"automations", "script"}, {"automations", "config"}, {"workflows", "source"}, {"jobs", "extra_dirs"},
}

// boundaries: what may follow a moved path inside a value ("/x" moves in
// "/x/a", "\"/x\"", "cd /x && …", never in "/xy").
var boundaries = []string{"/", `"`, " ", "'", "\n", ")", ";", ":"}

// fixDatabase moves the paths of moves (old → new) in pathColumns, signs
// everyone out (a session of the other machine), and in ModeClone turns
// bots and automations off and drops the peer pairings. It returns how many
// values changed.
func fixDatabase(ctx context.Context, dbPath string, moves map[string]string, mode string) (int64, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// longest first: the office's data folder lies inside its project's
	olds := make([]string, 0, len(moves))
	for from, to := range moves {
		if from != "" && from != to {
			olds = append(olds, from)
		}
	}
	sort.Slice(olds, func(i, j int) bool { return len(olds[i]) > len(olds[j]) })

	var changed int64
	for _, tc := range pathColumns {
		if !hasColumn(ctx, tx, tc[0], tc[1]) { // an older or newer schema: skip what it lacks
			continue
		}
		n, err := remapColumn(ctx, tx, tc[0], tc[1], olds, moves)
		if err != nil {
			return 0, fmt.Errorf("%s.%s: %w", tc[0], tc[1], err)
		}
		changed += n
	}
	stmts := []string{`DELETE FROM sessions`}
	if mode == ModeClone {
		stmts = append(stmts,
			`UPDATE automations SET enabled = 0`,
			`UPDATE channels SET enabled = 0`,
			`DELETE FROM settings WHERE key IN ('peers', 'peer_invites', 'peer_pending_joins', 'peer_outgoing', 'peer_requests')`,
		)
	}
	for _, q := range stmts {
		table := strings.Fields(strings.TrimPrefix(strings.TrimPrefix(q, "DELETE FROM "), "UPDATE "))[0]
		if !hasColumn(ctx, tx, table, "") {
			continue
		}
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return 0, fmt.Errorf("%s: %w", q, err)
		}
	}
	return changed, tx.Commit()
}

// remapColumn moves the paths in one column. Two passes through placeholders,
// so a new path that contains an old one is not moved twice.
func remapColumn(ctx context.Context, tx *sql.Tx, table, col string, olds []string, moves map[string]string) (int64, error) {
	var total int64
	tok := func(i int) string { return fmt.Sprintf("\x00office-move-%d\x00", i) }
	q := fmt.Sprintf(`UPDATE "%s" SET "%s" = ? WHERE "%s" = ?`, table, col, col)
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT DISTINCT "%s" FROM "%s" WHERE "%s" IS NOT NULL`, col, table, col))
	if err != nil {
		return 0, err
	}
	var values []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return 0, err
		}
		if v.Valid {
			values = append(values, v.String)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, v := range values {
		next := v
		if i := indexOf(olds, next); i >= 0 { // the whole value is a path
			next = tok(i)
		}
		for i, old := range olds {
			for _, b := range boundaries {
				next = strings.ReplaceAll(next, old+b, tok(i)+b)
			}
			if strings.HasSuffix(next, old) {
				next = strings.TrimSuffix(next, old) + tok(i)
			}
		}
		for i, old := range olds {
			next = strings.ReplaceAll(next, tok(i), moves[old])
		}
		if next == v {
			continue
		}
		res, err := tx.ExecContext(ctx, q, next, v)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// hasColumn: table exists (col "") or has col.
func hasColumn(ctx context.Context, tx *sql.Tx, table, col string) bool {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info("%s")`, table))
	if err != nil {
		return false
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, typ        string
			dflt             sql.NullString
		)
		if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk) == nil {
			found = found || col == "" || name == col
		}
	}
	return found
}
