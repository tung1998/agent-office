package storage

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// ProviderKind is how agent-office reaches a model.
type ProviderKind string

const (
	ProviderAnthropic        ProviderKind = "anthropic"         // Anthropic Messages API
	ProviderOpenAI           ProviderKind = "openai"            // OpenAI API
	ProviderOpenAICompatible ProviderKind = "openai_compatible" // any /v1/chat/completions endpoint
	ProviderClaudeCLI        ProviderKind = "claude_cli"        // local `claude -p`
	ProviderCodexCLI         ProviderKind = "codex_cli"         // local `codex exec`
	ProviderGeminiCLI        ProviderKind = "gemini_cli"        // local `gemini` (headless)
	ProviderAntigravityCLI   ProviderKind = "antigravity_cli"   // local `agy` (print mode)
)

// Valid reports whether k is known.
func (k ProviderKind) Valid() bool {
	switch k {
	case ProviderAnthropic, ProviderOpenAI, ProviderOpenAICompatible, ProviderClaudeCLI, ProviderCodexCLI, ProviderGeminiCLI, ProviderAntigravityCLI:
		return true
	}
	return false
}

// IsCLI reports whether the provider runs a local binary instead of an HTTP API.
func (k ProviderKind) IsCLI() bool {
	return k == ProviderClaudeCLI || k == ProviderCodexCLI || k == ProviderGeminiCLI || k == ProviderAntigravityCLI
}

// cliHomeEnv is the variable that moves each CLI's config and credentials
// to another folder: a connection with an account of its own (ADR-178).
var cliHomeEnv = map[ProviderKind]string{ProviderClaudeCLI: "CLAUDE_CONFIG_DIR", ProviderCodexCLI: "CODEX_HOME", ProviderGeminiCLI: "GEMINI_CLI_HOME"}

// OwnAccounts reports whether a connection of kind k can sign in to an
// account of its own (Antigravity CLI keeps it under $HOME only).
func (k ProviderKind) OwnAccounts() bool { return cliHomeEnv[k] != "" }

// CLIEnv is what a connection with an account of its own adds to the CLI's
// environment (ADR-178); nil for the machine's account.
func (p Provider) CLIEnv() []string {
	if p.CLIHome == "" || cliHomeEnv[p.Kind] == "" {
		return nil
	}
	return []string{cliHomeEnv[p.Kind] + "=" + p.CLIHome}
}

// Model tiers let templates say "strong model" without naming a vendor model.
const (
	TierStrong   = "strong"
	TierBalanced = "balanced"
	TierFast     = "fast"
)

// ValidTier reports whether t is a model tier.
func ValidTier(t string) bool { return t == TierStrong || t == TierBalanced || t == TierFast }

