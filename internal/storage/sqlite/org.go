package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type scanner interface{ Scan(...any) error }

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return storage.ErrNotFound
	}
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func parseTimes(dst []*time.Time, src ...string) error {
	for i, v := range src {
		t, err := parseTime(v)
		if err != nil {
			return err
		}
		*dst[i] = t
	}
	return nil
}

// ---- providers ----

type providerRepo struct{ db dbtx }

const providerCols = `id, name, kind, base_url, api_key_enc, api_key_env, api_key_hint, tier_models, models,
	is_default, enabled, status, status_detail, checked_at, created_at, updated_at, preset, limit_caps, cli_home`

func scanProvider(row scanner) (storage.Provider, error) {
	var (
		p                   storage.Provider
		kind, tiers, models string
		caps                string
		isDefault, enabled  int
		checked             sql.NullString
		created, updated    string
	)
	if err := row.Scan(&p.ID, &p.Name, &kind, &p.BaseURL, &p.APIKeyEnc, &p.APIKeyEnv, &p.APIKeyHint, &tiers, &models,
		&isDefault, &enabled, &p.Status, &p.StatusDetail, &checked, &created, &updated, &p.Preset, &caps, &p.CLIHome); err != nil {
		return p, notFound(err)
	}
	p.Kind = storage.ProviderKind(kind)
	p.IsDefault, p.Enabled = isDefault != 0, enabled != 0
	if err := json.Unmarshal([]byte(tiers), &p.TierModels); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(models), &p.Models); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(caps), &p.LimitCaps); err != nil {
		return p, err
	}
	if checked.Valid {
		t, err := parseTime(checked.String)
		if err != nil {
			return p, err
		}
		p.CheckedAt = &t
	}
	return p, parseTimes([]*time.Time{&p.CreatedAt, &p.UpdatedAt}, created, updated)
}

func (r providerRepo) Create(ctx context.Context, p storage.Provider) (storage.Provider, error) {
	now := time.Now().UTC()
	if p.ID == "" {
		p.ID = ids.New("prv")
	}
	if p.TierModels == nil {
		p.TierModels = map[string]string{}
	}
	if p.Models == nil {
		p.Models = []string{}
	}
	if p.Status == "" {
		p.Status = "unknown"
	}
	p.CreatedAt, p.UpdatedAt = now, now
	_, err := r.db.ExecContext(ctx, `INSERT INTO providers (`+providerCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, string(p.Kind), p.BaseURL, p.APIKeyEnc, p.APIKeyEnv, p.APIKeyHint, toJSON(p.TierModels), toJSON(p.Models),
		boolInt(p.IsDefault), boolInt(p.Enabled), p.Status, p.StatusDetail, nil, fmtTime(now), fmtTime(now), p.Preset, capsJSON(p.LimitCaps), p.CLIHome)
	if isUnique(err) {
		return storage.Provider{}, storage.ErrConflict
	}
	return p, err
}

