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

// The support pack's data (ADR-171).

type supportRepo struct{ db dbtx }

func (s *Store) Support() storage.SupportRepo { return supportRepo{s.q} }

const sourceCols = `id, project_id, kind, name, automation_id, mode, token_hash, config, rate_per_min, enabled, created_at, updated_at, secret_enc`

func scanSource(row scanner) (storage.SupportSource, error) {
	var (
		x                        storage.SupportSource
		config, created, updated string
	)
	if err := row.Scan(&x.ID, &x.ProjectID, &x.Kind, &x.Name, &x.AutomationID, &x.Mode, &x.TokenHash, &config, &x.RatePerMin, &x.Enabled, &created, &updated, &x.SecretEnc); err != nil {
		return x, notFound(err)
	}
	_ = json.Unmarshal([]byte(config), &x.Config)
	if x.Config == nil {
		x.Config = map[string]any{}
	}
	return x, parseTimes([]*time.Time{&x.CreatedAt, &x.UpdatedAt}, created, updated)
}

func (r supportRepo) CreateSource(ctx context.Context, x storage.SupportSource) (storage.SupportSource, error) {
	now := time.Now().UTC()
	x.ID, x.CreatedAt, x.UpdatedAt = ids.New("sps"), now, now
	if x.Config == nil {
		x.Config = map[string]any{}
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO support_sources (`+sourceCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		x.ID, x.ProjectID, x.Kind, x.Name, x.AutomationID, x.Mode, x.TokenHash, toJSON(x.Config), x.RatePerMin, x.Enabled, fmtTime(now), fmtTime(now), x.SecretEnc)
	return x, err
}

func (r supportRepo) UpdateSource(ctx context.Context, x storage.SupportSource) error {
	if x.Config == nil {
		x.Config = map[string]any{}
	}
	res, err := r.db.ExecContext(ctx, `UPDATE support_sources SET name=?, automation_id=?, mode=?, token_hash=?, config=?, rate_per_min=?, enabled=?, secret_enc=?, updated_at=? WHERE id=?`,
		x.Name, x.AutomationID, x.Mode, x.TokenHash, toJSON(x.Config), x.RatePerMin, x.Enabled, x.SecretEnc, fmtTime(time.Now()), x.ID)
	return affected(res, err)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (r supportRepo) Source(ctx context.Context, id string) (storage.SupportSource, error) {
	return scanSource(r.db.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM support_sources WHERE id=?`, id))
}

func (r supportRepo) Sources(ctx context.Context, projectID string) ([]storage.SupportSource, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+sourceCols+` FROM support_sources WHERE project_id=? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.SupportSource{}
	for rows.Next() {
		x, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

const ticketCols = `id, project_id, source_id, external_thread, customer_id, subject, status, case_type, assignee, priority, tags,
	first_response_at, resolved_at, created_at, updated_at, sla_due_at, sla_breached`

func scanTicket(row scanner) (storage.SupportTicket, error) {
	var (
		t                      storage.SupportTicket
		tags, created, updated string
		first, resolved, due   sql.NullString
	)
	if err := row.Scan(&t.ID, &t.ProjectID, &t.SourceID, &t.ExternalThread, &t.CustomerID, &t.Subject, &t.Status, &t.CaseType, &t.Assignee, &t.Priority, &tags,
		&first, &resolved, &created, &updated, &due, &t.SLABreached); err != nil {
		return t, notFound(err)
	}
	_ = json.Unmarshal([]byte(tags), &t.Tags)
	if t.Tags == nil {
		t.Tags = []string{}
	}
	var err error
	if t.FirstResponseAt, err = optParse(first); err != nil {
		return t, err
	}
	if t.ResolvedAt, err = optParse(resolved); err != nil {
		return t, err
	}
	if t.SLADueAt, err = optParse(due); err != nil {
		return t, err
	}
	return t, parseTimes([]*time.Time{&t.CreatedAt, &t.UpdatedAt}, created, updated)
}

func (r supportRepo) CreateTicket(ctx context.Context, t storage.SupportTicket) (storage.SupportTicket, error) {
	now := time.Now().UTC()
	t.ID, t.CreatedAt, t.UpdatedAt = ids.New("tkt"), now, now
	if t.Status == "" {
		t.Status = "new"
	}
	if t.Tags == nil {
		t.Tags = []string{}
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO support_tickets (`+ticketCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.ProjectID, t.SourceID, t.ExternalThread, t.CustomerID, t.Subject, t.Status, t.CaseType, t.Assignee, t.Priority, toJSON(t.Tags),
		optTime(t.FirstResponseAt), optTime(t.ResolvedAt), fmtTime(now), fmtTime(now), optTime(t.SLADueAt), t.SLABreached)
	return t, err
}

