package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type jobRepo struct{ db dbtx }

const jobCols = `id, project_id, kind, origin, origin_id, trigger, created_by, conversation_id, message_id, task_id, status, error, error_code,
	agent_id, title, cost_usd, input_tokens, output_tokens, duration_ms, dedupe_key, debounce_key, debounce_until, payload, reply,
	next_attempt_at, created_at, started_at, finished_at, output, exit_code, parent_job_id, full_access, full_access_by, extra_dirs`

func scanJob(row scanner) (storage.Job, error) {
	var (
		j                                        storage.Job
		conv, msg, task                          sql.NullString
		debounce, next, started, finished, reply sql.NullString
		created                                  string
		exit                                     sql.NullInt64
		extraDirs                                string
	)
	if err := row.Scan(&j.ID, &j.ProjectID, &j.Kind, &j.Origin, &j.OriginID, &j.Trigger, &j.CreatedBy, &conv, &msg, &task, &j.Status, &j.Error,
		&j.ErrorCode, &j.AgentID, &j.Title, &j.CostUSD, &j.InputTokens, &j.OutputTokens, &j.DurationMS, &j.DedupeKey, &j.DebounceKey,
		&debounce, &j.Payload, &reply, &next, &created, &started, &finished, &j.Output, &exit, &j.ParentJobID, &j.FullAccess, &j.FullAccessBy, &extraDirs); err != nil {
		return j, notFound(err)
	}
	if exit.Valid {
		c := int(exit.Int64)
		j.ExitCode = &c
	}
	j.ConversationID, j.MessageID, j.TaskID = conv.String, msg.String, task.String
	if reply.Valid && reply.String != "" && reply.String != "{}" {
		_ = json.Unmarshal([]byte(reply.String), &j.Reply)
	}
	if extraDirs != "" && extraDirs != "[]" {
		_ = json.Unmarshal([]byte(extraDirs), &j.ExtraDirs)
	}
	var err error
	if j.CreatedAt, err = parseTime(created); err != nil {
		return j, err
	}
	for _, x := range []struct {
		src sql.NullString
		dst **time.Time
	}{{debounce, &j.DebounceUntil}, {next, &j.NextAttemptAt}, {started, &j.StartedAt}, {finished, &j.FinishedAt}} {
		if x.src.Valid && x.src.String != "" {
			t, err := parseTime(x.src.String)
			if err != nil {
				return j, err
			}
			*x.dst = &t
		}
	}
	return j, nil
}

func replyJSON(r map[string]any) string {
	if len(r) == 0 {
		return "{}"
	}
	return toJSON(r)
}

func extraDirsJSON(dirs []string) string {
	if len(dirs) == 0 {
		return "[]"
	}
	return toJSON(dirs)
}

