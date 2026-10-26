package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/ids"
	"github.com/tung1998/agent-office/internal/storage"
)

type chatRepo struct {
	db      dbtx
	changed func(storage.Change) // nil: nobody follows
}

func (r chatRepo) tell(c storage.Change) {
	if r.changed != nil {
		r.changed(c)
	}
}

const convCols = `id, project_id, agent_id, agent_name, title, session_id, runtime, mode, task_id, created_by, created_at, updated_at, edit_mode, purpose, automation_id, context_tokens, context_window, effort, subject`

// convRead: what is read back (cleaned is set by the data cleanup only)
const convRead = convCols + `, cleaned, cleaned_at`

func scanConv(row scanner) (storage.Conversation, error) {
	var (
		c                storage.Conversation
		agent, task      sql.NullString
		created, updated string
		cleanedAt        sql.NullString
	)
	if err := row.Scan(&c.ID, &c.ProjectID, &agent, &c.AgentName, &c.Title, &c.SessionID, &c.Runtime, &c.Mode, &task, &c.CreatedBy, &created, &updated, &c.EditMode, &c.Purpose, &c.AutomationID, &c.ContextTokens, &c.ContextWindow, &c.Effort, &c.Subject, &c.Cleaned, &cleanedAt); err != nil {
		return c, notFound(err)
	}
	c.AgentID, c.TaskID = agent.String, task.String
	if cleanedAt.Valid {
		if t, err := parseTime(cleanedAt.String); err == nil {
			c.CleanedAt = &t
		}
	}
	return c, parseTimes([]*time.Time{&c.CreatedAt, &c.UpdatedAt}, created, updated)
}

