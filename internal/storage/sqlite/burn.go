package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type burnRepo struct{ db dbtx }

func (s *Store) Burn() storage.BurnRepo { return burnRepo{s.q} }

const burnSessionCols = `id, project_id, conversation_id, agent_id, model_tier, max_parallel, result_mode, focus, work_order, ends_at, state, waiting_until, started_by, started_at, created_at, updated_at, review_profile_id, scanned, notify_channel_id, notify_chat_id, run_branch, hunt_template, hunt_prompt, code_map, verify, lessons, review_cap, stop_after, coverage, run_plan`

func scanBurnSession(row interface{ Scan(...any) error }) (storage.BurnSession, error) {
	var s storage.BurnSession
	var ends, waiting, started sql.NullString
	var created, updated, plan string
	err := row.Scan(&s.ID, &s.ProjectID, &s.ConversationID, &s.AgentID, &s.ModelTier, &s.MaxParallel, &s.ResultMode, &s.Focus, &s.Order, &ends, &s.State, &waiting, &s.StartedBy, &started, &created, &updated,
		&s.ReviewProfileID, &s.Scanned, &s.NotifyChannelID, &s.NotifyChatID, &s.RunBranch, &s.Template, &s.HuntPrompt, &s.CodeMap, &s.Verify, &s.Lessons, &s.ReviewCap, &s.StopAfter, &s.Coverage, &plan)
	if errors.Is(err, sql.ErrNoRows) {
		return s, storage.ErrNotFound
	}
	if err != nil {
		return s, err
	}
	s.EndsAt, s.WaitingUntil, s.StartedAt = burnOpt(ends), burnOpt(waiting), burnOpt(started)
	_ = json.Unmarshal([]byte(plan), &s.Plan) // unreadable: the defaults
	return s, parseTimes([]*time.Time{&s.CreatedAt, &s.UpdatedAt}, created, updated)
}

func burnOpt(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := parseTime(v.String)
	if err != nil {
		return nil
	}
	return &t
}

func (r burnRepo) Session(ctx context.Context, projectID string) (storage.BurnSession, error) {
	return scanBurnSession(r.db.QueryRowContext(ctx, `SELECT `+burnSessionCols+` FROM burn_sessions WHERE project_id=?`, projectID))
}

func (r burnRepo) SessionByID(ctx context.Context, id string) (storage.BurnSession, error) {
	return scanBurnSession(r.db.QueryRowContext(ctx, `SELECT `+burnSessionCols+` FROM burn_sessions WHERE id=?`, id))
}

func (r burnRepo) SessionByConversation(ctx context.Context, conversationID string) (storage.BurnSession, error) {
	if conversationID == "" {
		return storage.BurnSession{}, storage.ErrNotFound
	}
	return scanBurnSession(r.db.QueryRowContext(ctx, `SELECT `+burnSessionCols+` FROM burn_sessions WHERE conversation_id=? OR id IN (SELECT session_id FROM burn_items WHERE work_conversation_id=?)
		OR id IN (SELECT i.session_id FROM burn_items i, json_each(i.review_conversations) j WHERE j.value=?) LIMIT 1`, conversationID, conversationID, conversationID))
}

