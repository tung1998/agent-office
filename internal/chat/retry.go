package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
)

// A person's turn that died on a passing failure of the AI service (a 529, a
// 5xx, a dropped stream: error code api_transient) goes on by itself in its
// session a while later, like an answer cut by a restart (ADR-260): 2, then
// 10, then 30 minutes later, as many times in a row as the settings say. The
// plan is kept on the job (retry_at), so it outlives office going down; the
// person writing in the chat, or taking it back, calls it off.

// TransientRetryWaits are the waits before each try in a row (the last one
// repeats past its end); tests make them short.
var TransientRetryWaits = []time.Duration{2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

// A person's turn that stopped because every AI connection of its agent is
// out of quota (error code out_of_tokens) waits for the first of them to
// reset, then goes on the same way (ADR-269): planned on the job too, called
// off the same way, at most QuotaRetries times in a row. The office's own
// daily budget (code budget) is a cap the person set: never waited out.

// OutOfTokensCode is the error code of a job that stopped for quota.
const OutOfTokensCode = "out_of_tokens"

// QuotaRetries is how many times in a row a turn out of quota waits for the
// reset and goes on.
const QuotaRetries = 2

// QuotaRetrySlack is how long after the reset a turn out of quota goes on
// (the AI service's clock is not office's); tests make it short.
var QuotaRetrySlack = 2 * time.Minute

// Retry is a planned retry of a chat's last turn, for the dashboard.
type Retry struct {
	JobID string    `json:"job_id"`
	At    time.Time `json:"at"`
	N     int       `json:"n"`
	Max   int       `json:"max"`
	Quota bool      `json:"quota,omitempty"` // it waits for the quota to reset (ADR-269)
}

// retryOf is j's planned retry, for the dashboard.
func (e *Engine) retryOf(ctx context.Context, j storage.Job) *Retry {
	if j.ErrorCode == OutOfTokensCode {
		return &Retry{JobID: j.ID, At: *j.RetryAt, N: j.RetryN, Max: max(QuotaRetries, j.RetryN), Quota: true}
	}
	return &Retry{JobID: j.ID, At: *j.RetryAt, N: j.RetryN, Max: max(e.maxRetries(ctx), j.RetryN)}
}

// retryTimers: job id → the timer of its planned retry.
type retryTimers struct {
	mu sync.Mutex
	m  map[string]*time.Timer
}

func (r *retryTimers) set(id string, t *time.Timer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]*time.Timer{}
	}
	if old := r.m[id]; old != nil {
		old.Stop()
	}
	r.m[id] = t
}

func (r *retryTimers) drop(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.m[id]; t != nil {
		t.Stop()
		delete(r.m, id)
	}
}

func (r *retryTimers) stopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, t := range r.m {
		t.Stop()
		delete(r.m, id)
	}
}

// retryable: a person's own turn in one of the project's chats (a bot's, a
// workflow's or a task's run goes on by its own rules).
func retryable(j storage.Job, conv storage.Conversation) bool {
	return j.Kind == "chat_turn" && j.Origin == "user" && j.AgentID != "" && j.ConversationID != "" &&
		conv.Purpose == "" && conv.TaskID == "" && conv.Cleaned == ""
}

// maxRetries is how many times in a row a cut turn goes on (0: off).
func (e *Engine) maxRetries(ctx context.Context) int {
	if e.usage == nil {
		return 0
	}
	st, _ := e.usage.Settings(ctx)
	return st.Retries()
}

// transientInARow is how many of agentID's latest turns in a chat in a row
// died on a passing API failure (j itself included once it ended so).
func (e *Engine) transientInARow(ctx context.Context, conversationID, agentID string, max int) int {
	return e.inARow(ctx, conversationID, agentID, TransientCode, max)
}