func (r chatRepo) CreateConversation(ctx context.Context, c storage.Conversation) (storage.Conversation, error) {
	now := time.Now().UTC()
	if c.ID == "" {
		c.ID = ids.New("cnv")
	}
	c.CreatedAt, c.UpdatedAt = now, now
	if c.Mode == "" {
		c.Mode = "propose"
	}
	if c.EditMode == "" {
		c.EditMode = "worktree"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO conversations (`+convCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.ProjectID, nullStr(c.AgentID), c.AgentName, c.Title, c.SessionID, c.Runtime, c.Mode, nullStr(c.TaskID), c.CreatedBy, fmtTime(now), fmtTime(now), c.EditMode, c.Purpose, c.AutomationID, c.ContextTokens, c.ContextWindow, c.Effort, c.Subject)
	if err == nil {
		r.tell(storage.Change{Kind: "conversation", ConversationID: c.ID})
	}
	return c, err
}

func (r chatRepo) UpdateConversation(ctx context.Context, c storage.Conversation) error {
	if c.Mode == "" {
		c.Mode = "propose"
	}
	if c.EditMode == "" {
		c.EditMode = "worktree"
	}
	err := execOne(ctx, r.db, `UPDATE conversations SET agent_id=?, agent_name=?, title=?, session_id=?, runtime=?, mode=?, edit_mode=?, context_tokens=?, context_window=?, effort=?, updated_at=? WHERE id=?`,
		nullStr(c.AgentID), c.AgentName, c.Title, c.SessionID, c.Runtime, c.Mode, c.EditMode, c.ContextTokens, c.ContextWindow, c.Effort, fmtTime(time.Now()), c.ID)
	if err == nil {
		r.tell(storage.Change{Kind: "conversation", ConversationID: c.ID})
	}
	return err
}

func (r chatRepo) GetConversation(ctx context.Context, id string) (storage.Conversation, error) {
	c, err := scanConv(r.db.QueryRowContext(ctx, `SELECT `+convRead+` FROM conversations WHERE id=?`, id))
	if err != nil {
		return c, err
	}
	list := []storage.Conversation{c}
	err = r.fillTags(ctx, list)
	return list[0], err
}

// fillTags loads the tags of a page of chats in one query.
func (r chatRepo) fillTags(ctx context.Context, list []storage.Conversation) error {
	if len(list) == 0 {
		return nil
	}
	at := map[string]int{}
	args := make([]any, len(list))
	for i, c := range list {
		at[c.ID], args[i] = i, c.ID
		list[i].Tags = []string{}
	}
	rows, err := r.db.QueryContext(ctx, `SELECT conversation_id, tag FROM conversation_tags WHERE conversation_id IN (`+placeholders(len(list))+`) ORDER BY created_at, tag`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, tag string
		if err := rows.Scan(&id, &tag); err != nil {
			return err
		}
		list[at[id]].Tags = append(list[at[id]].Tags, tag)
	}
	return rows.Err()
}

func (r chatRepo) SetConversationTags(ctx context.Context, conversationID string, tags []string) error {
	if _, err := r.GetConversation(ctx, conversationID); err != nil {
		return err
	}
	// kept tags keep their date (the "last used" of suggestions); the rest go
	args := []any{conversationID}
	keep := ""
	if len(tags) > 0 {
		keep = ` AND tag NOT IN (` + placeholders(len(tags)) + `)`
		for _, t := range tags {
			args = append(args, t)
		}
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM conversation_tags WHERE conversation_id=?`+keep, args...); err != nil {
		return err
	}
	now := fmtTime(time.Now().UTC())
	for _, t := range tags {
		if _, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO conversation_tags (conversation_id, tag, created_at) VALUES (?,?,?)`, conversationID, t, now); err != nil {
			return err
		}
	}
	r.tell(storage.Change{Kind: "conversation", ConversationID: conversationID})
	return nil
}

func (r chatRepo) ProjectTags(ctx context.Context, projectID, createdBy string) ([]storage.TagCount, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT t.tag, COUNT(*), MAX(t.created_at) FROM conversation_tags t
		JOIN conversations c ON c.id = t.conversation_id WHERE c.project_id=? AND (?='' OR c.created_by=?)
		GROUP BY t.tag ORDER BY MAX(t.created_at) DESC, t.tag`, projectID, createdBy, createdBy)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.TagCount{}
	for rows.Next() {
		var (
			tc   storage.TagCount
			last string
		)
		if err := rows.Scan(&tc.Tag, &tc.Count, &last); err != nil {
			return nil, err
		}
		if tc.LastUse, err = parseTime(last); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func (r chatRepo) TaskConversation(ctx context.Context, taskID string) (storage.Conversation, error) {
	return scanConv(r.db.QueryRowContext(ctx, `SELECT `+convRead+` FROM conversations WHERE task_id=?`, taskID))
}

func (r chatRepo) ListConversations(ctx context.Context, projectID string, limit int) ([]storage.Conversation, error) {
	return r.ListConversationsFrom(ctx, projectID, "", limit)
}

func (r chatRepo) ListConversationsFrom(ctx context.Context, projectID, source string, limit int) ([]storage.Conversation, error) {
	return r.ListConversationsBefore(ctx, projectID, source, time.Time{}, limit)
}

func (r chatRepo) ListConversationsBefore(ctx context.Context, projectID, source string, before time.Time, limit int) ([]storage.Conversation, error) {
	return r.ListConversationsTagged(ctx, projectID, source, nil, before, limit, "")
}

func (r chatRepo) ListConversationsTagged(ctx context.Context, projectID, source string, tags []string, before time.Time, limit int, createdBy string) ([]storage.Conversation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// the project's own chats (web, automations); an editor's (automation,
	// skill, workflow) stays with what it writes
	where := `purpose=''`
	switch source {
	case "all":
		where = `purpose IN ('','channel','burn')`
	case "burn":
		where = `purpose='burn'`
	case "web":
		where = `purpose='' AND created_by LIKE 'human:%'`
	case "auto":
		where = `purpose='' AND created_by LIKE 'auto:%'`
	case "discord", "telegram":
		where = `purpose='channel' AND created_by LIKE '` + source + `:%'`
	}
	page := ""
	args := []any{projectID}
	if !before.IsZero() { // the next page: older than the last one shown
		page = ` AND updated_at < ?`
		args = append(args, fmtTime(before))
	}
	if len(tags) > 0 { // every tag (the column compares without case)
		page += ` AND id IN (SELECT conversation_id FROM conversation_tags WHERE tag IN (` + placeholders(len(tags)) + `) GROUP BY conversation_id HAVING COUNT(DISTINCT tag) = ?)`
		for _, t := range tags {
			args = append(args, t)
		}
		args = append(args, len(tags))
	}
	if createdBy != "" {
		page += ` AND created_by = ?`
		args = append(args, createdBy)
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `SELECT `+convRead+` FROM conversations WHERE project_id=? AND task_id IS NULL AND `+where+page+` ORDER BY updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	var out []storage.Conversation
	for rows.Next() {
		c, err := scanConv(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, r.fillTags(ctx, out)
}

func (r chatRepo) ListAgentConversations(ctx context.Context, projectID, agentID string, since time.Time, limit int) ([]storage.Conversation, error) {
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	where := `purpose IN ('','channel','burn') AND (agent_id=? OR id IN (SELECT conversation_id FROM conversation_agents WHERE agent_id=?))`
	args := []any{projectID, agentID, agentID}
	if !since.IsZero() {
		where += ` AND updated_at >= ?`
		args = append(args, fmtTime(since))
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `SELECT `+convRead+` FROM conversations WHERE project_id=? AND task_id IS NULL AND `+where+` ORDER BY updated_at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	var out []storage.Conversation
	for rows.Next() {
		c, err := scanConv(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, r.fillTags(ctx, out)
}

func (r chatRepo) DeleteConversation(ctx context.Context, id string) error {
	conv, _ := r.GetConversation(ctx, id) // best effort: scope the change event even once the row is gone
	err := execOne(ctx, r.db, `DELETE FROM conversations WHERE id=?`, id)
	if err == nil {
		r.tell(storage.Change{Kind: "conversation.deleted", ConversationID: id, ProjectID: conv.ProjectID, CreatedBy: conv.CreatedBy})
	}
	return err
}

func (r chatRepo) AddMessage(ctx context.Context, m storage.Message) (storage.Message, error) {
	if m.ID == "" {
		m.ID = ids.New("msg")
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	if m.Tools == nil {
		m.Tools = []storage.ToolCall{}
	}
	if m.Attachments == nil {
		m.Attachments = []storage.Attachment{}
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO messages (id, conversation_id, role, content, tools, attachments, run_id, author, created_at, context, agent_id, sender) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, m.Role, m.Content, toJSON(m.Tools), toJSON(m.Attachments), nullStr(m.RunID), m.Author, fmtTime(m.CreatedAt), m.Context, m.AgentID, senderJSON(m.Sender))
	if err == nil {
		_, _ = r.db.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, fmtTime(m.CreatedAt), m.ConversationID)
		r.tell(storage.Change{Kind: "message", ConversationID: m.ConversationID, Message: &m})
	}
	return m, err
}

// senderJSON is a message's sender as stored (” = none).
func senderJSON(s *storage.Sender) string {
	if s == nil {
		return ""
	}
	return toJSON(s)
}

func (r chatRepo) ListMessages(ctx context.Context, conversationID string) ([]storage.Message, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT m.id, m.conversation_id, m.role, m.content, m.tools, m.attachments, m.run_id, m.author, m.created_at, m.context, m.agent_id, m.sender,
		COALESCE(r.provider_name, ''), COALESCE(r.model, '')
		FROM messages m LEFT JOIN runs r ON r.id = m.run_id WHERE m.conversation_id=? ORDER BY m.created_at, m.id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Message
	for rows.Next() {
		var (
			m                    storage.Message
			tools, atts, created string
			sender               string
			run                  sql.NullString
		)
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &tools, &atts, &run, &m.Author, &created, &m.Context, &m.AgentID, &sender, &m.Provider, &m.Model); err != nil {
			return nil, err
		}
		m.RunID = run.String
		if sender != "" {
			m.Sender = &storage.Sender{}
			if err := json.Unmarshal([]byte(sender), m.Sender); err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal([]byte(tools), &m.Tools); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(atts), &m.Attachments); err != nil {
			return nil, err
		}
		if m.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const patchCols = `id, conversation_id, message_id, task_id, step_id, diff, files, status, detail, decided_by, decided_at, created_at, origin, tree`

func scanPatch(row scanner) (storage.Patch, error) {
	var (
		p                          storage.Patch
		files, created             string
		decided, conv, msg, tk, st sql.NullString
	)
	if err := row.Scan(&p.ID, &conv, &msg, &tk, &st, &p.Diff, &files, &p.Status, &p.Detail, &p.DecidedBy, &decided, &created, &p.Origin, &p.Tree); err != nil {
		return p, notFound(err)
	}
	p.ConversationID, p.MessageID, p.TaskID, p.StepID = conv.String, msg.String, tk.String, st.String
	if err := json.Unmarshal([]byte(files), &p.Files); err != nil {
		return p, err
	}
	if decided.Valid {
		t, err := parseTime(decided.String)
		if err != nil {
			return p, err
		}
		p.DecidedAt = &t
	}
	var err error
	p.CreatedAt, err = parseTime(created)
	return p, err
}

func (r chatRepo) AddPatch(ctx context.Context, p storage.Patch) (storage.Patch, error) {
	if p.ID == "" {
		p.ID = ids.New("pat")
	}
	if p.Status == "" {
		p.Status = "pending"
	}
	if p.Files == nil {
		p.Files = []string{}
	}
	p.CreatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `INSERT INTO patches (`+patchCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, nullStr(p.ConversationID), nullStr(p.MessageID), nullStr(p.TaskID), nullStr(p.StepID), p.Diff, toJSON(p.Files), p.Status,
		p.Detail, p.DecidedBy, nil, fmtTime(p.CreatedAt), p.Origin, p.Tree)
	return p, err
}

func (r chatRepo) GetPatch(ctx context.Context, id string) (storage.Patch, error) {
	return scanPatch(r.db.QueryRowContext(ctx, `SELECT `+patchCols+` FROM patches WHERE id=?`, id))
}

func (r chatRepo) ListPatches(ctx context.Context, conversationID string) ([]storage.Patch, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+patchCols+` FROM patches WHERE conversation_id=? ORDER BY created_at, id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Patch
	for rows.Next() {
		p, err := scanPatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r chatRepo) SetPatchDiff(ctx context.Context, id, diff string, files []string) error {
	if files == nil {
		files = []string{}
	}
	return execOne(ctx, r.db, `UPDATE patches SET diff=?, files=? WHERE id=?`, diff, toJSON(files), id)
}

func (r chatRepo) QueuePatch(ctx context.Context, id, by string) error {
	return execOne(ctx, r.db, `UPDATE patches SET status='queued', decided_by=? WHERE id=? AND status='pending'`, by, id)
}

func (r chatRepo) PatchRefs(ctx context.Context, conversationID string) ([]storage.Patch, error) {
	cols := strings.Replace(patchCols, "diff,", "'',", 1)
	rows, err := r.db.QueryContext(ctx, `SELECT `+cols+` FROM patches WHERE conversation_id=? ORDER BY created_at, id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Patch
	for rows.Next() {
		p, err := scanPatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r chatRepo) PendingPatches(ctx context.Context, limit int) ([]storage.Patch, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+patchCols+` FROM patches WHERE status='pending' AND conversation_id<>'' ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Patch
	for rows.Next() {
		p, err := scanPatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r chatRepo) DecidePatch(ctx context.Context, id, status, detail, by string, at time.Time) error {
	return execOne(ctx, r.db, `UPDATE patches SET status=?, detail=?, decided_by=?, decided_at=? WHERE id=?`, status, detail, by, fmtTime(at), id)
}

func (r chatRepo) AutomationConversation(ctx context.Context, automationID string) (storage.Conversation, error) {
	return scanConv(r.db.QueryRowContext(ctx, `SELECT `+convRead+` FROM conversations WHERE automation_id=? ORDER BY updated_at DESC LIMIT 1`, automationID))
}

func (r chatRepo) SubjectConversation(ctx context.Context, projectID, purpose, subject, createdBy string) (storage.Conversation, error) {
	return scanConv(r.db.QueryRowContext(ctx, `SELECT `+convRead+` FROM conversations WHERE project_id=? AND purpose=? AND subject=? AND subject!='' AND (?='' OR created_by=?) ORDER BY updated_at DESC LIMIT 1`,
		projectID, purpose, subject, createdBy, createdBy))
}

func (r chatRepo) EmptyConversation(ctx context.Context, projectID, createdBy string) (storage.Conversation, error) {
	return scanConv(r.db.QueryRowContext(ctx, `SELECT `+convRead+` FROM conversations c WHERE project_id=? AND created_by=? AND purpose='' AND task_id IS NULL AND automation_id='' AND title='' AND cleaned=''
		AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id=c.id) AND NOT EXISTS (SELECT 1 FROM chat_queued q WHERE q.conversation_id=c.id) AND NOT EXISTS (SELECT 1 FROM conversation_tags t WHERE t.conversation_id=c.id)
		ORDER BY updated_at DESC LIMIT 1`, projectID, createdBy))
}

func (r chatRepo) SetConversationSubject(ctx context.Context, conversationID, subject string) error {
	err := execOne(ctx, r.db, `UPDATE conversations SET subject=? WHERE id=?`, subject, conversationID)
	if err == nil {
		r.tell(storage.Change{Kind: "conversation", ConversationID: conversationID})
	}
	return err
}

func (r chatRepo) LinkAutomation(ctx context.Context, conversationID, automationID string) error {
	err := execOne(ctx, r.db, `UPDATE conversations SET automation_id=?, purpose='automation' WHERE id=?`, automationID, conversationID)
	if isUnique(err) {
		return storage.ErrConflict // the automation has its chat already
	}
	if err == nil {
		r.tell(storage.Change{Kind: "conversation", ConversationID: conversationID})
	}
	return err
}

func (r chatRepo) UpsertMember(ctx context.Context, m storage.ChatMember) error {
	if m.JoinedAt.IsZero() {
		m.JoinedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO conversation_agents (conversation_id, agent_id, agent_name, session_id, runtime, last_message_id, context_tokens, context_window, joined_at)
		VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (conversation_id, agent_id) DO UPDATE SET agent_name=excluded.agent_name, session_id=excluded.session_id, runtime=excluded.runtime,
			last_message_id=excluded.last_message_id, context_tokens=excluded.context_tokens, context_window=excluded.context_window`,
		m.ConversationID, m.AgentID, m.AgentName, m.SessionID, m.Runtime, m.LastMessageID, m.ContextTokens, m.ContextWindow, fmtTime(m.JoinedAt))
	return err
}

func (r chatRepo) Members(ctx context.Context, conversationID string) ([]storage.ChatMember, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT conversation_id, agent_id, agent_name, session_id, runtime, last_message_id, context_tokens, context_window, joined_at
		FROM conversation_agents WHERE conversation_id=? ORDER BY joined_at, agent_id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.ChatMember{}
	for rows.Next() {
		var (
			m      storage.ChatMember
			joined string
		)
		if err := rows.Scan(&m.ConversationID, &m.AgentID, &m.AgentName, &m.SessionID, &m.Runtime, &m.LastMessageID, &m.ContextTokens, &m.ContextWindow, &joined); err != nil {
			return nil, err
		}
		if m.JoinedAt, err = parseTime(joined); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r chatRepo) SetConversationContext(ctx context.Context, id string, tokens, window int) error {
	return execOne(ctx, r.db, `UPDATE conversations SET context_tokens=?, context_window=? WHERE id=?`, tokens, window, id)
}

func (r chatRepo) MarkSeen(ctx context.Context, userID, conversationID string, seen bool) error {
	if !seen {
		_, err := r.db.ExecContext(ctx, `DELETE FROM conversation_reads WHERE user_id=? AND conversation_id=?`, userID, conversationID)
		return err
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO conversation_reads (user_id, conversation_id, seen_at) VALUES (?,?,?)
		ON CONFLICT (user_id, conversation_id) DO UPDATE SET seen_at=excluded.seen_at`, userID, conversationID, fmtTime(time.Now()))
	return err
}

func (r chatRepo) Unread(ctx context.Context, userID, author string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT c.id FROM conversations c
		JOIN (SELECT conversation_id, max(created_at) AS at FROM messages WHERE role='assistant' GROUP BY conversation_id) a ON a.conversation_id=c.id
		LEFT JOIN conversation_reads s ON s.conversation_id=c.id AND s.user_id=?
		WHERE a.at > coalesce(s.seen_at, '')
		  AND EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id=c.id AND m.role='user' AND m.author=?)
		ORDER BY a.at DESC LIMIT 100`, userID, author)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r chatRepo) MarkAllSeen(ctx context.Context, userID, author string) (int, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO conversation_reads (user_id, conversation_id, seen_at)
		SELECT ?, c.id, ? FROM conversations c
		JOIN (SELECT conversation_id, max(created_at) AS at FROM messages WHERE role='assistant' GROUP BY conversation_id) a ON a.conversation_id=c.id
		LEFT JOIN conversation_reads s ON s.conversation_id=c.id AND s.user_id=?
		WHERE a.at > coalesce(s.seen_at, '')
		  AND EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id=c.id AND m.role='user' AND m.author=?)
		ON CONFLICT (user_id, conversation_id) DO UPDATE SET seen_at=excluded.seen_at`, userID, fmtTime(time.Now()), userID, author)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ftsQuery makes the person's words an FTS5 query: every word must be there,
// each one quoted (nothing in it is FTS syntax), the last a prefix.
func ftsQuery(q string) string {
	var words []string
	for _, w := range strings.Fields(q) {
		w = strings.Trim(w, `"'.,;:!?()[]{}`)
		if w != "" {
			words = append(words, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
		}
	}
	if len(words) == 0 {
		return ""
	}
	words[len(words)-1] += "*"
	return strings.Join(words, " ")
}

func (r chatRepo) SearchMessages(ctx context.Context, query string, f storage.SearchFilter) ([]storage.MessageHit, error) {
	q := ftsQuery(query)
	if q == "" || len(f.ProjectIDs) == 0 {
		return []storage.MessageHit{}, nil
	}
	if f.Limit <= 0 || f.Limit > 50 {
		f.Limit = 10
	}
	sql := `SELECT m.id, m.conversation_id, c.project_id, c.title, m.author, m.role, m.created_at,
		snippet(messages_fts, 0, '«', '»', '…', 24)
		FROM messages_fts JOIN messages m ON m.rowid = messages_fts.rowid JOIN conversations c ON c.id = m.conversation_id
		WHERE messages_fts MATCH ? AND c.project_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(f.ProjectIDs)), ",") + `)`
	args := []any{q}
	for _, p := range f.ProjectIDs {
		args = append(args, p)
	}
	if !f.Since.IsZero() {
		sql += ` AND m.created_at >= ?`
		args = append(args, fmtTime(f.Since))
	}
	if f.Author != "" {
		sql += ` AND m.author LIKE ?`
		args = append(args, "%"+f.Author+"%")
	}
	if f.HideOthersIn != "" {
		sql += ` AND (c.project_id != ? OR c.created_by = ?)`
		args = append(args, f.HideOthersIn, f.Me)
	}
	sql += ` ORDER BY rank LIMIT ?`
	args = append(args, f.Limit)
	rows, err := r.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.MessageHit{}
	for rows.Next() {
		var h storage.MessageHit
		var at string
		if err := rows.Scan(&h.MessageID, &h.ConversationID, &h.ProjectID, &h.Title, &h.Author, &h.Role, &at, &h.Snippet); err != nil {
			return nil, err
		}
		h.CreatedAt, _ = parseTime(at)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r chatRepo) QueueMessage(ctx context.Context, q storage.QueuedMessage) (storage.QueuedMessage, error) {
	if q.ID == "" {
		q.ID = ids.New("chq")
	}
	if q.CreatedAt.IsZero() {
		q.CreatedAt = time.Now().UTC()
	}
	if q.Attachments == nil {
		q.Attachments = []storage.Attachment{}
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO chat_queued (id, conversation_id, author, text, attachments, context, options, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		q.ID, q.ConversationID, q.Author, q.Text, toJSON(q.Attachments), q.Context, toJSON(q.Options), fmtTime(q.CreatedAt))
	if err == nil {
		r.tell(storage.Change{Kind: "conversation", ConversationID: q.ConversationID})
	}
	return q, err
}

func (r chatRepo) QueuedMessages(ctx context.Context, conversationID string) ([]storage.QueuedMessage, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, conversation_id, author, text, attachments, context, options, created_at FROM chat_queued WHERE conversation_id=? ORDER BY created_at, id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.QueuedMessage
	for rows.Next() {
		var (
			q             storage.QueuedMessage
			att, opts, at string
		)
		if err := rows.Scan(&q.ID, &q.ConversationID, &q.Author, &q.Text, &att, &q.Context, &opts, &at); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(att), &q.Attachments)
		_ = json.Unmarshal([]byte(opts), &q.Options)
		if q.Attachments == nil {
			q.Attachments = []storage.Attachment{}
		}
		q.CreatedAt, _ = parseTime(at)
		out = append(out, q)
	}
	return out, rows.Err()
}

func (r chatRepo) DeleteQueued(ctx context.Context, conversationID string, ids ...string) error {
	q, args := `DELETE FROM chat_queued WHERE conversation_id=?`, []any{conversationID}
	if len(ids) > 0 {
		q += ` AND id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		r.tell(storage.Change{Kind: "conversation", ConversationID: conversationID})
	}
	return nil
}

func (r chatRepo) QueuedConversations(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT conversation_id FROM chat_queued`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