// UpdateTicket applies t, refusing with storage.ErrStale if the row changed
// since t was read (CAS on updated_at) so two concurrent read-modify-write
// paths (an agent's support_set, an inbound customer message, a reply
// delivery outcome…) can't silently clobber each other's fields, and
// storage.ErrNotFound if it was deleted meanwhile.
func (r supportRepo) UpdateTicket(ctx context.Context, t storage.SupportTicket) error {
	if t.Tags == nil {
		t.Tags = []string{}
	}
	prevUpdatedAt := t.UpdatedAt
	res, err := r.db.ExecContext(ctx, `UPDATE support_tickets SET customer_id=?, subject=?, status=?, case_type=?, assignee=?, priority=?, tags=?,
		first_response_at=?, resolved_at=?, sla_due_at=?, sla_breached=?, updated_at=? WHERE id=? AND updated_at=?`,
		t.CustomerID, t.Subject, t.Status, t.CaseType, t.Assignee, t.Priority, toJSON(t.Tags), optTime(t.FirstResponseAt), optTime(t.ResolvedAt),
		optTime(t.SLADueAt), t.SLABreached, fmtTime(time.Now()), t.ID, fmtTime(prevUpdatedAt))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if _, getErr := r.Ticket(ctx, t.ID); errors.Is(getErr, storage.ErrNotFound) {
			return storage.ErrNotFound
		}
		return storage.ErrStale
	}
	return nil
}

