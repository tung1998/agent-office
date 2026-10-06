package storage

import (
	"context"
	"time"
)

// Job is one run of work: a chat answer, a task, or an automation's turn. It
// is also the queue (ADR-040); the content lives in the chat or the task.
type Job struct {
	ID             string
	ProjectID      string
	Kind           string // chat_turn | task | script
	Origin         string // user | automation | monitor | retry
	OriginID       string // automation id, monitor id or the retried job
	Trigger        string // ui | schedule | webhook | telegram | discord | manual | escalate
	CreatedBy      string
	ConversationID string
	MessageID      string
	TaskID         string
	Status         string // pending | running | done | failed | cancelled | skipped | needs_input
	Error          string
	ErrorCode      string // busy_timeout | budget | out_of_tokens | rate_limit | agent_missing | restart | agent_error | cancelled | disabled
	AgentID        string
	Title          string
	CostUSD        float64
	InputTokens    int
	OutputTokens   int
	DurationMS     int64
	DedupeKey      string
	DebounceKey    string
	DebounceUntil  *time.Time
	Payload        string
	Reply          map[string]any // where to answer (chat apps)
	NextAttemptAt  *time.Time
	CreatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	Output         string // script: the last 64KB of stdout and stderr
	ExitCode       *int   // script: its exit code
	ParentJobID    string // escalation: the script job that asked for an agent
	// ADR-074 security: effective full access for this run (computed once at job start)
	FullAccess   bool   // agent or automation can run with full permissions
	FullAccessBy string // the admin who enabled it (validated at compute time)
	// ExtraDirs: extra directories this run may read, computed once at job
	// start (an automation's override replaces the agent's own, never adds to it).
	ExtraDirs []string
}

// JobFilter narrows a job listing; empty fields match everything.
type JobFilter struct {
	ProjectID string
	Kind      string
	Origin    string
	OriginID  string
	OriginIDs []string // any of these (a bot: its commands, and itself)
	TaskID    string
	// ConversationID: a chat's jobs (its turns)
	ConversationID string
	Source         string // web | discord | telegram | auto (where it came from)
	Query          string // in the title
	Status         string
	AgentID        string
	Since          time.Time
	Before         string // cursor: list jobs older than this job id
	Limit          int
}

// JobStats is jobs grouped by one dimension.
type JobStats struct {
	Key     string  `json:"key"`
	Jobs    int     `json:"jobs"`
	Done    int     `json:"done"`
	Failed  int     `json:"failed"`
	CostUSD float64 `json:"cost_usd"`
	P50MS   int64   `json:"p50_ms"`
	P95MS   int64   `json:"p95_ms"`
}

// JobRepo stores jobs and hands out queued ones.
type JobRepo interface {
	// Create adds a job; ErrConflict when (origin_id, dedupe_key) exists.
	Create(ctx context.Context, j Job) (Job, error)
	Get(ctx context.Context, id string) (Job, error)
	// Update saves status, links, error, payload and queue times.
	Update(ctx context.Context, j Job) error
	// Finish ends a job; cost, tokens and duration come from its model calls.
	Finish(ctx context.Context, id, status, errCode, errMsg string, at time.Time) (Job, error)
	// Claim marks up to limit due pending jobs running, skipping busy origins.
	Claim(ctx context.Context, now time.Time, limit int, busy []string) ([]Job, error)
	List(ctx context.Context, f JobFilter) ([]Job, error)
	// Active counts pending and running jobs of an origin.
	Active(ctx context.Context, origin, originID string) (int, error)
	ByDedupe(ctx context.Context, originID, key string, since time.Time) (Job, error)
	// ByDebounce is the pending job of an origin waiting on a debounce key.
	ByDebounce(ctx context.Context, originID, key string) (Job, error)
	// Debounce replaces the payload and wait of a job still pending; false
	// when it already started.
	Debounce(ctx context.Context, id, payload string, next time.Time) (bool, error)
	// ClearDedupe frees a dedupe key whose window has passed.
	ClearDedupe(ctx context.Context, originID, key string) error
	// Groups gathers jobs by the work they belong to (a chat's turns, a task;
	// an automation run is one of its own), the latest active first; f.Before is a group's key.
	Groups(ctx context.Context, f JobFilter) ([]JobGroup, error)
	// Stats groups jobs by day, kind, origin, agent or status.
	Stats(ctx context.Context, f JobFilter, by string) ([]JobStats, error)
	CostSince(ctx context.Context, origin, originID string, since time.Time) (float64, error)
	// SetOutput stores what a script printed and how it exited.
	SetOutput(ctx context.Context, id, output string, exitCode int) error
	// FailRunning ends every running job (the office restarted under them).
	FailRunning(ctx context.Context, errCode, errMsg string, at time.Time) (int64, error)
}

