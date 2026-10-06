package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	is_default, enabled, status, status_detail, checked_at, created_at, updated_at, preset`

func scanProvider(row scanner) (storage.Provider, error) {
	var (
		p                   storage.Provider
		kind, tiers, models string
		isDefault, enabled  int
		checked             sql.NullString
		created, updated    string
	)
	if err := row.Scan(&p.ID, &p.Name, &kind, &p.BaseURL, &p.APIKeyEnc, &p.APIKeyEnv, &p.APIKeyHint, &tiers, &models,
		&isDefault, &enabled, &p.Status, &p.StatusDetail, &checked, &created, &updated, &p.Preset); err != nil {
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
	_, err := r.db.ExecContext(ctx, `INSERT INTO providers (`+providerCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, string(p.Kind), p.BaseURL, p.APIKeyEnc, p.APIKeyEnv, p.APIKeyHint, toJSON(p.TierModels), toJSON(p.Models),
		boolInt(p.IsDefault), boolInt(p.Enabled), p.Status, p.StatusDetail, nil, fmtTime(now), fmtTime(now), p.Preset)
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
		tier_models=?, enabled=?, preset=?, updated_at=? WHERE id=?`,
		p.Name, string(p.Kind), p.BaseURL, p.APIKeyEnc, p.APIKeyEnv, p.APIKeyHint, toJSON(p.TierModels), boolInt(p.Enabled),
		p.Preset, fmtTime(time.Now()), p.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
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

// ---- org models ----

type orgModelRepo struct{ db dbtx }

const orgCols = `id, repo_id, source_template_id, key, name, description, kind, governance, builtin, created_at, updated_at`

func scanOrg(row scanner) (storage.OrgModel, error) {
	var (
		m                storage.OrgModel
		repoID, source   sql.NullString
		gov              string
		builtin          int
		created, updated string
	)
	if err := row.Scan(&m.ID, &repoID, &source, &m.Key, &m.Name, &m.Description, &m.Kind, &gov, &builtin, &created, &updated); err != nil {
		return m, notFound(err)
	}
	m.RepoID, m.SourceTemplateID, m.Builtin = repoID.String, source.String, builtin != 0
	if err := json.Unmarshal([]byte(gov), &m.Governance); err != nil {
		return m, err
	}
	return m, parseTimes([]*time.Time{&m.CreatedAt, &m.UpdatedAt}, created, updated)
}

func (r orgModelRepo) Create(ctx context.Context, m storage.OrgModel) (storage.OrgModel, error) {
	now := time.Now().UTC()
	if m.ID == "" {
		m.ID = ids.New("org")
	}
	m.CreatedAt, m.UpdatedAt = now, now
	_, err := r.db.ExecContext(ctx, `INSERT INTO org_models (`+orgCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, nullStr(m.RepoID), nullStr(m.SourceTemplateID), m.Key, m.Name, m.Description, m.Kind, toJSON(m.Governance),
		boolInt(m.Builtin), fmtTime(now), fmtTime(now))
	if isUnique(err) {
		return storage.OrgModel{}, storage.ErrConflict
	}
	return m, err
}

func (r orgModelRepo) Update(ctx context.Context, m storage.OrgModel) error {
	err := execOne(ctx, r.db, `UPDATE org_models SET key=?, name=?, description=?, kind=?, governance=?, source_template_id=?, updated_at=? WHERE id=?`,
		m.Key, m.Name, m.Description, m.Kind, toJSON(m.Governance), nullStr(m.SourceTemplateID), fmtTime(time.Now()), m.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

func (r orgModelRepo) Get(ctx context.Context, id string) (storage.OrgModel, error) {
	return scanOrg(r.db.QueryRowContext(ctx, `SELECT `+orgCols+` FROM org_models WHERE id=?`, id))
}

func (r orgModelRepo) GetTemplateByKey(ctx context.Context, key string) (storage.OrgModel, error) {
	return scanOrg(r.db.QueryRowContext(ctx, `SELECT `+orgCols+` FROM org_models WHERE repo_id IS NULL AND key=?`, key))
}

func (r orgModelRepo) GetForRepo(ctx context.Context, repoID string) (storage.OrgModel, error) {
	return scanOrg(r.db.QueryRowContext(ctx, `SELECT `+orgCols+` FROM org_models WHERE repo_id=?`, repoID))
}

func (r orgModelRepo) ListTemplates(ctx context.Context) ([]storage.OrgModel, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+orgCols+` FROM org_models WHERE repo_id IS NULL ORDER BY builtin DESC, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.OrgModel
	for rows.Next() {
		m, err := scanOrg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r orgModelRepo) Delete(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM org_models WHERE id=?`, id)
}

// ---- agents ----

type agentRepo struct{ db dbtx }

func idsOrEmpty(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

const agentCols = `id, org_model_id, key, name, tier, role, description, reports_to, provider_id, model_tier, llm_model,
	instructions, permissions, sort, created_at, updated_at, avatar, enabled, fallback_provider_ids, effort`

func scanAgent(row scanner) (storage.Agent, error) {
	var (
		a                storage.Agent
		reports, perms   string
		avatar, fallback string
		provider         sql.NullString
		created, updated string
		enabled          bool
	)
	if err := row.Scan(&a.ID, &a.OrgModelID, &a.Key, &a.Name, &a.Tier, &a.Role, &a.Description, &reports, &provider, &a.ModelTier,
		&a.LLMModel, &a.Instructions, &perms, &a.Sort, &created, &updated, &avatar, &enabled, &fallback, &a.Effort); err != nil {
		return a, notFound(err)
	}
	a.ProviderID, a.Disabled = provider.String, !enabled
	_ = json.Unmarshal([]byte(fallback), &a.FallbackProviderIDs)
	if err := json.Unmarshal([]byte(reports), &a.ReportsTo); err != nil {
		return a, err
	}
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
	if a.ReportsTo == nil {
		a.ReportsTo = []string{}
	}
	a.CreatedAt, a.UpdatedAt = now, now
	_, err := r.db.ExecContext(ctx, `INSERT INTO agents (`+agentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.OrgModelID, a.Key, a.Name, a.Tier, a.Role, a.Description, toJSON(a.ReportsTo), nullStr(a.ProviderID), a.ModelTier,
		a.LLMModel, a.Instructions, toJSON(a.Permissions), a.Sort, fmtTime(now), fmtTime(now), toJSON(a.Avatar), !a.Disabled,
		toJSON(idsOrEmpty(a.FallbackProviderIDs)), a.Effort)
	if isUnique(err) {
		return storage.Agent{}, storage.ErrConflict
	}
	return a, err
}

func (r agentRepo) Update(ctx context.Context, a storage.Agent) error {
	if a.ReportsTo == nil {
		a.ReportsTo = []string{}
	}
	err := execOne(ctx, r.db, `UPDATE agents SET key=?, name=?, tier=?, role=?, description=?, reports_to=?, provider_id=?,
		model_tier=?, llm_model=?, instructions=?, permissions=?, sort=?, avatar=?, fallback_provider_ids=?, effort=?, updated_at=? WHERE id=?`,
		a.Key, a.Name, a.Tier, a.Role, a.Description, toJSON(a.ReportsTo), nullStr(a.ProviderID), a.ModelTier, a.LLMModel,
		a.Instructions, toJSON(a.Permissions), a.Sort, toJSON(a.Avatar), toJSON(idsOrEmpty(a.FallbackProviderIDs)), a.Effort, fmtTime(time.Now()), a.ID)
	if isUnique(err) {
		return storage.ErrConflict
	}
	return err
}

func (r agentRepo) Get(ctx context.Context, id string) (storage.Agent, error) {
	return scanAgent(r.db.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE id=?`, id))
}

func (r agentRepo) List(ctx context.Context, orgModelID string) ([]storage.Agent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+agentCols+` FROM agents WHERE org_model_id=?
		ORDER BY CASE tier WHEN 'lead' THEN 0 WHEN 'manager' THEN 1 ELSE 2 END, sort, created_at`, orgModelID)
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

const repoCols = `id, name, path, git_remote, description, created_at, updated_at`

func scanRepo(row scanner) (storage.Repo, error) {
	var (
		r                storage.Repo
		created, updated string
	)
	if err := row.Scan(&r.ID, &r.Name, &r.Path, &r.GitRemote, &r.Description, &created, &updated); err != nil {
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
	_, err := r.db.ExecContext(ctx, `INSERT INTO repos (`+repoCols+`) VALUES (?,?,?,?,?,?,?)`,
		x.ID, x.Name, x.Path, x.GitRemote, x.Description, fmtTime(now), fmtTime(now))
	if isUnique(err) {
		return storage.Repo{}, storage.ErrConflict
	}
	return x, err
}

func (r repoRepo) Update(ctx context.Context, x storage.Repo) error {
	return execOne(ctx, r.db, `UPDATE repos SET name=?, git_remote=?, description=?, updated_at=? WHERE id=?`,
		x.Name, x.GitRemote, x.Description, fmtTime(time.Now()), x.ID)
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
	_, err := r.db.ExecContext(ctx, `INSERT INTO org_revisions (id, org_model_id, action, actor, agent_count, snapshot, created_at) VALUES (?,?,?,?,?,?,?)`,
		x.ID, x.OrgModelID, x.Action, x.Actor, x.AgentCount, string(x.Snapshot), fmtTime(x.CreatedAt))
	return x, err
}

func scanRevision(row scanner) (storage.Revision, error) {
	var (
		x             storage.Revision
		snap, created string
	)
	if err := row.Scan(&x.ID, &x.OrgModelID, &x.Action, &x.Actor, &x.AgentCount, &snap, &created); err != nil {
		return x, notFound(err)
	}
	x.Snapshot = []byte(snap)
	var err error
	x.CreatedAt, err = parseTime(created)
	return x, err
}

const revCols = `id, org_model_id, action, actor, agent_count, snapshot, created_at`

func (r revisionRepo) Get(ctx context.Context, id string) (storage.Revision, error) {
	return scanRevision(r.db.QueryRowContext(ctx, `SELECT `+revCols+` FROM org_revisions WHERE id=?`, id))
}

func (r revisionRepo) List(ctx context.Context, orgModelID string, limit int) ([]storage.Revision, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+revCols+` FROM org_revisions WHERE org_model_id=? ORDER BY created_at DESC, id DESC LIMIT ?`, orgModelID, limit)
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

func (r revisionRepo) Prune(ctx context.Context, orgModelID string, keep int) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM org_revisions WHERE org_model_id=? AND id NOT IN (
		SELECT id FROM org_revisions WHERE org_model_id=? ORDER BY created_at DESC, id DESC LIMIT ?)`, orgModelID, orgModelID, keep)
	return err
}
