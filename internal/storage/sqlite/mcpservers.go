package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

func (s *Store) MCPServers() storage.MCPServerRepo { return mcpServerRepo{s.q} }

type mcpServerRepo struct{ db dbtx }

const mcpServerCols = `id, name, kind, url, command, args, env_enc, headers_enc, scope, origin, enabled,
	last_check_at, last_check_status, last_check_error, last_tools, created_at, updated_at, oauth_enc,
	agents, trusted_tools, origin_ref`

func scanMCPServer(row scanner) (storage.MCPServer, error) {
	var (
		m                storage.MCPServer
		args, tools      string
		agents, trusted  string
		last             sql.NullString
		created, updated string
	)
	if err := row.Scan(&m.ID, &m.Name, &m.Kind, &m.URL, &m.Command, &args, &m.EnvEnc, &m.HeadersEnc, &m.Scope, &m.Origin, &m.Enabled,
		&last, &m.LastCheckStatus, &m.LastCheckError, &tools, &created, &updated, &m.OAuthEnc,
		&agents, &trusted, &m.OriginRef); err != nil {
		return m, notFound(err)
	}
	_ = json.Unmarshal([]byte(args), &m.Args)
	_ = json.Unmarshal([]byte(tools), &m.LastTools)
	_ = json.Unmarshal([]byte(agents), &m.Agents)
	_ = json.Unmarshal([]byte(trusted), &m.TrustedTools)
	normMCPServer(&m)
	var err error
	if m.LastCheckAt, err = optParse(last); err != nil {
		return m, err
	}
	return m, parseTimes([]*time.Time{&m.CreatedAt, &m.UpdatedAt}, created, updated)
}

func normMCPServer(m *storage.MCPServer) {
	if m.Args == nil {
		m.Args = []string{}
	}
	if m.LastTools == nil {
		m.LastTools = []storage.MCPTool{}
	}
	if m.Agents == nil {
		m.Agents = []string{}
	}
	if m.TrustedTools == nil {
		m.TrustedTools = []string{}
	}
	if m.Kind == "" {
		m.Kind = "http"
	}
	if m.Scope == "" {
		m.Scope = "machine"
	}
	if m.Origin == "" {
		m.Origin = "manual"
	}
}