// Automation starts jobs on a schedule or a trigger (ADR-040).
type Automation struct {
	ID             string
	ProjectID      string
	Name           string
	Enabled        bool
	Source         string // schedule | webhook | telegram | discord
	Config         AutomationConfig
	Action         string // chat | task | script
	AgentID        string // chat: who answers ("" = first lead)
	Prompt         string
	EditMode       string
	ModelTier      string // strong | balanced | fast; "" = the agent's own
	KeepContext    bool
	Limits         AutomationLimits
	Script         AutomationScript   // action script
	Escalate       AutomationEscalate // action script: when to call an agent
	Failures       int
	DisabledCode   string
	DisabledReason string
	LastRunAt      *time.Time
	NextRunAt      *time.Time
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// PermissionMode: "agent" (default) uses the agent's own Permissions
	// (FullAccess/ExtraDirs); "override" replaces them with the fields below
	// (ADR-074). Override never adds to the agent's own rights, it replaces them.
	PermissionMode     string
	OverrideFullAccess bool
	OverrideAdminBy    string // who enabled OverrideFullAccess (must be admin at run time)
	OverrideExtraDirs  []string
}

// AutomationConfig depends on the source.
type AutomationConfig struct {
	EveryMinutes   int    `json:"every_minutes,omitempty"`
	Cron           string `json:"cron,omitempty"`
	Timezone       string `json:"timezone,omitempty"`
	Auth           string `json:"auth,omitempty"` // bearer | header | query
	AuthName       string `json:"auth_name,omitempty"`
	SecretHash     string `json:"secret_hash,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"` // keep_context: the chat reused
	// telegram | discord (ADR-049): the channel whose messages it handles, and
	// which: one of the keywords (none = any), within scope (a fast model decides)
	ChannelID string   `json:"channel_id,omitempty"`
	Keywords  []string `json:"keywords,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	// Command: a custom slash command of the bot ("" = messages by keyword).
	// CommandArg names the text typed after it ("" = none).
	Command            string `json:"command,omitempty"`
	CommandDescription string `json:"command_description,omitempty"`
	CommandArg         string `json:"command_arg,omitempty"`
	// Skill: the command calls this project skill ("/skill text" to the chat)
	Skill string `json:"skill,omitempty"`
	// ReplyMode: what the bot shows of a run, answer | steps ("" = the bot's own)
	ReplyMode string `json:"reply_mode,omitempty"`
	// PullRequest: a webhook of GitHub/Bitbucket pull requests; office runs
	// it on a PR opened or updated only, with the PR's diff ({{diff}}).
	PullRequest bool `json:"pull_request,omitempty"`
	// Notify: what a run answers is sent to this bot's chat too ("" = kept in office).
	NotifyChannelID string `json:"notify_channel_id,omitempty"`
	NotifyChatID    string `json:"notify_chat_id,omitempty"`
	// Tags go on the chat each run talks in (added to its own; at most 10 a chat).
	Tags []string `json:"tags,omitempty"`
	// EndsAt: a schedule stops (turns itself off) at this time, as a Burn
	// does (nil = runs until turned off).
	EndsAt *time.Time `json:"ends_at,omitempty"`
}

// AutomationLimits guard unattended runs.
type AutomationLimits struct {
	MaxRunsPerHour       int     `json:"max_runs_per_hour,omitempty"`
	DailyCostUSD         float64 `json:"daily_cost_usd,omitempty"`
	DisableAfterFailures int     `json:"disable_after_failures,omitempty"`
	DebounceSeconds      int     `json:"debounce_seconds,omitempty"`
	DebounceKey          string  `json:"debounce_key,omitempty"`
	DebounceMaxSeconds   int     `json:"debounce_max_seconds,omitempty"`
	// MaxParallel: runs of it at once (0 = 1). At its time with that many
	// still running, no new one starts — the running ones go on (ADR-082).
	MaxParallel int `json:"max_parallel,omitempty"`
	// MaxMinutes: how long one run may take (0 = no limit).
	MaxMinutes int `json:"max_minutes,omitempty"`
}

// Parallel is how many runs of a may run at once: a kept conversation is
// one chat, answered one turn at a time.
func (a Automation) Parallel() int {
	if a.KeepContext || a.Limits.MaxParallel < 1 {
		return 1
	}
	return a.Limits.MaxParallel
}

// AutomationScript is code an automation runs, without AI (ADR-041).
type AutomationScript struct {
	Lang     string `json:"lang,omitempty"` // bash | node | python
	Body     string `json:"body,omitempty"`
	TimeoutS int    `json:"timeout_s,omitempty"`
}

// AutomationEscalate hands a script's result to an agent when needed.
type AutomationEscalate struct {
	When    string `json:"when,omitempty"`   // never | failure | signal
	Action  string `json:"action,omitempty"` // chat | task
	AgentID string `json:"agent_id,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// AutomationRepo stores automations.
type AutomationRepo interface {
	Create(ctx context.Context, a Automation) (Automation, error)
	Get(ctx context.Context, id string) (Automation, error)
	Update(ctx context.Context, a Automation) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, projectID string) ([]Automation, error)
	// Due lists enabled schedules whose next run has come.
	Due(ctx context.Context, now time.Time) ([]Automation, error)
}

// JobGroup is the jobs of one piece of work: a chat's turns, a task, an
// automation's runs (the latest job's fields describe it).
type JobGroup struct {
	Key            string // c:<conversation> | t:<task> | j:<job>
	LatestID       string // the latest job's id
	ProjectID      string
	Kind           string
	Origin         string
	OriginID       string
	Trigger        string
	CreatedBy      string
	ConversationID string
	TaskID         string
	AgentID        string
	Title          string
	Status         string // the latest job's
	Runs           int
	Failed         int
	Active         int // pending or running
	CostUSD        float64
	LastAt         time.Time
}