// inARow is how many of agentID's latest turns in a chat in a row ended
// with error code.
func (e *Engine) inARow(ctx context.Context, conversationID, agentID, code string, max int) int {
	jobs, err := e.store.Jobs().List(ctx, storage.JobFilter{ConversationID: conversationID, Kind: "chat_turn", AgentID: agentID, Limit: max + 2})
	if err != nil {
		return 0
	}
	n := 0
	for _, j := range jobs {
		if j.ErrorCode != code {
			break
		}
		n++
	}
	return n
}

// planRetry plans j, a turn that just died on a passing API failure, to go
// on later, and says so in the chat; past the tries in a row, it says the
// chat waits for the person.
func (e *Engine) planRetry(j storage.Job, conv storage.Conversation) {
	ctx := context.Background()
	if (j.ErrorCode != TransientCode && j.ErrorCode != OutOfTokensCode) || !retryable(j, conv) || e.stopping.Load() {
		return
	}
	max := e.maxRetries(ctx)
	if max == 0 {
		return
	}
	if j.ErrorCode == OutOfTokensCode {
		e.planQuotaRetry(ctx, j, conv)
		return
	}
	n := e.transientInARow(ctx, conv.ID, j.AgentID, max)
	if n > max {
		e.note(conv, prompts.Render("chat/api-retry-over", map[string]any{"Max": max}))
		return
	}
	wait := TransientRetryWaits[min(n, len(TransientRetryWaits))-1]
	at := time.Now().UTC().Add(wait)
	if err := e.store.Jobs().SetRetry(ctx, j.ID, &at, n); err != nil {
		slog.Warn("chat: plan retry", "job", j.ID, "err", err)
		return
	}
	e.note(conv, prompts.Render("chat/api-retry-planned", map[string]any{"At": at.Local().Format("15:04"), "N": n, "Max": max}))
	e.armRetry(j.ID, wait)
	e.running(conv.ID) // the dashboard shows the plan
}

// planQuotaRetry plans j, a turn that just stopped because its agent's
// connections are out of quota, to go on once the first of them resets
// (ADR-269); past QuotaRetries in a row, the chat waits for the person.
func (e *Engine) planQuotaRetry(ctx context.Context, j storage.Job, conv storage.Conversation) {
	n := e.inARow(ctx, conv.ID, j.AgentID, OutOfTokensCode, QuotaRetries)
	if n > QuotaRetries {
		e.note(conv, prompts.Render("chat/quota-retry-over", map[string]any{"Max": QuotaRetries}))
		return
	}
	at := e.QuotaFree(ctx, j.AgentID).Add(QuotaRetrySlack).UTC()
	if err := e.store.Jobs().SetRetry(ctx, j.ID, &at, n); err != nil {
		slog.Warn("chat: plan quota retry", "job", j.ID, "err", err)
		return
	}
	e.note(conv, prompts.Render("chat/quota-retry-planned", map[string]any{"At": at.Local().Format("02/01 15:04"), "N": n, "Max": QuotaRetries}))
	e.armRetry(j.ID, time.Until(at))
	e.running(conv.ID) // the dashboard shows the plan
}

// QuotaFree is when agentID may take a turn again: the first of its
// connections out of quota to reset (QuotaStop), else — a report that named
// no reset, or a connection free already — in RejectedCooldown.
func (e *Engine) QuotaFree(ctx context.Context, agentID string) time.Time {
	if a, err := e.store.Agents().Get(ctx, agentID); err == nil {
		if until, out := e.QuotaStop(ctx, a); out && until.After(time.Now()) {
			return until
		}
	}
	return time.Now().Add(RejectedCooldown)
}

func (e *Engine) armRetry(jobID string, wait time.Duration) {
	e.retries.set(jobID, time.AfterFunc(max(wait, 0), func() {
		if _, err := e.RetryNow(context.Background(), jobID); err != nil && err != ErrNoRetry {
			slog.Warn("chat: retry after a passing API failure", "job", jobID, "err", err)
		}
	}))
}

// ErrNoRetry: the job has no retry planned (any more).
var ErrNoRetry = fmt.Errorf("không có lần tự chạy tiếp nào đang chờ")