func (r providerRepo) Update(ctx context.Context, p storage.Provider) error {
	if p.TierModels == nil {
		p.TierModels = map[string]string{}
	}
	err := execOne(ctx, r.db, `UPDATE providers SET name=?, kind=?, base_url=?, api_key_enc=?, api_key_env=?, api_key_hint=?,
		tier_models=?, enabled=?, preset=?, limit_caps=?, updated_at=? WHERE id=?`,
		p.Name, string(p.Kind), p.BaseURL, p.APIKeyEnc, p.APIKeyEnv, p.APIKeyHint, toJSON(p.TierModels), boolInt(p.Enabled),
		p.Preset, capsJSON(p.LimitCaps), fmtTime(time.Now()), p.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

// capsJSON: a provider's stop thresholds as stored ({} when none).
func capsJSON(c map[string]int) string {
	if c == nil {
		c = map[string]int{}
	}
	return toJSON(c)
}

func (r providerRepo) Get(ctx context.Context, id string) (storage.Provider, error) {
	return scanProvider(r.db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE id=?`, id))
}

func (r providerRepo) List(ctx context.Context) ([]storage.Provider, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+providerCols+` FROM providers ORDER BY is_default DESC, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r providerRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM providers WHERE id=?`, id)
}

func (r providerRepo) SetDefault(ctx context.Context, id string) error {
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `UPDATE providers SET is_default = CASE WHEN id=? THEN 1 ELSE 0 END`, id)
	return err
}

func (r providerRepo) SetStatus(ctx context.Context, id, status, detail string, models []string, at time.Time) error {
	if models == nil {
		return execOne(ctx, r.db, `UPDATE providers SET status=?, status_detail=?, checked_at=? WHERE id=?`, status, detail, fmtTime(at), id)
	}
	return execOne(ctx, r.db, `UPDATE providers SET status=?, status_detail=?, models=?, checked_at=? WHERE id=?`,
		status, detail, toJSON(models), fmtTime(at), id)
}

// SetTierModels writes auto-filled tier models, but only while the row still
// matches prevTiers/baseURL/kind — if the admin edited the provider (or saved
// different tiers) since prevTiers was read, this is a no-op, not an overwrite.
func (r providerRepo) SetTierModels(ctx context.Context, id string, newTiers, prevTiers map[string]string, baseURL string, kind storage.ProviderKind) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE providers SET tier_models=?, updated_at=?
		WHERE id=? AND base_url=? AND kind=? AND tier_models=?`,
		toJSON(newTiers), fmtTime(time.Now()), id, baseURL, string(kind), toJSON(prevTiers))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ---- org models ----

// ---- agents ----

type agentRepo struct{ db dbtx }

func idsOrEmpty(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

const agentCols = `id, project_id, key, name, role, description, provider_id, model_tier, llm_model,
	instructions, permissions, sort, created_at, updated_at, avatar, enabled, fallback_provider_ids, effort`

func scanAgent(row scanner) (storage.Agent, error) {
	var (
		a                storage.Agent
		perms            string
		avatar, fallback string
		provider         sql.NullString
		created, updated string
		enabled          bool
	)
	if err := row.Scan(&a.ID, &a.ProjectID, &a.Key, &a.Name, &a.Role, &a.Description, &provider, &a.ModelTier,
		&a.LLMModel, &a.Instructions, &perms, &a.Sort, &created, &updated, &avatar, &enabled, &fallback, &a.Effort); err != nil {
		return a, notFound(err)
	}
	a.ProviderID, a.Disabled = provider.String, !enabled
	_ = json.Unmarshal([]byte(fallback), &a.FallbackProviderIDs)
	if err := json.Unmarshal([]byte(perms), &a.Permissions); err != nil {
		return a, err
	}
	_ = json.Unmarshal([]byte(avatar), &a.Avatar)
	return a, parseTimes([]*time.Time{&a.CreatedAt, &a.UpdatedAt}, created, updated)
}

func (r agentRepo) Create(ctx context.Context, a storage.Agent) (storage.Agent, error) {
	now := time.Now().UTC()
	if a.ID == "" {
		a.ID = ids.New("agt")
	}
	a.CreatedAt, a.UpdatedAt = now, now
	_, err := r.db.ExecContext(ctx, `INSERT INTO agents (`+agentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.ProjectID, a.Key, a.Name, a.Role, a.Description, nullStr(a.ProviderID), a.ModelTier,
		a.LLMModel, a.Instructions, toJSON(a.Permissions), a.Sort, fmtTime(now), fmtTime(now), toJSON(a.Avatar), !a.Disabled,
		toJSON(idsOrEmpty(a.FallbackProviderIDs)), a.Effort)
	if isUnique(err) {
		return storage.Agent{}, storage.ErrConflict
	}
	return a, err
}

func (r agentRepo) Update(ctx context.Context, a storage.Agent) error {
	err := execOne(ctx, r.db, `UPDATE agents SET key=?, name=?, role=?, description=?, provider_id=?,
		model_tier=?, llm_model=?, instructions=?, permissions=?, sort=?, avatar=?, fallback_provider_ids=?, effort=?, updated_at=? WHERE id=?`,
		a.Key, a.Name, a.Role, a.Description, nullStr(a.ProviderID), a.ModelTier, a.LLMModel,
		a.Instructions, toJSON(a.Permissions), a.Sort, toJSON(a.Avatar), toJSON(idsOrEmpty(a.FallbackProviderIDs)), a.Effort, fmtTime(time.Now()), a.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

// UpdateIf is Update, but refuses with ErrConflict when the row no longer
// matches prev (a write landed between the caller's Get and this Update).
// It re-reads the row and compares it to prev as decoded Go values, not as
// raw JSON text: a column like permissions can hold the same data in a
// string that differs from what Go's own json.Marshal would produce (an
// older migration's json_set, a legacy default of "{}", key order), so a
// text comparison would conflict forever on rows Go itself never wrote.
// The store opens with a single connection (sqlite.go SetMaxOpenConns(1)),
// so this Get-then-Update is race-free the same way every other write here
// is: all of them serialize through that one connection.
func (r agentRepo) UpdateIf(ctx context.Context, a, prev storage.Agent) error {
	cur, err := r.Get(ctx, a.ID)
	if err != nil {
		return err
	}
	cur.CreatedAt, cur.UpdatedAt = time.Time{}, time.Time{}
	prev.CreatedAt, prev.UpdatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(cur, prev) {
		return storage.ErrConflict
	}
	return r.Update(ctx, a)
}

func (r agentRepo) Get(ctx context.Context, id string) (storage.Agent, error) {
	return scanAgent(r.db.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE id=?`, id))
}

func (r agentRepo) List(ctx context.Context, projectID string) ([]storage.Agent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+agentCols+` FROM agents WHERE project_id=? ORDER BY sort, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r agentRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM agents WHERE id=?`, id)
}

func (r agentRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return execOne(ctx, r.db, `UPDATE agents SET enabled=?, updated_at=? WHERE id=?`, enabled, fmtTime(time.Now()), id)
}

// ---- repos ----

type repoRepo struct{ db dbtx }

const repoCols = `id, name, path, git_remote, description, created_at, updated_at, default_agent_id`

func scanRepo(row scanner) (storage.Repo, error) {
	var (
		r                storage.Repo
		created, updated string
	)
	if err := row.Scan(&r.ID, &r.Name, &r.Path, &r.GitRemote, &r.Description, &created, &updated, &r.DefaultAgentID); err != nil {
		return r, notFound(err)
	}
	return r, parseTimes([]*time.Time{&r.CreatedAt, &r.UpdatedAt}, created, updated)
}

func (r repoRepo) Create(ctx context.Context, x storage.Repo) (storage.Repo, error) {
	now := time.Now().UTC()
	if x.ID == "" {
		x.ID = ids.New("rep")
	}
	x.CreatedAt, x.UpdatedAt = now, now
	_, err := r.db.ExecContext(ctx, `INSERT INTO repos (`+repoCols+`) VALUES (?,?,?,?,?,?,?,?)`,
		x.ID, x.Name, x.Path, x.GitRemote, x.Description, fmtTime(now), fmtTime(now), x.DefaultAgentID)
	if isUnique(err) {
		return storage.Repo{}, storage.ErrConflict
	}
	return x, err
}

func (r repoRepo) Update(ctx context.Context, x storage.Repo) error {
	return execOne(ctx, r.db, `UPDATE repos SET name=?, git_remote=?, description=?, default_agent_id=?, updated_at=? WHERE id=?`,
		x.Name, x.GitRemote, x.Description, x.DefaultAgentID, fmtTime(time.Now()), x.ID)
}

func (r repoRepo) UpdateInfo(ctx context.Context, id, name, description string) error {
	return execOne(ctx, r.db, `UPDATE repos SET name=?, description=?, updated_at=? WHERE id=?`,
		name, description, fmtTime(time.Now()), id)
}

func (r repoRepo) UpdateInfoRemote(ctx context.Context, id, name, description, gitRemote string, prevUpdatedAt time.Time) error {
	res, err := r.db.ExecContext(ctx, `UPDATE repos SET name=?, description=?, git_remote=?, updated_at=? WHERE id=? AND updated_at=?`,
		name, description, gitRemote, fmtTime(time.Now()), id, fmtTime(prevUpdatedAt))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if _, getErr := r.Get(ctx, id); errors.Is(getErr, storage.ErrNotFound) {
			return storage.ErrNotFound
		}
		return storage.ErrStale
	}
	return nil
}

func (r repoRepo) SetDefaultAgent(ctx context.Context, id, agentID string) error {
	return execOne(ctx, r.db, `UPDATE repos SET default_agent_id=?, updated_at=? WHERE id=?`,
		agentID, fmtTime(time.Now()), id)
}

func (r repoRepo) Get(ctx context.Context, id string) (storage.Repo, error) {
	return scanRepo(r.db.QueryRowContext(ctx, `SELECT `+repoCols+` FROM repos WHERE id=?`, id))
}

func (r repoRepo) GetByPath(ctx context.Context, path string) (storage.Repo, error) {
	return scanRepo(r.db.QueryRowContext(ctx, `SELECT `+repoCols+` FROM repos WHERE path=?`, path))
}

func (r repoRepo) List(ctx context.Context) ([]storage.Repo, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+repoCols+` FROM repos ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Repo
	for rows.Next() {
		x, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r repoRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM repos WHERE id=?`, id)
}

// ---- revisions ----

type revisionRepo struct{ db dbtx }

func (r revisionRepo) Create(ctx context.Context, x storage.Revision) (storage.Revision, error) {
	if x.ID == "" {
		x.ID = ids.New("rev")
	}
	if x.CreatedAt.IsZero() {
		x.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO revisions (id, project_id, action, actor, agent_count, snapshot, created_at) VALUES (?,?,?,?,?,?,?)`,
		x.ID, x.ProjectID, x.Action, x.Actor, x.AgentCount, string(x.Snapshot), fmtTime(x.CreatedAt))
	return x, err
}

func scanRevision(row scanner) (storage.Revision, error) {
	var (
		x             storage.Revision
		snap, created string
	)
	if err := row.Scan(&x.ID, &x.ProjectID, &x.Action, &x.Actor, &x.AgentCount, &snap, &created); err != nil {
		return x, notFound(err)
	}
	x.Snapshot = []byte(snap)
	var err error
	x.CreatedAt, err = parseTime(created)
	return x, err
}

const revCols = `id, project_id, action, actor, agent_count, snapshot, created_at`

func (r revisionRepo) Get(ctx context.Context, id string) (storage.Revision, error) {
	return scanRevision(r.db.QueryRowContext(ctx, `SELECT `+revCols+` FROM revisions WHERE id=?`, id))
}

func (r revisionRepo) List(ctx context.Context, projectID string, limit int) ([]storage.Revision, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+revCols+` FROM revisions WHERE project_id=? ORDER BY created_at DESC, id DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Revision
	for rows.Next() {
		x, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (r revisionRepo) Prune(ctx context.Context, projectID string, keep int) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM revisions WHERE project_id=? AND id NOT IN (
		SELECT id FROM revisions WHERE project_id=? ORDER BY created_at DESC, id DESC LIMIT ?)`, projectID, projectID, keep)
	return err
}