// Provider is an AI connection.
type Provider struct {
	ID         string
	Name       string
	Kind       ProviderKind
	Preset     string // catalog entry it was made from ("" = by hand)
	BaseURL    string
	APIKeyEnc  string // encrypted; only internal/secrets decrypts it
	APIKeyEnv  string
	APIKeyHint string
	TierModels map[string]string
	LimitCaps  map[string]int // usage window (five_hour, seven_day) → stop at this percent; 0/missing = off (ADR-136)
	// CLIHome: a Claude Code connection signed in to an account of its own
	// keeps its config and credentials here ("" = the machine's ~/.claude, ADR-178)
	CLIHome      string
	Models       []string
	IsDefault    bool
	Enabled      bool
	Status       string // unknown | ok | error
	StatusDetail string
	CheckedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Permissions bound what an agent may do.
type Permissions struct {
	// Level is the agent's permission package (see internal/perm); empty
	// means derived from ReadOnly (read / propose).
	Level    string `json:"level,omitempty"`
	ReadOnly bool   `json:"read_only"`
	// Caps are the agent's own picks of capabilities; nil = its package's preset.
	Caps *[]string `json:"caps,omitempty"`
	// Commands narrow the project's commands for this agent; nil = all of them.
	Commands *[]string `json:"commands,omitempty"`
	// Processes and Containers it may run/restart on its own (with the
	// capability); nil = the project's check processes, and no containers.
	Processes        *[]string `json:"processes,omitempty"`
	Containers       *[]string `json:"containers,omitempty"`
	RequiresApproval bool      `json:"requires_approval,omitempty"` // side effects need a human
	// FullAccess: admin only (ADR-074), the agent runs with administrator
	// permissions (bypassPermissions) wherever it runs, once the run's level is
	// already Operate (it never raises a lower chat/task mode).
	FullAccess bool `json:"full_access,omitempty"`
	// FullAccessBy: who turned FullAccess on; it must still be an admin at run
	// time, or the agent falls back to its normal level.
	FullAccessBy string `json:"full_access_by,omitempty"`
	// ExtraDirs: admin only, additional read-allowed directories (absolute paths).
	ExtraDirs []string `json:"extra_dirs,omitempty"`
}

// Agent belongs to one project. How agents work together is a workflow
// (internal/workflow), not a hierarchy (ADR-099).
type Agent struct {
	ID           string
	ProjectID    string
	Key          string
	Name         string
	Role         string
	Description  string
	ProviderID   string
	ModelTier    string
	LLMModel     string
	Instructions string
	Permissions  Permissions
	Avatar       Avatar
	Sort         int
	// Disabled: paused from the agent list (column enabled=0). Left out of
	// chat; callers get agentinfo.OffNotice instead of a run. Zero value =
	// on, so agents built in code stay on; only SetEnabled changes it.
	Disabled  bool
	CreatedAt time.Time
	UpdatedAt time.Time
	// FallbackProviderIDs: the connections tried next, top to bottom, when
	// the agent's own one (ProviderID, or the default) fails, is over its
	// limit or is turned off. An entry may name a model ("id|model",
	// FallbackEntry): the same connection can come back with another model.
	FallbackProviderIDs []string
	// Effort: how hard it thinks by default (ValidEffort; "" = the CLI's own).
	Effort string
}

// FallbackEntry is a fallback list entry: a connection id, and a model of it
// ("" = the agent's tier model there).
func FallbackEntry(providerID, model string) string {
	if model = strings.TrimSpace(model); model != "" {
		return providerID + "|" + model
	}
	return providerID
}

// SplitFallback reads an entry FallbackEntry made.
func SplitFallback(entry string) (providerID, model string) {
	id, model, _ := strings.Cut(strings.TrimSpace(entry), "|")
	return strings.TrimSpace(id), strings.TrimSpace(model)
}

// Efforts are the thinking levels a turn may ask for, lowest first ("max" is
// shown as Ultra); "" leaves it to the CLI or model.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

func ValidEffort(e string) bool { return e == "" || slices.Contains(Efforts, e) }

// EffortsFor are the levels a model takes on a kind of connection, lowest
// first; none = it has no choice. Antigravity names the level in the model
// (gemini-3.8-flash-high) or takes none (claude-sonnet-4-6): only its own
// default model picks one, up to high.
func EffortsFor(kind ProviderKind, model string) []string {
	if kind == ProviderAntigravityCLI {
		if model != "" {
			return nil
		}
		return Efforts[:3]
	}
	return Efforts
}

// FitEffort is the level a turn asks that model for: e when it takes it,
// else the highest it takes below e, else "" (left to the model).
func FitEffort(kind ProviderKind, model, e string) string {
	fit, want := "", slices.Index(Efforts, e)
	for _, x := range EffortsFor(kind, model) {
		if slices.Index(Efforts, x) <= want {
			fit = x
		}
	}
	return fit
}

// OffNotice is what anyone calling a paused agent gets (chat, delegate, bots,
// automations), instead of an AI run.
func OffNotice(name string) string {
	return "⏸️ " + name + " đang tạm nghỉ, chưa nhận việc. Bật lại trong danh sách agent."
}

// ErrAgentOff: the agent is paused (Agent.Disabled).
var ErrAgentOff = errors.New("agent đang tạm nghỉ")

// OnAgents are the agents that are not paused.
func OnAgents(agents []Agent) []Agent {
	out := make([]Agent, 0, len(agents))
	for _, a := range agents {
		if !a.Disabled {
			out = append(out, a)
		}
	}
	return out
}

// Avatar is how an agent looks: a color and an icon, or a small image
// (a data URL). Empty = the dashboard derives one from the agent's id.
type Avatar struct {
	Color string `json:"color,omitempty"`
	Icon  string `json:"icon,omitempty"`
	Image string `json:"image,omitempty"`
}

// Repo is a codebase agent-office manages.
type Repo struct {
	ID          string
	Name        string
	Path        string
	GitRemote   string
	Description string
	// DefaultAgentID answers a new chat, bots and automations that name no
	// agent ("" or paused = the first agent on; see DefaultAgent).
	DefaultAgentID string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// DefaultAgent is the project's default agent among agents (sorted): the one
// named by the project if it is on, otherwise the first agent on.
func DefaultAgent(r Repo, agents []Agent) (Agent, bool) {
	for _, a := range agents {
		if a.ID == r.DefaultAgentID && !a.Disabled {
			return a, true
		}
	}
	for _, a := range agents {
		if !a.Disabled {
			return a, true
		}
	}
	return Agent{}, false
}

// DefaultAgentAny is DefaultAgent, or when every agent is paused the default
// one anyway (callers then answer with OffNotice).
func DefaultAgentAny(r Repo, agents []Agent) (Agent, bool) {
	if a, ok := DefaultAgent(r, agents); ok {
		return a, true
	}
	for _, a := range agents {
		if a.ID == r.DefaultAgentID {
			return a, true
		}
	}
	if len(agents) > 0 {
		return agents[0], true
	}
	return Agent{}, false
}

// ProviderRepo manages AI connections.
type ProviderRepo interface {
	Create(ctx context.Context, p Provider) (Provider, error)
	Update(ctx context.Context, p Provider) error
	Get(ctx context.Context, id string) (Provider, error)
	List(ctx context.Context) ([]Provider, error)
	Delete(ctx context.Context, id string) error
	SetDefault(ctx context.Context, id string) error
	SetStatus(ctx context.Context, id, status, detail string, models []string, at time.Time) error
	// SetTierModels writes auto-filled tier models only if the row still matches
	// prevTiers/baseURL/kind (unchanged since they were read); it is a no-op,
	// not an error, if a concurrent edit moved the row in the meantime.
	SetTierModels(ctx context.Context, id string, newTiers, prevTiers map[string]string, baseURL string, kind ProviderKind) (bool, error)
}

// AgentRepo manages the agents of a project.
type AgentRepo interface {
	Create(ctx context.Context, a Agent) (Agent, error)
	Update(ctx context.Context, a Agent) error
	// UpdateIf is Update, but refuses with ErrConflict when the row's
	// content columns no longer match prev (a write landed between the
	// caller's Get and this Update).
	UpdateIf(ctx context.Context, a, prev Agent) error
	Get(ctx context.Context, id string) (Agent, error)
	List(ctx context.Context, projectID string) ([]Agent, error) // by sort
	Delete(ctx context.Context, id string) error
	// SetEnabled pauses or resumes an agent; Update never touches it.
	SetEnabled(ctx context.Context, id string, enabled bool) error
}

// AgentSkill is a skill an agent keeps at hand (ADR-185): its name and why are
// in the agent's prompt, its instructions read with use_skill when needed.
type AgentSkill struct {
	AgentID, Name string
	Why           string
	Source        string // person | agent | setup | library (came with a library agent) | call (the person called it with /name)
	Uses          int
	LastUsedAt    time.Time // zero: never used
	CreatedAt     time.Time
}

// AgentSkillRepo stores the skills each agent keeps, oldest first.
type AgentSkillRepo interface {
	List(ctx context.Context, agentID string) ([]AgentSkill, error)
	// Add puts a skill on the list, or updates its why (Source and uses stay).
	Add(ctx context.Context, s AgentSkill) error
	Remove(ctx context.Context, agentID, name string) error
	// Used counts one use of a skill on the list (none: nothing happens).
	Used(ctx context.Context, agentID, name string) (bool, error)
}

// Revision is a snapshot of a project's agents taken before a change.
type Revision struct {
	ID         string
	ProjectID  string
	Action     string // what was about to happen: agent.update, agent.delete, restore…
	Actor      string
	AgentCount int
	Snapshot   []byte // JSON of team.Snapshot
	CreatedAt  time.Time
}

// RevisionRepo stores snapshots of projects' agents.
type RevisionRepo interface {
	Create(ctx context.Context, r Revision) (Revision, error)
	Get(ctx context.Context, id string) (Revision, error)
	List(ctx context.Context, projectID string, limit int) ([]Revision, error)
	// Prune keeps the newest keep revisions of a project.
	Prune(ctx context.Context, projectID string, keep int) error
}

// Run is one model call.
type Run struct {
	ID           string
	Kind         string
	JobID        string // the job it ran for ("" = none)
	ProjectID    string
	AgentID      string
	ProviderID   string
	ProviderName string
	Model        string
	Status       string // ok | error | blocked
	InputTokens  int
	OutputTokens int
	CostUSD      *float64
	CostSource   string // provider | estimate | unknown
	DurationMS   int64
	Error        string
	Actor        string
	CreatedAt    time.Time
}

// RunFilter narrows a run listing.
type RunFilter struct {
	ProjectID string
	Since     time.Time
	Limit     int
	Before    string // a run id: only the runs older than it (the next page)
}

// UsageRow is spend aggregated by one dimension.
type UsageRow struct {
	Key          string  `json:"key"` // day (YYYY-MM-DD), project id, or model
	Label        string  `json:"label"`
	Runs         int     `json:"runs"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	UnknownCost  int     `json:"unknown_cost"` // runs whose cost is unknown
}

// RunRepo stores model calls.
type RunRepo interface {
	Create(ctx context.Context, r Run) (Run, error)
	List(ctx context.Context, f RunFilter) ([]Run, error)
	// Spent sums known cost since t (optionally for one project).
	Spent(ctx context.Context, since time.Time, projectID string) (float64, error)
	// Since returns every run since t, newest first (for in-Go statistics).
	Since(ctx context.Context, t time.Time) ([]Run, error)
	// Aggregate groups runs since t by "day" (in loc), "project" or "model".
	Aggregate(ctx context.Context, since time.Time, by string, loc *time.Location) ([]UsageRow, error)
}

// SettingRepo stores JSON settings.
type SettingRepo interface {
	Get(ctx context.Context, key string, dst any) (bool, error)
	Set(ctx context.Context, key string, v any) error
	// GetRaw returns the stored JSON for key as-is (for a later SetIf), and
	// whether it exists.
	GetRaw(ctx context.Context, key string) (raw string, ok bool, err error)
	// SetIf stores v under key only if the raw JSON currently stored there
	// still equals expected (what GetRaw last returned; "" means the key
	// must not exist yet). Returns ErrConflict when another write landed
	// since expected was read.
	SetIf(ctx context.Context, key, expected string, v any) error
}

// Conversation is a chat thread with one agent of a project.
type Conversation struct {
	ID           string
	ProjectID    string
	AgentID      string
	AgentName    string
	Title        string
	SessionID    string
	Runtime      string
	Mode         string // permission mode (internal/perm level), a ceiling for this chat
	EditMode     string // where it changes code: perm.EditWorktree (default) or perm.EditDirect
	TaskID       string // set for the follow-up talk about one task
	Purpose      string // "" = a chat of the project; "automation" = builds one automation (not listed)
	AutomationID string // purpose automation: the automation it builds, once saved
	Subject      string // purpose skill/workflow: what it writes ("skill:<scope>:<name>", "wf:<id>", "lib:<key>")
	Effort       string // this chat's thinking level over its agent's ("" = the agent's)
	Cleaned      string // data cleanup: CleanContent | CleanSummary ("" = as it was); takes no more messages
	CleanedAt    *time.Time
	// how full the model's context was after the last answer (0 = unknown)
	ContextTokens, ContextWindow int
	CreatedBy                    string
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
	Tags                         []string // filled by Get and the lists, written with SetConversationTags
}

// TagCount is one tag a project's chats use: how many, and when last added.
type TagCount struct {
	Tag     string    `json:"tag"`
	Count   int       `json:"count"`
	LastUse time.Time `json:"last_used_at"`
}

// ChatMember is one agent in a chat (ADR-044): its own session there, the
// last message it has seen, and its context after its last answer.
type ChatMember struct {
	ConversationID, AgentID, AgentName string
	SessionID, Runtime                 string
	LastMessageID                      string
	ContextTokens, ContextWindow       int
	JoinedAt                           time.Time
}

// ToolCall summarises one tool use while answering.
type ToolCall struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Error   bool   `json:"error,omitempty"`
}

// Message is one turn of a conversation.
type Message struct {
	ID             string
	ConversationID string
	Role           string // user | assistant | error
	Content        string
	Tools          []ToolCall
	Attachments    []Attachment
	RunID          string
	Author         string
	AgentID        string // the agent that sent it (assistant messages only; "" if none)
	CreatedAt      time.Time
	Context        string  // the page the person was on (sent to the agent as data)
	Sender         *Sender // who wrote it from outside office (a bot's chat); nil = in office
	// the connection and model of the run that wrote it (from runs, not stored here)
	Provider string
	Model    string
}

// Sender is the person who wrote to a bot (Discord, Telegram), as the
// platform names them.
type Sender struct {
	Kind     string `json:"kind"`               // discord | telegram
	ID       string `json:"id"`                 // the platform's user id
	Username string `json:"username,omitempty"` // @handle
	Name     string `json:"name,omitempty"`     // display name (nickname on the server, else global)
	Private  bool   `json:"private,omitempty"`  // a direct message, not a server or group
	Link     string `json:"link,omitempty"`     // the message on the platform
	// the office account this platform id is linked to (Users), if any
	AccountID string `json:"account_id,omitempty"`
	Account   string `json:"account,omitempty"` // its name, else its email
}

// Attachment references a file a person attached (see internal/attach).
type Attachment struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // image | pdf | text
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}

// Patch is a proposed code change awaiting approval.
type Patch struct {
	ID             string
	ConversationID string
	MessageID      string
	TaskID         string
	StepID         string
	Diff           string
	Files          []string
	Status         string // pending | applied | rejected | failed
	Detail         string
	DecidedBy      string
	DecidedAt      *time.Time
	CreatedAt      time.Time
	Origin         string // "" = written by the agent, "worktree" = taken from its worktree
	Tree           string // origin worktree: the worktree's name ("" = the chat's or task's own)
}

// ChatRepo stores conversations, messages and patches.
// Change is a chat written, for the dashboard's live data (ADR-078): a
// message added (with it), a conversation made, changed or deleted.
// QueuedMessage is a message written while its chat answered: office sends
// the chat's waiting ones together as the next message once it is free.
type QueuedMessage struct {
	ID, ConversationID string
	Author             string // who wrote it (actor)
	Text, Context      string
	Attachments        []Attachment
	Options            QueuedOptions
	CreatedAt          time.Time
}

// QueuedOptions are the chat settings sent with it, applied when it goes.
type QueuedOptions struct {
	Mode     string  `json:"mode,omitempty"`
	EditMode string  `json:"edit_mode,omitempty"`
	AgentID  string  `json:"agent_id,omitempty"`
	Effort   *string `json:"effort,omitempty"`
}

type Change struct {
	Kind           string // message | conversation | conversation.deleted
	ConversationID string
	Message        *Message
	// ProjectID/CreatedBy: the deleted conversation's, so a listener can still
	// scope visibility (e.g. a private assistant chat) once it is gone.
	ProjectID string
	CreatedBy string
}

// MessageHit is a message found by a search of the chats (ADR-086).
type MessageHit struct {
	MessageID, ConversationID, ProjectID string
	Title, Author, Role, Snippet         string
	CreatedAt                            time.Time
}

// SearchFilter narrows a search: the projects searched, from when, and
// whose private chats (the office assistant's) may show.
type SearchFilter struct {
	ProjectIDs []string
	Since      time.Time
	Author     string // only messages by this one ("" = anyone)
	// HideOthersIn: in this project (the assistant's), only chats made by Me
	HideOthersIn, Me string
	Limit            int
}

type ChatRepo interface {
	// SearchMessages finds messages matching the words of query, best first.
	SearchMessages(ctx context.Context, query string, f SearchFilter) ([]MessageHit, error)
	// MarkSeen: the person looked at the chat now (seen) or wants it unread again.
	MarkSeen(ctx context.Context, userID, conversationID string, seen bool) error
	// Unread: the chats the person wrote in (as author) that an agent
	// answered after they last looked, newest first.
	Unread(ctx context.Context, userID, author string) ([]string, error)
	// MarkAllSeen: every chat Unread would list is seen now, in one go; how many.
	MarkAllSeen(ctx context.Context, userID, author string) (int, error)
	CreateConversation(ctx context.Context, c Conversation) (Conversation, error)
	UpdateConversation(ctx context.Context, c Conversation) error
	GetConversation(ctx context.Context, id string) (Conversation, error)
	// ListConversations lists a project's own chats (not the talks about tasks).
	ListConversations(ctx context.Context, projectID string, limit int) ([]Conversation, error)
	// ListConversationsFrom lists them by where they started: web, discord,
	// telegram, auto, or all (with the bots' chats).
	ListConversationsFrom(ctx context.Context, projectID, source string, limit int) ([]Conversation, error)
	// ListConversationsBefore is the next page: those updated before (zero: the newest).
	ListConversationsBefore(ctx context.Context, projectID, source string, before time.Time, limit int) ([]Conversation, error)
	// ListConversationsTagged is ListConversationsBefore keeping only the chats
	// that have every one of tags (none: all); createdBy not "": only that
	// person's chats (the assistant's own chats are private per person).
	ListConversationsTagged(ctx context.Context, projectID, source string, tags []string, before time.Time, limit int, createdBy string) ([]Conversation, error)
	// ListAgentConversations lists a project's chats (as ListConversationsFrom
	// with source "all") owned by agentID or that it is a member of, updated at
	// or after since (zero: no lower bound), newest first, capped at limit.
	ListAgentConversations(ctx context.Context, projectID, agentID string, since time.Time, limit int) ([]Conversation, error)
	// SetConversationTags replaces a chat's tags.
	SetConversationTags(ctx context.Context, conversationID string, tags []string) error
	// ProjectTags lists the tags of a project's chats, the last used first;
	// createdBy not "": only that person's chats (the assistant's are private).
	ProjectTags(ctx context.Context, projectID, createdBy string) ([]TagCount, error)
	TaskConversation(ctx context.Context, taskID string) (Conversation, error)
	// AutomationConversation is the chat that builds an automation.
	AutomationConversation(ctx context.Context, automationID string) (Conversation, error)
	// SubjectConversation is the latest editor chat (skill, workflow) of a
	// subject; createdBy not "": only that person's.
	SubjectConversation(ctx context.Context, projectID, purpose, subject, createdBy string) (Conversation, error)
	// EmptyConversation is createdBy's latest plain chat of the project with
	// nothing in it yet (no message, nothing queued, no tag).
	EmptyConversation(ctx context.Context, projectID, createdBy string) (Conversation, error)
	// SetConversationSubject ties an editor chat to what it writes.
	SetConversationSubject(ctx context.Context, conversationID, subject string) error
	// LinkAutomation ties a building chat to the automation once it is saved.
	LinkAutomation(ctx context.Context, conversationID, automationID string) error
	DeleteConversation(ctx context.Context, id string) error
	// CleanConversation takes a chat's messages and diffs out and leaves note
	// in their place; state is CleanContent or CleanSummary.
	CleanConversation(ctx context.Context, id, state, note string) error

	AddMessage(ctx context.Context, m Message) (Message, error)
	// SetConversationContext writes only the context columns (turns running at
	// once must not write back an older copy of the rest).
	SetConversationContext(ctx context.Context, id string, tokens, window int) error
	// UpsertMember adds an agent to a chat or updates its session/context (joined_at kept).
	UpsertMember(ctx context.Context, m ChatMember) error
	// Members lists a chat's agents in the order they joined.
	Members(ctx context.Context, conversationID string) ([]ChatMember, error)
	ListMessages(ctx context.Context, conversationID string) ([]Message, error)

	// QueueMessage keeps a message written while the chat answers (sent next).
	QueueMessage(ctx context.Context, q QueuedMessage) (QueuedMessage, error)
	// QueuedMessages lists a chat's waiting messages, oldest first.
	QueuedMessages(ctx context.Context, conversationID string) ([]QueuedMessage, error)
	// QueuedConversations lists the chats with waiting messages.
	QueuedConversations(ctx context.Context) ([]string, error)
	// DeleteQueued drops those of a chat's waiting messages (none named: all).
	DeleteQueued(ctx context.Context, conversationID string, ids ...string) error

	AddPatch(ctx context.Context, p Patch) (Patch, error)
	GetPatch(ctx context.Context, id string) (Patch, error)
	// SetPatchDiff replaces a diff and its files (a worktree's, refreshed onto the project).
	SetPatchDiff(ctx context.Context, id, diff string, files []string) error
	ListPatches(ctx context.Context, conversationID string) ([]Patch, error)
	// PendingPatches lists chats' diffs waiting for a person, newest first.
	PendingPatches(ctx context.Context, limit int) ([]Patch, error)
	// PatchRefs lists one chat's diffs, oldest first, without their diff text.
	PatchRefs(ctx context.Context, conversationID string) ([]Patch, error)
	DecidePatch(ctx context.Context, id, status, detail, by string, at time.Time) error
	// QueuePatch marks a pending diff approved by by, waiting for the chat's
	// earlier proposals ("queued").
	QueuePatch(ctx context.Context, id, by string) error
}

// Task is a goal given to a project's agents (old Việc data, ADR-057).
type Task struct {
	ID          string
	ProjectID   string
	Title       string
	Goal        string
	Mode        string // single | hierarchy | council
	Status      string // running | done | failed | cancelled | rejected
	Result      string
	Detail      string
	BudgetUSD   float64
	CostUSD     float64
	ModeLevel   string // permission mode (internal/perm level), a ceiling for this task
	EditMode    string // where it changes code: perm.EditWorktree (default) or perm.EditDirect
	AssigneeID  string // one agent does it alone ("" = the team)
	Attachments []Attachment
	CreatedBy   string
	CreatedAt   time.Time
	FinishedAt  *time.Time
	Cleaned     string // data cleanup: CleanContent | CleanSummary ("" = as it was)
}

// TaskStep is one agent's turn inside a task.
type TaskStep struct {
	ID          string
	TaskID      string
	Seq         int
	Phase       string // plan | vote | revise | work | review | synthesize
	AgentID     string
	AgentKey    string
	AgentName   string
	Instruction string
	Output      string
	Data        map[string]any
	Tools       []ToolCall
	Status      string // running | done | failed | skipped
	Error       string
	CostUSD     *float64
	RunID       string
	StartedAt   time.Time
	FinishedAt  *time.Time
}

// TaskRepo stores tasks and their steps.
type TaskRepo interface {
	Create(ctx context.Context, t Task) (Task, error)
	Update(ctx context.Context, t Task) error
	Get(ctx context.Context, id string) (Task, error)
	List(ctx context.Context, projectID string, limit int) ([]Task, error) // "" = all projects
	Delete(ctx context.Context, id string) error
	AddStep(ctx context.Context, s TaskStep) (TaskStep, error)
	UpdateStep(ctx context.Context, s TaskStep) error
	ListSteps(ctx context.Context, taskID string) ([]TaskStep, error)
	// ListByAgentSince lists a project's tasks created at or after since that
	// have at least one step run by agentID.
	ListByAgentSince(ctx context.Context, projectID, agentID string, since time.Time) ([]Task, error)
	// CleanTask takes a finished task's steps and diffs out; result replaces its result.
	CleanTask(ctx context.Context, id, state, result string) error
	ListPatches(ctx context.Context, taskID string) ([]Patch, error)
	// FailRunning ends tasks left running (the office restarted under them).
	FailRunning(ctx context.Context, detail string, at time.Time) (int64, error)
}

// RepoRepo manages registered repositories.
type RepoRepo interface {
	Create(ctx context.Context, r Repo) (Repo, error)
	Update(ctx context.Context, r Repo) error
	// UpdateInfo writes only name and description, leaving other fields untouched.
	UpdateInfo(ctx context.Context, id, name, description string) error
	// UpdateInfoRemote writes name, description and git remote, leaving other fields (notably default_agent_id) untouched.
	UpdateInfoRemote(ctx context.Context, id, name, description, gitRemote string) error
	// SetDefaultAgent writes only the default agent, leaving other fields untouched.
	SetDefaultAgent(ctx context.Context, id, agentID string) error
	Get(ctx context.Context, id string) (Repo, error)
	GetByPath(ctx context.Context, path string) (Repo, error)
	List(ctx context.Context) ([]Repo, error)
	Delete(ctx context.Context, id string) error
}

// Process is a command office runs for a project (dev server, build, test).
type Process struct {
	ID          string
	ProjectID   string
	Name        string
	Command     string
	Cwd         string // relative to the project folder
	Kind        string // service | job
	Source      string
	Autostart   bool
	Autorestart bool
	Env         map[string]string // over the office's environment when it runs (PORT…)
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProcessRepo stores process definitions.
type ProcessRepo interface {
	Create(ctx context.Context, p Process) (Process, error)
	Update(ctx context.Context, p Process) error
	Get(ctx context.Context, id string) (Process, error)
	List(ctx context.Context, projectID string) ([]Process, error) // "" = all projects
	Delete(ctx context.Context, id string) error
}

// Monitor watches an office process or a compose service of a project (one
// per process/service).
type Monitor struct {
	ID            string
	ProjectID     string
	Name          string
	Type          string // process | container
	Target        string // process id | compose service
	Config        MonitorConfig
	IntervalS     int
	Enabled       bool
	AIEnabled     bool
	AIBudgetUSD   float64
	Status        string // pending | up | down
	Fails         int
	LastCheckedAt *time.Time
	LastChangeAt  *time.Time
	LastLatencyMS int
	LastMessage   string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// MonitorConfig holds type-specific options.
type MonitorConfig struct {
	File string `json:"file,omitempty"` // compose file (container)
}

// MonitorCheck is one check result.
type MonitorCheck struct {
	MonitorID string
	At        time.Time
	OK        bool
	LatencyMS int
	Message   string
}

// MonitorEvent is a status change.
type MonitorEvent struct {
	ID             string
	MonitorID      string
	ProjectID      string
	Kind           string // up | down
	Message        string
	Analysis       string
	AnalysisStatus string
	CostUSD        float64
	At             time.Time
}

// MonitorRepo stores monitors, their checks and events.
type MonitorRepo interface {
	Create(ctx context.Context, m Monitor) (Monitor, error)
	Update(ctx context.Context, m Monitor) error     // definition fields
	SaveStatus(ctx context.Context, m Monitor) error // status fields
	Get(ctx context.Context, id string) (Monitor, error)
	List(ctx context.Context, projectID string) ([]Monitor, error) // "" = all
	Delete(ctx context.Context, id string) error

	AddCheck(ctx context.Context, c MonitorCheck) error
	Checks(ctx context.Context, monitorID string, since time.Time) ([]MonitorCheck, error)
	PruneChecks(ctx context.Context, before time.Time) error

	AddEvent(ctx context.Context, e MonitorEvent) (MonitorEvent, error)
	UpdateEvent(ctx context.Context, e MonitorEvent) error
	Events(ctx context.Context, projectID string, limit int) ([]MonitorEvent, error) // "" = all
	AICostSince(ctx context.Context, monitorID string, since time.Time) (float64, error)
	// RecentAnalysis reports whether monitorID has a running or completed
	// analysis event (other than excludeID) since the given time.
	RecentAnalysis(ctx context.Context, monitorID, excludeID string, since time.Time) (bool, error)
}

// Action is an operation an agent proposed; it runs only after approval.
type Action struct {
	ID             string
	ProjectID      string
	ConversationID string
	MessageID      string
	TaskID         string
	RunRef         string
	JobID          string // the chat answer/task run it was proposed in
	Kind           string
	Target         string
	TargetID       string
	Reason         string
	Args           ActionArgs
	Status         string // pending | done | failed | rejected
	Detail         string
	ProposedBy     string
	DecidedBy      string
	DecidedAt      *time.Time
	CreatedAt      time.Time
}

// ActionArgs are extra inputs of an action.
type ActionArgs struct {
	Message string   `json:"message,omitempty"` // git commit
	Files   []string `json:"files,omitempty"`   // git commit
	Branch  string   `json:"branch,omitempty"`  // git branch
	Dir     string   `json:"dir,omitempty"`     // run_command: the worktree it runs in ("" = project folder)
	// create_automation / update_automation: the proposed automation (trigger.Spec)
	Automation json.RawMessage `json:"automation,omitempty"`
	// config_change: a settings change through the config registry (ADR-045)
	Change *ConfigChange `json:"change,omitempty"`
	// mcp_call: a call of an office MCP tool that writes (ADR-093)
	MCP *MCPCallArgs `json:"mcp,omitempty"`
	// remember: the topic the note goes under and its index line ("" = core, ADR-134)
	Topic   string `json:"topic,omitempty"`
	Summary string `json:"summary,omitempty"`
	// remember: the agent's notes the new one takes the place of; forget:
	// the notes to drop (ADR-215)
	Replaces []string `json:"replaces,omitempty"`
	// send_message to a linked project: its id; with no target chat, a new
	// chat is opened there
	Project string `json:"project,omitempty"`
	// send_message to a linked project in another office (ADR-167): the
	// paired office's id; Project is then its project's id there
	Peer string `json:"peer,omitempty"`
	// create_project / import_project: the project to make (ADR-170)
	NewProject *NewProject `json:"new_project,omitempty"`
	// data_delete: the record of a collection to delete (ADR-172)
	Record *RecordRef `json:"record,omitempty"`
	// AfterDiff: a commit proposed in a turn whose worktree changes are not
	// in the project yet; it comes after the turn's diff and runs once that
	// is merged (ADR-189)
	AfterDiff bool `json:"after_diff,omitempty"`
}

// RecordRef names a record of a project's collection.
type RecordRef struct {
	Collection string `json:"collection"`
	ID         string `json:"id"`
}

// NewProject is a project the office assistant proposes to make (ADR-170):
// cloned from URL, at an existing Path, or a new empty repo in Dir; Pack, a
// project pack (.zip) to import into it, Include what of it.
type NewProject struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url,omitempty"`
	Path        string   `json:"path,omitempty"`
	Dir         string   `json:"dir,omitempty"`
	Pack        string   `json:"pack,omitempty"`
	Include     []string `json:"include,omitempty"`
	// Parent: the project an administrator made it from (ADR-191): the two
	// link to each other and its agent learns who it reports to.
	Parent string `json:"parent,omitempty"`
}

// MCPCallArgs is a tool call waiting for a person.
type MCPCallArgs struct {
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ConfigChange is a proposed change of one setting: create, update or delete
// a resource (automation, agent, …) with a JSON patch; Before is the setting
// as it was when proposed (the approval refuses if it changed since).
type ConfigChange struct {
	Resource string          `json:"resource"`
	Op       string          `json:"op"` // create | update | delete
	ID       string          `json:"id,omitempty"`
	Patch    json.RawMessage `json:"patch,omitempty"`
	Before   json.RawMessage `json:"before,omitempty"` // the fields a patch may set, secrets hidden (shown on the card)
	Hash     string          `json:"hash,omitempty"`   // of those fields unhidden: a change since the proposal makes it stale
}

// ActionRepo stores proposed actions.
type ActionRepo interface {
	Create(ctx context.Context, a Action) (Action, error)
	Update(ctx context.Context, a Action) error
	Get(ctx context.Context, id string) (Action, error)
	// List filters by conversation, task or run (the first non-empty one).
	List(ctx context.Context, conversationID, taskID, runRef string) ([]Action, error)
	// Pending lists actions waiting for a person, newest first.
	Pending(ctx context.Context, limit int) ([]Action, error)
}