func (r burnRepo) SaveSession(ctx context.Context, s storage.BurnSession) (storage.BurnSession, error) {
	now := time.Now().UTC()
	if s.ID == "" {
		s.ID, s.CreatedAt = ids.New("brn"), now
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	s.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `INSERT INTO burn_sessions (`+burnSessionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET conversation_id=excluded.conversation_id, agent_id=excluded.agent_id, model_tier=excluded.model_tier,
		max_parallel=excluded.max_parallel, result_mode=excluded.result_mode, focus=excluded.focus, work_order=excluded.work_order, ends_at=excluded.ends_at, state=excluded.state,
		waiting_until=excluded.waiting_until, started_by=excluded.started_by, started_at=excluded.started_at, updated_at=excluded.updated_at,
		review_profile_id=excluded.review_profile_id, scanned=excluded.scanned, notify_channel_id=excluded.notify_channel_id, notify_chat_id=excluded.notify_chat_id, run_branch=excluded.run_branch,
		hunt_template=excluded.hunt_template, hunt_prompt=excluded.hunt_prompt, verify=excluded.verify, review_cap=excluded.review_cap, stop_after=excluded.stop_after, run_plan=excluded.run_plan`,
		s.ID, s.ProjectID, s.ConversationID, s.AgentID, s.ModelTier, s.MaxParallel, s.ResultMode, s.Focus, cmp.Or(s.Order, "roadmap"), optTime(s.EndsAt), s.State, optTime(s.WaitingUntil),
		s.StartedBy, optTime(s.StartedAt), fmtTime(s.CreatedAt), fmtTime(s.UpdatedAt),
		s.ReviewProfileID, s.Scanned, s.NotifyChannelID, s.NotifyChatID, s.RunBranch, cmp.Or(s.Template, "general"), s.HuntPrompt, s.CodeMap, s.Verify, s.Lessons, s.ReviewCap, s.StopAfter, s.Coverage, toJSON(s.Plan))
	if isUnique(err) {
		return s, storage.ErrConflict
	}
	return s, err
}

func (r burnRepo) SetScanned(ctx context.Context, id, scanned string) error {
	return execOne(ctx, r.db, `UPDATE burn_sessions SET scanned=? WHERE id=?`, scanned, id)
}

// SetCodeMap: code_map is only written here, never by SaveSession (a loop
// saving the session it read earlier would undo a scan's map).
func (r burnRepo) SetCodeMap(ctx context.Context, id, codeMap string) error {
	return execOne(ctx, r.db, `UPDATE burn_sessions SET code_map=? WHERE id=?`, codeMap, id)
}

// SetCoverage: like code_map, the coverage plan is only written here.
func (r burnRepo) SetCoverage(ctx context.Context, id, coverage string) error {
	return execOne(ctx, r.db, `UPDATE burn_sessions SET coverage=? WHERE id=?`, coverage, id)
}

// SetLessons: like code_map, lessons are only written here.
func (r burnRepo) SetLessons(ctx context.Context, id, lessons string) error {
	return execOne(ctx, r.db, `UPDATE burn_sessions SET lessons=? WHERE id=?`, lessons, id)
}

func (r burnRepo) Running(ctx context.Context) ([]storage.BurnSession, error) {
	return r.sessions(ctx, `state IN ('running','waiting_limit','draining')`)
}

func (r burnRepo) Scheduled(ctx context.Context) ([]storage.BurnSession, error) {
	return r.sessions(ctx, `json_extract(run_plan, '$.auto') = 1`)
}

func (r burnRepo) sessions(ctx context.Context, where string) ([]storage.BurnSession, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+burnSessionCols+` FROM burn_sessions WHERE `+where)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.BurnSession
	for rows.Next() {
		s, err := scanBurnSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const burnItemCols = `id, session_id, title, kind, detail, status, priority, branch, worktree, summary, attempts, subagents, cost_usd, created_at, updated_at, reviewed, review_note, review_conversations, work_conversation_id, review_err_attempts, run_branch, files, owner, restarts`

func scanBurnItem(row interface{ Scan(...any) error }) (storage.BurnItem, error) {
	var it storage.BurnItem
	var created, updated, reviewed, convs, files string
	err := row.Scan(&it.ID, &it.SessionID, &it.Title, &it.Kind, &it.Detail, &it.Status, &it.Priority, &it.Branch, &it.Worktree, &it.Summary, &it.Attempts, &it.Subagents, &it.CostUSD, &created, &updated,
		&reviewed, &it.ReviewNote, &convs, &it.WorkConversationID, &it.ReviewErrAttempts, &it.RunBranch, &files, &it.Owner, &it.Restarts)
	if errors.Is(err, sql.ErrNoRows) {
		return it, storage.ErrNotFound
	}
	if err != nil {
		return it, err
	}
	it.Reviewed = splitList(reviewed)
	if files != "" {
		it.Files = strings.Split(files, "\n")
	}
	_ = json.Unmarshal([]byte(convs), &it.ReviewConversations)
	return it, parseTimes([]*time.Time{&it.CreatedAt, &it.UpdatedAt}, created, updated)
}

func (r burnRepo) AddItem(ctx context.Context, it storage.BurnItem) (storage.BurnItem, error) {
	now := time.Now().UTC()
	it.ID, it.CreatedAt, it.UpdatedAt = ids.New("bit"), now, now
	if it.Status == "" {
		it.Status = "found"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO burn_items (`+burnItemCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		it.ID, it.SessionID, it.Title, it.Kind, it.Detail, it.Status, it.Priority, it.Branch, it.Worktree, it.Summary, it.Attempts, it.Subagents, it.CostUSD, fmtTime(now), fmtTime(now),
		strings.Join(it.Reviewed, ","), it.ReviewNote, jsonMap(it.ReviewConversations), it.WorkConversationID, it.ReviewErrAttempts, it.RunBranch, strings.Join(it.Files, "\n"), it.Owner, it.Restarts)
	return it, err
}