func (r supportRepo) Ticket(ctx context.Context, id string) (storage.SupportTicket, error) {
	return scanTicket(r.db.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM support_tickets WHERE id=?`, id))
}

func (r supportRepo) OpenTicket(ctx context.Context, sourceID, thread string) (storage.SupportTicket, error) {
	return scanTicket(r.db.QueryRowContext(ctx, `SELECT `+ticketCols+` FROM support_tickets
		WHERE source_id=? AND external_thread=? AND status NOT IN ('resolved', 'closed') ORDER BY created_at DESC LIMIT 1`, sourceID, thread))
}

func (r supportRepo) Tickets(ctx context.Context, projectID, status string, limit int) ([]storage.SupportTicket, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q, args := `SELECT `+ticketCols+` FROM support_tickets WHERE project_id=?`, []any{projectID}
	if status != "" {
		q, args = q+` AND status=?`, append(args, status)
	}
	rows, err := r.db.QueryContext(ctx, q+` ORDER BY updated_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.SupportTicket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

const supportEventCols = `id, ticket_id, source_id, kind, body, actor, external_id, job_id, conversation_id, reply_to, state, meta, created_at`

func scanEvent(row scanner) (storage.SupportEvent, error) {
	var (
		e             storage.SupportEvent
		meta, created string
	)
	if err := row.Scan(&e.ID, &e.TicketID, &e.SourceID, &e.Kind, &e.Body, &e.Actor, &e.ExternalID, &e.JobID, &e.ConversationID, &e.ReplyTo, &e.State, &meta, &created); err != nil {
		return e, notFound(err)
	}
	_ = json.Unmarshal([]byte(meta), &e.Meta)
	var err error
	e.CreatedAt, err = parseTime(created)
	return e, err
}

func (r supportRepo) AddEvent(ctx context.Context, e storage.SupportEvent) (storage.SupportEvent, error) {
	e.ID, e.CreatedAt = ids.New("tev"), time.Now().UTC()
	if e.Meta == nil {
		e.Meta = map[string]any{}
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO support_events (`+supportEventCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.TicketID, e.SourceID, e.Kind, e.Body, e.Actor, e.ExternalID, e.JobID, e.ConversationID, e.ReplyTo, e.State, toJSON(e.Meta), fmtTime(e.CreatedAt))
	if isUnique(err) {
		return e, storage.ErrConflict
	}
	return e, err
}

func (r supportRepo) UpdateEvent(ctx context.Context, e storage.SupportEvent) error {
	if e.Meta == nil {
		e.Meta = map[string]any{}
	}
	res, err := r.db.ExecContext(ctx, `UPDATE support_events SET body=?, state=?, job_id=?, conversation_id=?, meta=? WHERE id=?`,
		e.Body, e.State, e.JobID, e.ConversationID, toJSON(e.Meta), e.ID)
	return affected(res, err)
}

func (r supportRepo) Event(ctx context.Context, id string) (storage.SupportEvent, error) {
	return scanEvent(r.db.QueryRowContext(ctx, `SELECT `+supportEventCols+` FROM support_events WHERE id=?`, id))
}

func (r supportRepo) EventByExternal(ctx context.Context, sourceID, externalID string) (storage.SupportEvent, error) {
	return scanEvent(r.db.QueryRowContext(ctx, `SELECT `+supportEventCols+` FROM support_events WHERE source_id=? AND external_id=?`, sourceID, externalID))
}

func (r supportRepo) EventByJob(ctx context.Context, jobID string) (storage.SupportEvent, error) {
	return scanEvent(r.db.QueryRowContext(ctx, `SELECT `+supportEventCols+` FROM support_events WHERE job_id=? ORDER BY created_at LIMIT 1`, jobID))
}

func (r supportRepo) Events(ctx context.Context, ticketID string) ([]storage.SupportEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+supportEventCols+` FROM support_events WHERE ticket_id=? ORDER BY created_at, id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.SupportEvent{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r supportRepo) ticketList(ctx context.Context, q string, args ...any) ([]storage.SupportTicket, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.SupportTicket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r supportRepo) TicketsSince(ctx context.Context, projectID string, since time.Time) ([]storage.SupportTicket, error) {
	return r.ticketList(ctx, `SELECT `+ticketCols+` FROM support_tickets WHERE project_id=? AND created_at>=? ORDER BY created_at`, projectID, fmtTime(since))
}

func (r supportRepo) Overdue(ctx context.Context, now time.Time) ([]storage.SupportTicket, error) {
	return r.ticketList(ctx, `SELECT `+ticketCols+` FROM support_tickets WHERE sla_due_at IS NOT NULL AND sla_due_at<? AND sla_breached=0
		AND first_response_at IS NULL AND status NOT IN ('resolved', 'closed') LIMIT 200`, fmtTime(now))
}

const outCols = `id, project_id, kind, ref_id, ticket_id, event, payload, status, attempts, last_error, next_at, created_at, updated_at`

func scanOut(row scanner) (storage.SupportOut, error) {
	var (
		o                               storage.SupportOut
		payload, next, created, updated string
	)
	if err := row.Scan(&o.ID, &o.ProjectID, &o.Kind, &o.RefID, &o.TicketID, &o.Event, &payload, &o.Status, &o.Attempts, &o.LastError, &next, &created, &updated); err != nil {
		return o, notFound(err)
	}
	_ = json.Unmarshal([]byte(payload), &o.Payload)
	return o, parseTimes([]*time.Time{&o.NextAt, &o.CreatedAt, &o.UpdatedAt}, next, created, updated)
}

func (r supportRepo) AddOut(ctx context.Context, o storage.SupportOut) (storage.SupportOut, error) {
	now := time.Now().UTC()
	o.ID, o.CreatedAt, o.UpdatedAt = ids.New("out"), now, now
	if o.Status == "" {
		o.Status = "pending"
	}
	if o.NextAt.IsZero() {
		o.NextAt = now
	}
	if o.Payload == nil {
		o.Payload = map[string]any{}
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO support_outbox (`+outCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.ProjectID, o.Kind, o.RefID, o.TicketID, o.Event, toJSON(o.Payload), o.Status, o.Attempts, o.LastError, fmtTime(o.NextAt), fmtTime(now), fmtTime(now))
	return o, err
}

func (r supportRepo) UpdateOut(ctx context.Context, o storage.SupportOut) error {
	res, err := r.db.ExecContext(ctx, `UPDATE support_outbox SET status=?, attempts=?, last_error=?, next_at=?, updated_at=? WHERE id=?`,
		o.Status, o.Attempts, o.LastError, fmtTime(o.NextAt), fmtTime(time.Now()), o.ID)
	return affected(res, err)
}

func (r supportRepo) outList(ctx context.Context, q string, args ...any) ([]storage.SupportOut, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.SupportOut{}
	for rows.Next() {
		o, err := scanOut(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r supportRepo) DueOut(ctx context.Context, now time.Time, limit int) ([]storage.SupportOut, error) {
	return r.outList(ctx, `SELECT `+outCols+` FROM support_outbox WHERE status='pending' AND next_at<=? ORDER BY next_at LIMIT ?`, fmtTime(now), limit)
}

func (r supportRepo) Outs(ctx context.Context, projectID string, limit int) ([]storage.SupportOut, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return r.outList(ctx, `SELECT `+outCols+` FROM support_outbox WHERE project_id=? ORDER BY created_at DESC LIMIT ?`, projectID, limit)
}

const hookCols = `id, project_id, name, kind, url, automation_id, events, secret_enc, enabled, created_at, updated_at`

func scanHook(row scanner) (storage.SupportHook, error) {
	var (
		h                        storage.SupportHook
		events, created, updated string
	)
	if err := row.Scan(&h.ID, &h.ProjectID, &h.Name, &h.Kind, &h.URL, &h.AutomationID, &events, &h.SecretEnc, &h.Enabled, &created, &updated); err != nil {
		return h, notFound(err)
	}
	_ = json.Unmarshal([]byte(events), &h.Events)
	if h.Events == nil {
		h.Events = []string{}
	}
	return h, parseTimes([]*time.Time{&h.CreatedAt, &h.UpdatedAt}, created, updated)
}

func (r supportRepo) CreateHook(ctx context.Context, h storage.SupportHook) (storage.SupportHook, error) {
	now := time.Now().UTC()
	h.ID, h.CreatedAt, h.UpdatedAt = ids.New("shk"), now, now
	if h.Events == nil {
		h.Events = []string{}
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO support_hooks (`+hookCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.ProjectID, h.Name, h.Kind, h.URL, h.AutomationID, toJSON(h.Events), h.SecretEnc, h.Enabled, fmtTime(now), fmtTime(now))
	return h, err
}

func (r supportRepo) UpdateHook(ctx context.Context, h storage.SupportHook) error {
	if h.Events == nil {
		h.Events = []string{}
	}
	res, err := r.db.ExecContext(ctx, `UPDATE support_hooks SET name=?, kind=?, url=?, automation_id=?, events=?, secret_enc=?, enabled=?, updated_at=? WHERE id=?`,
		h.Name, h.Kind, h.URL, h.AutomationID, toJSON(h.Events), h.SecretEnc, h.Enabled, fmtTime(time.Now()), h.ID)
	return affected(res, err)
}

func (r supportRepo) DeleteHook(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM support_hooks WHERE id=?`, id)
	return affected(res, err)
}

func (r supportRepo) Hook(ctx context.Context, id string) (storage.SupportHook, error) {
	return scanHook(r.db.QueryRowContext(ctx, `SELECT `+hookCols+` FROM support_hooks WHERE id=?`, id))
}

func (r supportRepo) Hooks(ctx context.Context, projectID string) ([]storage.SupportHook, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+hookCols+` FROM support_hooks WHERE project_id=? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.SupportHook{}
	for rows.Next() {
		h, err := scanHook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