func (r mcpServerRepo) Create(ctx context.Context, m storage.MCPServer) (storage.MCPServer, error) {
	now := time.Now().UTC()
	m.ID, m.CreatedAt, m.UpdatedAt = ids.New("mcp"), now, now
	normMCPServer(&m)
	_, err := r.db.ExecContext(ctx, `INSERT INTO mcp_servers (`+mcpServerCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,NULL,'','','[]',?,?,?,?,?,?)`,
		m.ID, m.Name, m.Kind, m.URL, m.Command, toJSON(m.Args), m.EnvEnc, m.HeadersEnc, m.Scope, m.Origin, m.Enabled, fmtTime(now), fmtTime(now), m.OAuthEnc,
		toJSON(m.Agents), toJSON(m.TrustedTools), m.OriginRef)
	if isUnique(err) {
		return storage.MCPServer{}, storage.ErrConflict
	}
	return m, err
}

func (r mcpServerRepo) Update(ctx context.Context, m storage.MCPServer) error {
	normMCPServer(&m)
	err := execOne(ctx, r.db, `UPDATE mcp_servers SET name=?, kind=?, url=?, command=?, args=?, env_enc=?, headers_enc=?, scope=?, origin=?, enabled=?,
		agents=?, trusted_tools=?, origin_ref=?, updated_at=? WHERE id=?`,
		m.Name, m.Kind, m.URL, m.Command, toJSON(m.Args), m.EnvEnc, m.HeadersEnc, m.Scope, m.Origin, m.Enabled,
		toJSON(m.Agents), toJSON(m.TrustedTools), m.OriginRef, fmtTime(time.Now()), m.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

func (r mcpServerRepo) Get(ctx context.Context, id string) (storage.MCPServer, error) {
	return scanMCPServer(r.db.QueryRowContext(ctx, `SELECT `+mcpServerCols+` FROM mcp_servers WHERE id=?`, id))
}

func (r mcpServerRepo) GetByName(ctx context.Context, name string) (storage.MCPServer, error) {
	return scanMCPServer(r.db.QueryRowContext(ctx, `SELECT `+mcpServerCols+` FROM mcp_servers WHERE name=?`, name))
}

func (r mcpServerRepo) List(ctx context.Context) ([]storage.MCPServer, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+mcpServerCols+` FROM mcp_servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.MCPServer{}
	for rows.Next() {
		m, err := scanMCPServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r mcpServerRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM mcp_servers WHERE id=?`, id)
}

func (r mcpServerRepo) SetCheck(ctx context.Context, id, status, errMsg string, tools []storage.MCPTool, at time.Time) error {
	if tools == nil {
		tools = []storage.MCPTool{}
	}
	return execOne(ctx, r.db, `UPDATE mcp_servers SET last_check_at=?, last_check_status=?, last_check_error=?, last_tools=? WHERE id=?`,
		fmtTime(at), status, errMsg, toJSON(tools), id)
}

func (r mcpServerRepo) SetOAuth(ctx context.Context, id, oauthEnc string) error {
	return execOne(ctx, r.db, `UPDATE mcp_servers SET oauth_enc=? WHERE id=?`, oauthEnc, id)
}

func (s *Store) MCPCalls() storage.MCPCallRepo { return mcpCallRepo{s.q} }

type mcpCallRepo struct{ db dbtx }

func (r mcpCallRepo) Add(ctx context.Context, c storage.MCPCall) error {
	if c.ID == "" {
		c.ID = ids.New("mcl")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO mcp_calls (id, server_id, server_name, tool, caller, caller_kind, project_id, conversation_id, job_id, action_id,
		status, error, duration_ms, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.ServerID, c.ServerName, c.Tool, c.Caller, c.CallerKind, c.ProjectID, c.ConversationID, c.JobID, c.ActionID,
		c.Status, c.Error, c.DurationMS, fmtTime(c.CreatedAt))
	return err
}

func (r mcpCallRepo) List(ctx context.Context, serverID string, limit int) ([]storage.MCPCall, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, server_id, server_name, tool, caller, caller_kind, project_id, conversation_id, job_id, action_id,
		status, error, duration_ms, created_at FROM mcp_calls`
	args := []any{}
	if serverID != "" {
		q += ` WHERE server_id=?`
		args = append(args, serverID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.MCPCall{}
	for rows.Next() {
		var c storage.MCPCall
		var created string
		if err := rows.Scan(&c.ID, &c.ServerID, &c.ServerName, &c.Tool, &c.Caller, &c.CallerKind, &c.ProjectID, &c.ConversationID, &c.JobID, &c.ActionID,
			&c.Status, &c.Error, &c.DurationMS, &created); err != nil {
			return nil, err
		}
		if err := parseTimes([]*time.Time{&c.CreatedAt}, created); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r mcpCallRepo) Stats(ctx context.Context, since time.Time) ([]storage.MCPCallStat, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT server_id, COUNT(*),
		SUM(CASE WHEN status IN ('error','denied') THEN 1 ELSE 0 END),
		SUM(CASE WHEN status='proposed' THEN 1 ELSE 0 END), MAX(created_at)
		FROM mcp_calls WHERE created_at >= ? GROUP BY server_id`, fmtTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.MCPCallStat{}
	for rows.Next() {
		var s storage.MCPCallStat
		var last sql.NullString
		if err := rows.Scan(&s.ServerID, &s.Calls, &s.Errors, &s.Proposed, &last); err != nil {
			return nil, err
		}
		if s.LastAt, err = optParse(last); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r mcpCallRepo) Prune(ctx context.Context, before time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM mcp_calls WHERE created_at < ?`, fmtTime(before))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
