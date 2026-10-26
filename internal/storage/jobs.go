package storage

import (
	"context"
	"strings"
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

// SendsAs is who a run speaks for when it sends into another chat: the person
// who started it, or, for a scheduled automation run as administrator, the
// admin who gave the agent that right (ADR-191). Bots and webhooks: nobody.
// SpeaksFor also knows an automation's owner (ADR-194).
func (j Job) SendsAs() (string, bool) {
	if strings.HasPrefix(j.CreatedBy, "human:") {
		return j.CreatedBy, true
	}
	if j.scheduled() && j.FullAccess && j.FullAccessBy != "" {
		return "human:" + j.FullAccessBy, true
	}
	return "", false
}

func (j Job) scheduled() bool {
	return j.Origin == "automation" && (j.Trigger == "schedule" || j.Trigger == "manual")
}

// SpeaksFor is SendsAs, and for a scheduled automation the person who owns
// it (ADR-194): the account that saved it, or for one an agent made, the
// person its turn spoke for. A disabled or unknown account: nobody.
func SpeaksFor(ctx context.Context, st Store, j Job) (string, bool) {
	if as, ok := j.SendsAs(); ok || !j.scheduled() {
		return as, ok
	}
	a, err := st.Automations().Get(ctx, j.OriginID)
	if err != nil || !strings.Contains(a.CreatedBy, "@") || strings.ContainsAny(a.CreatedBy, " :") {
		return "", false
	}
	if u, err := st.Users().GetByEmail(ctx, a.CreatedBy); err != nil || u.Disabled {
		return "", false
	}
	return "human:" + a.CreatedBy, true
}

// JobFilter narrows a job listing; empty fields match everything.
type JobFilter struct {
	ProjectID        string
	ExcludeProjectID string // omit this project's jobs (the office assistant's own)
	// HideOthersIn: an assistant-chat project whose jobs are hidden unless
	// CreatedBy is Me (ADR-046: an assistant chat is its creator's alone).
	HideOthersIn string
	Me           string
	Kind         string
	Origin       string
	OriginID     string
	OriginIDs    []string // any of these (a bot: its commands, and itself)
	TaskID       string
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
	// StartIfRunning stamps an automation job as this chat turn (conversation,
	// agent, title, started_at) but only while it is still "running";
	// ErrConflict when a cancel already moved it elsewhere (e.g. "cancelled")
	// so the caller must not overwrite that.
	StartIfRunning(ctx context.Context, id, conversationID, agentID, title string, startedAt time.Time) (Job, error)
	// Finish ends a job; cost, tokens and duration come from its model calls.
	Finish(ctx context.Context, id, status, errCode, errMsg string, at time.Time) (Job, error)
	// FinishIfRunning is Finish, but only applies when the job is still
	// running; ErrConflict when it already ended (e.g. a cancel racing the
	// job's own completion).
	FinishIfRunning(ctx context.Context, id, status, errCode, errMsg string, at time.Time) (Job, error)
	// FinishIfPending is Finish, but only applies when the job is still
	// pending; ErrConflict when it already started (e.g. a cancel racing
	// Claim/StartReady).
	FinishIfPending(ctx context.Context, id, status, errCode, errMsg string, at time.Time) (Job, error)
	// Claim marks up to limit due pending jobs running. remaining is, for an
	// origin with jobs already running, how many more of it may run now
	// (0 = none); an origin missing from it has nothing running yet.
	Claim(ctx context.Context, now time.Time, limit int, remaining map[string]int) ([]Job, error)
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
	// SetMessageID links a job to the chat message its answer became; touches
	// only that column (unlike Update, it can't race a status change made in
	// between its caller's Get and its write).
	SetMessageID(ctx context.Context, id, messageID string) error
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
	// Workflow: action "workflow" runs this workflow of the project, its
	// prompt being the input ("#key input" to the chat, ADR-109)
	Workflow string `json:"workflow,omitempty"`
	// ReplyMode: what the bot shows of a run, answer | steps ("" = the bot's own)
	ReplyMode string `json:"reply_mode,omitempty"`
	// OwnSetup: a bot's command set up on its own; false = its bot's default
	// setup (the bot's page copies it in at each save)
	OwnSetup bool `json:"own_setup,omitempty"`
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
	// Goal: action chat/workflow runs until it is reached (ADR-132; nil = one turn).
	Goal *AutomationGoal `json:"goal,omitempty"`
	// OnlyIf: a schedule runs only when its data says there is work (ADR-194; nil = always).
	OnlyIf *AutomationCondition `json:"only_if,omitempty"`
	// WakeAt: the next run one of its runs asked for (ADR-202); that run
	// goes even when OnlyIf finds nothing, then the schedule resumes.
	WakeAt *time.Time `json:"wake_at,omitempty"`
}

// AutomationCondition lets a frequent schedule cost nothing when there is
// nothing to do: it runs when a record of Collection matches Where, or (with
// Changed) when one changed since its last run, or when the work board says
// so (Board, ADR-196). None given: always.
type AutomationCondition struct {
	Collection string `json:"collection,omitempty"`
	Where      []Cond `json:"where,omitempty"`
	Changed    bool   `json:"changed,omitempty"`
	// Board: the project's work board (its Burn's). open = work no one is
	// doing (the Burn off with pieces left, or a piece waiting for review);
	// changed = a piece added, finished or waiting for review since its
	// last run (not one being worked on);
	// any = either.
	Board string `json:"board,omitempty"`
}

// Board conditions of a schedule (ADR-196).
const (
	BoardOpen    = "open"
	BoardChanged = "changed"
	BoardAny     = "any"
)

// AutomationGoal is "run until a condition is true" (ADR-132): after each
// turn, the check and/or the judge; not reached, the chat goes on with what
// they said, at most MaxRounds turns in all.
type AutomationGoal struct {
	Text      string `json:"text,omitempty"`       // what reached looks like (the judge reads it)
	Check     string `json:"check,omitempty"`      // a shell command in the project folder: exit 0 = reached
	Judge     bool   `json:"judge,omitempty"`      // a separate chat with the agent, read only, says whether Text is met
	MaxRounds int    `json:"max_rounds,omitempty"` // 0 = 5, at most 20
}

// On: the goal has something to check it with.
func (g *AutomationGoal) On() bool { return g != nil && (g.Check != "" || g.Judge) }

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
	// BurnStart: the run gave the Burn quests (@@quest lines) and the Burn is
	// stopped: it starts (ADR-132).
	BurnStart bool `json:"burn_start,omitempty"`
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
	// Update writes the whole row, CAS'd on the updated_at a was read with:
	// ErrConflict when the row changed since (another Update, or one of the
	// narrow writes below) instead of silently overwriting it.
	Update(ctx context.Context, a Automation) error
	// UpdateSpec writes only the fields a saved proposal can change — name,
	// source, action, agent_id, prompt, config (keeping its current
	// conversation_id and secret_hash), script, escalate, next_run_at — so an
	// edit applied from a stale read-modify-write (the automation builder's
	// approve path) can't revert run-tracking fields (failures, enabled,
	// disabled_code, disabled_reason, last_run_at) that RecordRun or Tick
	// changed concurrently.
	UpdateSpec(ctx context.Context, a Automation) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, projectID string) ([]Automation, error)
	// Due lists enabled schedules whose next run has come.
	Due(ctx context.Context, now time.Time) ([]Automation, error)
	// RecordRun applies the result of one run — last run time, the
	// consecutive-failure counter, and the kept conversation id (if any) — in
	// a single atomic statement, so two runs of the same automation finishing
	// close together (Parallel()>1) can't read the same Failures and
	// overwrite each other's increment. result is "ok" (reset Failures to 0),
	// "fail" (increment Failures, disabling the automation once it reaches
	// limit) or "skip" (a budget stop: Failures untouched either way).
	RecordRun(ctx context.Context, id string, now time.Time, result, failReason string, limit int, keptConv string) error
	// UpdateSecretHash rotates a webhook automation's secret hash only,
	// so it doesn't overwrite a concurrent edit to the rest of the
	// automation (name, config, limits, prompt, ...).
	UpdateSecretHash(ctx context.Context, id, secretHash string) error
	// Reenable turns an automation back on without touching its other
	// fields (name, config, script, ...), so a concurrent edit in
	// AutomationBuilder isn't overwritten by a stale read-modify-write.
	Reenable(ctx context.Context, id string, nextRunAt *time.Time) error
	// SetNextRun writes next_run_at only, so a schedule tick computing the
	// next run doesn't clobber a concurrent admin edit to the rest of the row.
	SetNextRun(ctx context.Context, id string, nextRunAt *time.Time) error
	// SetWake writes next_run_at and config.wake_at only (ADR-202): a run
	// of the schedule asks for its next run at; nil clears the request.
	SetWake(ctx context.Context, id string, at *time.Time) error
	// Disable turns an automation off with a reason (a safety stop: a daily
	// cost cap or a schedule's end time), touching only enabled, the disabled
	// fields and next_run_at — so it can't be undone by a concurrent admin
	// edit's full-row Update racing in behind it (the admin's Update is CAS'd
	// on updated_at, so it loses that race instead of silently reviving the
	// automation).
	Disable(ctx context.Context, id, code, reason string, nextRunAt *time.Time) error
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