func (r jobRepo) Create(ctx context.Context, j storage.Job) (storage.Job, error) {
	if j.ID == "" {
		j.ID = ids.New("job")
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	if j.Trigger == "" {
		j.Trigger = "ui"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO jobs (`+jobCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, j.ProjectID, j.Kind, j.Origin, j.OriginID, j.Trigger, j.CreatedBy, nullStr(j.ConversationID), nullStr(j.MessageID), nullStr(j.TaskID),
		j.Status, j.Error, j.ErrorCode, j.AgentID, j.Title, j.CostUSD, j.InputTokens, j.OutputTokens, j.DurationMS, j.DedupeKey, j.DebounceKey,
		optTime(j.DebounceUntil), j.Payload, replyJSON(j.Reply), optTime(j.NextAttemptAt), fmtTime(j.CreatedAt), optTime(j.StartedAt), optTime(j.FinishedAt),
		j.Output, exitArg(j.ExitCode), j.ParentJobID, j.FullAccess, j.FullAccessBy, extraDirsJSON(j.ExtraDirs))
	if isUnique(err) {
		return j, storage.ErrConflict
	}
	return j, err
}

func exitArg(c *int) any {
	if c == nil {
		return nil
	}
	return *c
}

func (r jobRepo) SetOutput(ctx context.Context, id, output string, exitCode int) error {
	return execOne(ctx, r.db, `UPDATE jobs SET output=?, exit_code=? WHERE id=?`, output, exitCode, id)
}

func (r jobRepo) Get(ctx context.Context, id string) (storage.Job, error) {
	return scanJob(r.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id=?`, id))
}

func (r jobRepo) Update(ctx context.Context, j storage.Job) error {
	return execOne(ctx, r.db, `UPDATE jobs SET conversation_id=?, message_id=?, task_id=?, status=?, error=?, error_code=?, agent_id=?, title=?,
		debounce_until=?, payload=?, reply=?, next_attempt_at=?, started_at=?, full_access=?, full_access_by=?, extra_dirs=? WHERE id=?`,
		nullStr(j.ConversationID), nullStr(j.MessageID), nullStr(j.TaskID), j.Status, j.Error, j.ErrorCode, j.AgentID, j.Title,
		optTime(j.DebounceUntil), j.Payload, replyJSON(j.Reply), optTime(j.NextAttemptAt), optTime(j.StartedAt), j.FullAccess, j.FullAccessBy, extraDirsJSON(j.ExtraDirs), j.ID)
}

func (r jobRepo) Finish(ctx context.Context, id, status, errCode, errMsg string, at time.Time) (storage.Job, error) {
	if err := execOne(ctx, r.db, `UPDATE jobs SET status=?, error_code=?, error=?, finished_at=?,
		cost_usd=(SELECT COALESCE(SUM(cost_usd),0) FROM runs WHERE job_id=jobs.id),
		input_tokens=(SELECT COALESCE(SUM(input_tokens),0) FROM runs WHERE job_id=jobs.id),
		output_tokens=(SELECT COALESCE(SUM(output_tokens),0) FROM runs WHERE job_id=jobs.id),
		duration_ms=MAX(0, CAST(ROUND((julianday(?) - julianday(COALESCE(started_at, created_at))) * 86400000) AS INTEGER))
		WHERE id=?`, status, errCode, errMsg, fmtTime(at), fmtTime(at), id); err != nil {
		return storage.Job{}, err
	}
	return r.Get(ctx, id)
}

func (r jobRepo) Claim(ctx context.Context, now time.Time, limit int, busy []string) ([]storage.Job, error) {
	if limit <= 0 {
		return nil, nil
	}
	if len(busy) == 0 {
		busy = []string{""}
	}
	args := []any{fmtTime(now), fmtTime(now)}
	for _, b := range busy {
		args = append(args, b)
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `UPDATE jobs SET status='running', started_at=?
		WHERE id IN (SELECT id FROM jobs WHERE status='pending' AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			AND (origin_id = '' OR origin_id NOT IN (?`+strings.Repeat(",?", len(busy)-1)+`))
			ORDER BY next_attempt_at, created_at LIMIT ?)
		RETURNING `+jobCols, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Two due jobs of one origin: keep the first running, put the rest back.
	var keep []storage.Job
	taken := map[string]bool{}
	for _, j := range out {
		if j.OriginID != "" && taken[j.OriginID] {
			if _, err := r.db.ExecContext(ctx, `UPDATE jobs SET status='pending', started_at=NULL WHERE id=?`, j.ID); err != nil {
				return nil, err
			}
			continue
		}
		taken[j.OriginID] = true
		keep = append(keep, j)
	}
	return keep, nil
}

func (r jobRepo) where(f storage.JobFilter) (string, []any) {
	var conds []string
	var args []any
	add := func(col, v string) {
		if v != "" {
			conds = append(conds, col+"=?")
			args = append(args, v)
		}
	}
	add("project_id", f.ProjectID)
	add("kind", f.Kind)
	add("origin", f.Origin)
	add("origin_id", f.OriginID)
	add("task_id", f.TaskID)
	add("conversation_id", f.ConversationID)
	switch f.Source { // a bot's trigger names it; web = a person on the dashboard
	case "discord", "telegram":
		conds = append(conds, "trigger=?")
		args = append(args, f.Source)
	case "web":
		conds = append(conds, "origin<>'automation' AND trigger NOT IN ('discord','telegram')")
	case "auto":
		conds = append(conds, "origin='automation' AND trigger NOT IN ('discord','telegram')")
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		conds = append(conds, "title LIKE ? ESCAPE '\\'")
		args = append(args, "%"+strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q)+"%")
	}
	if len(f.OriginIDs) > 0 {
		conds = append(conds, "origin_id IN (?"+strings.Repeat(",?", len(f.OriginIDs)-1)+")")
		for _, id := range f.OriginIDs {
			args = append(args, id)
		}
	}
	add("status", f.Status)
	add("agent_id", f.AgentID)
	if !f.Since.IsZero() {
		conds = append(conds, "created_at >= ?")
		args = append(args, fmtTime(f.Since))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (r jobRepo) List(ctx context.Context, f storage.JobFilter) ([]storage.Job, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	f.Limit = min(f.Limit, 200)
	w, args := r.where(f)
	if f.Before != "" {
		if w == "" {
			w = " WHERE "
		} else {
			w += " AND "
		}
		w += "(created_at, id) < (SELECT created_at, id FROM jobs WHERE id=?)"
		args = append(args, f.Before)
	}
	args = append(args, f.Limit)
	return r.query(ctx, `SELECT `+jobCols+` FROM jobs`+w+` ORDER BY created_at DESC, id DESC LIMIT ?`, args...)
}

func (r jobRepo) query(ctx context.Context, q string, args ...any) ([]storage.Job, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r jobRepo) Active(ctx context.Context, origin, originID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE origin=? AND origin_id=? AND status IN ('pending','running')`, origin, originID).Scan(&n)
	return n, err
}

func (r jobRepo) ByDedupe(ctx context.Context, originID, key string, since time.Time) (storage.Job, error) {
	return scanJob(r.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE origin_id=? AND dedupe_key=? AND created_at >= ?`,
		originID, key, fmtTime(since)))
}

func (r jobRepo) ByDebounce(ctx context.Context, originID, key string) (storage.Job, error) {
	return scanJob(r.db.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE origin_id=? AND debounce_key=? AND status='pending'
		ORDER BY created_at DESC LIMIT 1`, originID, key))
}

func (r jobRepo) Debounce(ctx context.Context, id, payload string, next time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE jobs SET payload=?, next_attempt_at=? WHERE id=? AND status='pending'`, payload, fmtTime(next), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r jobRepo) ClearDedupe(ctx context.Context, originID, key string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE jobs SET dedupe_key='' WHERE origin_id=? AND dedupe_key=?`, originID, key)
	return err
}

func (r jobRepo) Stats(ctx context.Context, f storage.JobFilter, by string) ([]storage.JobStats, error) {
	w, args := r.where(f)
	jobs, err := r.query(ctx, `SELECT `+jobCols+` FROM jobs`+w+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	key := func(j storage.Job) string {
		switch by {
		case "kind":
			return j.Kind
		case "origin":
			return j.Origin
		case "agent":
			return j.AgentID
		case "status":
			return j.Status
		default:
			return j.CreatedAt.In(time.Local).Format(time.DateOnly)
		}
	}
	rows := map[string]*storage.JobStats{}
	durations := map[string][]int64{}
	var order []string
	for _, j := range jobs {
		k := key(j)
		s := rows[k]
		if s == nil {
			s = &storage.JobStats{Key: k}
			rows[k] = s
			order = append(order, k)
		}
		s.Jobs++
		s.CostUSD += j.CostUSD
		switch j.Status {
		case "done":
			s.Done++
		case "failed":
			s.Failed++
		}
		if j.FinishedAt != nil && j.DurationMS > 0 {
			durations[k] = append(durations[k], j.DurationMS)
		}
	}
	out := make([]storage.JobStats, 0, len(order))
	for _, k := range order {
		s := rows[k]
		d := durations[k]
		slices.Sort(d)
		s.P50MS, s.P95MS = percentile(d, 0.5), percentile(d, 0.95)
		out = append(out, *s)
	}
	return out, nil
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[min(int(float64(len(sorted)-1)*p+0.5), len(sorted)-1)]
}

func (r jobRepo) CostSince(ctx context.Context, origin, originID string, since time.Time) (float64, error) {
	var v float64
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0) FROM jobs WHERE origin=? AND origin_id=? AND created_at >= ?`,
		origin, originID, fmtTime(since)).Scan(&v)
	return v, err
}

func (r jobRepo) FailRunning(ctx context.Context, errCode, errMsg string, at time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE jobs SET status='failed', error_code=?, error=?, finished_at=?,
		cost_usd=(SELECT COALESCE(SUM(cost_usd),0) FROM runs WHERE job_id=jobs.id),
		input_tokens=(SELECT COALESCE(SUM(input_tokens),0) FROM runs WHERE job_id=jobs.id),
		output_tokens=(SELECT COALESCE(SUM(output_tokens),0) FROM runs WHERE job_id=jobs.id)
		WHERE status='running'`, errCode, errMsg, fmtTime(at))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// jobGroupKey names the work a job belongs to: its chat, else its task, else
// itself (each run of an automation is a piece of work of its own).
const jobGroupKey = `CASE WHEN COALESCE(conversation_id,'')<>'' THEN 'c:'||conversation_id WHEN COALESCE(task_id,'')<>'' THEN 't:'||task_id
	ELSE 'j:'||id END`

func (r jobRepo) Groups(ctx context.Context, f storage.JobFilter) ([]storage.JobGroup, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	f.Limit = min(f.Limit, 100)
	w, args := r.where(f)
	having := ""
	if f.Before != "" { // groups last active before that one
		having = ` HAVING (MAX(created_at), g) < (SELECT MAX(created_at), g FROM (SELECT created_at, ` + jobGroupKey + ` AS g FROM jobs` + w + `) WHERE g=?)`
		args = append(args, args...)
		args = append(args, f.Before)
	}
	args = append(args, f.Limit)
	// SQLite: the bare columns come from the row of MAX(created_at), the latest job
	q := `SELECT g, id, project_id, kind, origin, COALESCE(origin_id,''), COALESCE(trigger,''), COALESCE(created_by,''), COALESCE(conversation_id,''), COALESCE(task_id,''), COALESCE(agent_id,''), COALESCE(title,''), status,
		COUNT(*), SUM(status='failed'), SUM(status IN ('pending','running')), COALESCE(SUM(cost_usd),0), MAX(created_at)
		FROM (SELECT *, ` + jobGroupKey + ` AS g FROM jobs` + w + `) GROUP BY g` + having + ` ORDER BY MAX(created_at) DESC, g DESC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.JobGroup{}
	for rows.Next() {
		var g storage.JobGroup
		var last string
		if err := rows.Scan(&g.Key, &g.LatestID, &g.ProjectID, &g.Kind, &g.Origin, &g.OriginID, &g.Trigger, &g.CreatedBy, &g.ConversationID, &g.TaskID, &g.AgentID,
			&g.Title, &g.Status, &g.Runs, &g.Failed, &g.Active, &g.CostUSD, &last); err != nil {
			return nil, err
		}
		if g.LastAt, err = parseTime(last); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