func (r burnRepo) DeleteItem(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM burn_items WHERE id=?`, id)
}

func (r burnRepo) UpdateItem(ctx context.Context, it storage.BurnItem) error {
	return execOne(ctx, r.db, `UPDATE burn_items SET title=?, kind=?, detail=?, status=?, priority=?, branch=?, worktree=?, summary=?, attempts=?, subagents=?, cost_usd=?, reviewed=?, review_note=?, review_conversations=?, work_conversation_id=?, review_err_attempts=?, run_branch=?, files=?, owner=?, restarts=?, updated_at=? WHERE id=?`,
		it.Title, it.Kind, it.Detail, it.Status, it.Priority, it.Branch, it.Worktree, it.Summary, it.Attempts, it.Subagents, it.CostUSD, strings.Join(it.Reviewed, ","), it.ReviewNote, jsonMap(it.ReviewConversations), it.WorkConversationID, it.ReviewErrAttempts, it.RunBranch, strings.Join(it.Files, "\n"), it.Owner, it.Restarts, fmtTime(time.Now()), it.ID)
}

func (r burnRepo) UpdateItemFrom(ctx context.Context, it storage.BurnItem, fromStatus string) error {
	if err := execOne(ctx, r.db, `UPDATE burn_items SET title=?, kind=?, detail=?, status=?, priority=?, branch=?, worktree=?, summary=?, attempts=?, subagents=?, cost_usd=?, reviewed=?, review_note=?, review_conversations=?, work_conversation_id=?, review_err_attempts=?, run_branch=?, files=?, owner=?, restarts=?, updated_at=? WHERE id=? AND status=?`,
		it.Title, it.Kind, it.Detail, it.Status, it.Priority, it.Branch, it.Worktree, it.Summary, it.Attempts, it.Subagents, it.CostUSD, strings.Join(it.Reviewed, ","), it.ReviewNote, jsonMap(it.ReviewConversations), it.WorkConversationID, it.ReviewErrAttempts, it.RunBranch, strings.Join(it.Files, "\n"), it.Owner, it.Restarts, fmtTime(time.Now()), it.ID, fromStatus); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return storage.ErrConflict
		}
		return err
	}
	return nil
}

func (r burnRepo) Item(ctx context.Context, id string) (storage.BurnItem, error) {
	return scanBurnItem(r.db.QueryRowContext(ctx, `SELECT `+burnItemCols+` FROM burn_items WHERE id=?`, id))
}

func (r burnRepo) Items(ctx context.Context, sessionID string) ([]storage.BurnItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+burnItemCols+` FROM burn_items WHERE session_id=? ORDER BY priority DESC, created_at`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.BurnItem{}
	for rows.Next() {
		it, err := scanBurnItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// splitList reads a comma list ("" = none).
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func jsonMap[V any](m map[string]V) string {
	if len(m) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(m)
	return string(b)
}

const burnProfileCols = `id, project_id, name, stages, created_at, updated_at`

func scanBurnProfile(row interface{ Scan(...any) error }) (storage.BurnReviewProfile, error) {
	var p storage.BurnReviewProfile
	var stages, created, updated string
	err := row.Scan(&p.ID, &p.ProjectID, &p.Name, &stages, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return p, storage.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Stages = map[string]storage.BurnReviewStage{}
	_ = json.Unmarshal([]byte(stages), &p.Stages)
	return p, parseTimes([]*time.Time{&p.CreatedAt, &p.UpdatedAt}, created, updated)
}

func (r burnRepo) ReviewProfiles(ctx context.Context, projectID string) ([]storage.BurnReviewProfile, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+burnProfileCols+` FROM burn_review_profiles WHERE project_id=? ORDER BY name, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.BurnReviewProfile{}
	for rows.Next() {
		p, err := scanBurnProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r burnRepo) ReviewProfile(ctx context.Context, id string) (storage.BurnReviewProfile, error) {
	return scanBurnProfile(r.db.QueryRowContext(ctx, `SELECT `+burnProfileCols+` FROM burn_review_profiles WHERE id=?`, id))
}

func (r burnRepo) SaveReviewProfile(ctx context.Context, p storage.BurnReviewProfile) (storage.BurnReviewProfile, error) {
	now := time.Now().UTC()
	if p.ID == "" {
		p.ID, p.CreatedAt = ids.New("brp"), now
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	_, err := r.db.ExecContext(ctx, `INSERT INTO burn_review_profiles (`+burnProfileCols+`) VALUES (?,?,?,?,?,?)
		ON CONFLICT (id) DO UPDATE SET name=excluded.name, stages=excluded.stages, updated_at=excluded.updated_at`,
		p.ID, p.ProjectID, p.Name, jsonMap(p.Stages), fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt))
	return p, err
}

func (r burnRepo) DeleteReviewProfile(ctx context.Context, id string) error {
	if err := execOne(ctx, r.db, `DELETE FROM burn_review_profiles WHERE id=?`, id); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `UPDATE burn_sessions SET review_profile_id='' WHERE review_profile_id=?`, id)
	return err
}