// RetryNow goes on now with a job's planned retry: its agent is told in the
// chat, in the name of who started it, and goes on in its session. A chat
// that moved on since (a newer turn) only drops the plan.
func (e *Engine) RetryNow(ctx context.Context, jobID string) (*Turn, error) {
	e.retries.drop(jobID)
	if e.stopping.Load() {
		return nil, ErrStopping // the next office reads the plan again
	}
	ok, err := e.store.Jobs().ClearRetry(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoRetry
	}
	j, err := e.store.Jobs().Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	defer e.running(j.ConversationID)
	// the person writing in the chat since called the plan off (dropRetries);
	// a turn another chat relayed in meanwhile does not (ADR-261)
	conv, err := e.store.Chat().GetConversation(ctx, j.ConversationID)
	if err != nil || !retryable(j, conv) {
		return nil, ErrNoRetry
	}
	a, err := e.store.Agents().Get(ctx, j.AgentID)
	if err != nil {
		return nil, err
	}
	tmpl := "chat/api-retry"
	if j.ErrorCode == OutOfTokensCode {
		tmpl = "chat/quota-retry"
	}
	text := prompts.Render(tmpl, map[string]any{"Agent": a.Name, "N": j.RetryN})
	from := e.lastAsked(ctx, j.ConversationID)
	pageContext := ""
	var info relayInfo
	as := actor.With(ctx, j.CreatedBy)
	if json.Unmarshal([]byte(from.Context), &info) == nil && info.ConversationID != "" { // a relayed message goes on as it came (ADR-176)
		pageContext = from.Context
		if info.Peer != "" {
			as = withPeerFrom(as, info.Peer)
		}
	}
	t, _, err := e.SendWithContext(as, j.ConversationID, text, pageContext, nil)
	return t, err
}

// CancelRetry calls a job's planned retry off.
func (e *Engine) CancelRetry(ctx context.Context, jobID string) error {
	e.retries.drop(jobID)
	ok, err := e.store.Jobs().ClearRetry(ctx, jobID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoRetry
	}
	if j, err := e.store.Jobs().Get(ctx, jobID); err == nil {
		e.running(j.ConversationID)
	}
	return nil
}

// dropRetries calls off the planned retries of a chat: the person wrote
// in it, so its next turn is theirs.
func (e *Engine) dropRetries(ctx context.Context, conversationID string) {
	jobs, err := e.store.Jobs().List(ctx, storage.JobFilter{ConversationID: conversationID, Kind: "chat_turn", Status: "failed", Limit: 5})
	if err != nil {
		return
	}
	for _, j := range jobs {
		if j.RetryAt != nil {
			e.retries.drop(j.ID)
			_, _ = e.store.Jobs().ClearRetry(ctx, j.ID)
		}
	}
}

// PlannedRetry is the retry planned for one of a chat's latest turns, if any.
func (e *Engine) PlannedRetry(ctx context.Context, conversationID string) *Retry {
	// not only the last turn: one relayed in since keeps it (ADR-261)
	jobs, err := e.store.Jobs().List(ctx, storage.JobFilter{ConversationID: conversationID, Kind: "chat_turn", Status: "failed", Limit: 5})
	if err != nil {
		return nil
	}
	for _, j := range jobs {
		if j.RetryAt != nil {
			return e.retryOf(ctx, j)
		}
	}
	return nil
}

// ResumeRetries arms again the retries planned before office went down; one
// already due goes on in a few seconds. It returns how many.
func (e *Engine) ResumeRetries(ctx context.Context) int {
	jobs, err := e.store.Jobs().Retries(ctx)
	if err != nil {
		return 0
	}
	n := 0
	for i, j := range jobs {
		wait := time.Until(*j.RetryAt)
		wait = max(wait, time.Duration(i+1)*RetryDueSpacing) // not all at once
		e.armRetry(j.ID, wait)
		n++
	}
	return n
}

// RetryDueSpacing spaces the retries already due when office starts.
var RetryDueSpacing = 5 * time.Second
