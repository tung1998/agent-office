// Package storage defines the persistence contract shared by every driver.
//
// Drivers live in sub-packages (sqlite now, postgres later) and must pass the
// same contract tests. Only the repositories needed by the current milestone
// exist; the rest of docs/DATA_MODEL.md is added table by table.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a lookup matches no row.
	ErrNotFound = errors.New("storage: not found")
	// ErrConflict is returned when a unique constraint would be violated.
	ErrConflict = errors.New("storage: conflict")
)

// Role is a coarse permission level for dashboard users.
type Role string

const (
	RoleAdmin  Role = "admin"  // manage users, config, approvals
	RoleMember Role = "member" // read everything, ask, comment
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleMember }

// User is a dashboard account.
type User struct {
	ID           string
	Email        string
	Name         string
	Role         Role
	PasswordHash string
	Disabled     bool
	// MustChange: the default admin of a first run; it sets a real email and
	// password before anything else.
	MustChange  bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastLoginAt *time.Time
}

// Session is a logged-in browser. Only the SHA-256 of the token is stored.
type Session struct {
	ID         string
	UserID     string
	TokenHash  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	UserAgent  string
	IP         string
}

// AuditEntry is one append-only audit record (ADR-043: who, who approved,
// where from, which channel, before/after).
type AuditEntry struct {
	ID     string
	Actor  string // older "human:<email>" form, kept for compatibility
	Action string
	Target string
	Detail map[string]any
	At     time.Time

	ActorKind  string // human | agent | automation | system
	ActorID    string
	ActorName  string
	ApprovedBy string // the person who approved an agent's proposal
	Via        string // ui | chat | task | assistant | mcp | automation | api

	ProjectID, ConversationID, JobID, TaskID, ActionID string

	Resource, ResourceID string
	Before, After        map[string]any // nil = no snapshot
	OK                   bool
}

// AuditFilter selects audit rows; empty fields match everything.
type AuditFilter struct {
	ProjectID, Resource, ResourceID, ActorKind, ActorID, ActorName, Via, ConversationID, JobID, TaskID string
	From, To                                                                                           time.Time // zero = unbounded
	BeforeID                                                                                           string    // cursor: the id of the last row of the previous page
	Limit                                                                                              int       // default 100, at most 500
}

// AuditCount is one group of a Count.
type AuditCount struct {
	Key           string
	Count, Failed int
}

// Store is the root handle a driver returns.
type Store interface {
	Migrate(ctx context.Context) error
	Close() error

	Users() UserRepo
	// Tokens are personal tokens for a person's own CLI (ADR-047).
	Tokens() TokenRepo
	// Channels are two-way Telegram/Discord bots (ADR-048).
	Channels() ChannelRepo
	Sessions() SessionRepo
	Audit() AuditRepo

	Providers() ProviderRepo
	OrgModels() OrgModelRepo
	Agents() AgentRepo
	Repos() RepoRepo
	Revisions() RevisionRepo
	Runs() RunRepo
	Settings() SettingRepo
	Chat() ChatRepo
	Tasks() TaskRepo
	Processes() ProcessRepo
	Monitors() MonitorRepo
	Actions() ActionRepo
	Jobs() JobRepo
	Automations() AutomationRepo
	// Memories are each agent's long-term notes in a project (ADR-068).
	Memories() MemoryRepo
	// Burn is a project's agent running on its own, finding work (spec 2026-10-01-burn-design).
	Burn() BurnRepo
	// MCPServers are the MCP servers behind the office gateway (ADR-091).
	MCPServers() MCPServerRepo
	// MCPCalls log each tool call through the gateway (ADR-093).
	MCPCalls() MCPCallRepo
	// Data is what the projects keep, for cleanup (ADR-095).
	Data() DataRepo

	// InTx runs fn in one transaction; the Store passed to fn is bound to it.
	InTx(ctx context.Context, fn func(Store) error) error
}

// UserRepo manages dashboard accounts.
type UserRepo interface {
	Create(ctx context.Context, u User) (User, error)
	GetByID(ctx context.Context, id string) (User, error)
	GetByEmail(ctx context.Context, email string) (User, error)
	List(ctx context.Context) ([]User, error)
	Count(ctx context.Context) (int, error)
	UpdatePassword(ctx context.Context, id, hash string) error
	// SetCredentials sets a new email, name and password and clears MustChange.
	SetCredentials(ctx context.Context, id, email, name, hash string) error
	SetDisabled(ctx context.Context, id string, disabled bool) error
	TouchLogin(ctx context.Context, id string, at time.Time) error
	Delete(ctx context.Context, id string) error
}

// SessionRepo manages login sessions.
type SessionRepo interface {
	Create(ctx context.Context, s Session) (Session, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (Session, error)
	Touch(ctx context.Context, id string, at time.Time) error
	Delete(ctx context.Context, id string) error
	DeleteForUser(ctx context.Context, userID string) error
	// DeleteForUserExcept revokes every session of userID but keepID.
	DeleteForUserExcept(ctx context.Context, userID, keepID string) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// AuditRepo appends audit records. There is deliberately no update or delete.
type AuditRepo interface {
	Append(ctx context.Context, e AuditEntry) error
	List(ctx context.Context, f AuditFilter) ([]AuditEntry, error)
	// Count groups by "day" (UTC), "kind", "actor" (kind:name), "resource", "via" or "project".
	Count(ctx context.Context, f AuditFilter, by string) ([]AuditCount, error)
}

// UserToken is a personal token (its hash) for a person's own Claude Code CLI.
type UserToken struct {
	ID, UserID, Name, TokenHash string
	CreatedAt                   time.Time
	LastUsedAt                  *time.Time
	ExpiresAt                   *time.Time // nil: never
	Revoked                     bool
}

// Expired reports whether the token can no longer be used at now.
func (t UserToken) Expired(now time.Time) bool {
	return t.ExpiresAt != nil && !now.Before(*t.ExpiresAt)
}

// TokenRepo stores personal tokens.
type TokenRepo interface {
	Create(ctx context.Context, t UserToken) (UserToken, error)
	List(ctx context.Context, userID string) ([]UserToken, error)
	GetByHash(ctx context.Context, hash string) (UserToken, error)
	Touch(ctx context.Context, id string, at time.Time) error
	Revoke(ctx context.Context, id, userID string) error
}

// Channel is a Telegram or Discord bot an agent of the project answers.
type Channel struct {
	ID, ProjectID, Kind, Name string
	TokenEnc                  string // the bot token, encrypted
	AgentID                   string // "" = the lead
	Mode                      string // the most its runs may do (perm level), default read
	Enabled                   bool
	Allow                     []string // chat/user ids allowed ("" = anyone)
	Scope                     string   // topics it answers
	FilterEnabled             bool     // refuse what is out of Scope (a fast model decides)
	Refusal                   string   // the reply to an out-of-scope message
	Approvers                 []string // user ids who may decide proposals from the chat (none = only on the dashboard)
	Approval                  string   // a new chat's way: ask (commands) | direct (what the agent proposes is approved)
	Header                    string   // the line on top of its answers: {agent} {project} {branch}; "" = default, "-" = none
	ReplyMode                 string   // what a run shows: "" = its answer only, ReplySteps = its steps too (a command may override)
	Defaults                  string   // its commands' default setup (JSON, the dashboard's); "" = none yet
	BotName                   string
	LastError                 string
	LastMessageAt             *time.Time
	CreatedAt, UpdatedAt      time.Time
}

// Reply modes of a bot (Channel.ReplyMode, AutomationConfig.ReplyMode).
const (
	ReplyAnswer = "answer" // a command's override: the answer only
	ReplySteps  = "steps"  // the steps kept in the chat, as the CLI shows them, then the answer
)

// ReplyModeOf is what a command's run shows: its own mode, else its bot's.
func ReplyModeOf(ch Channel, cfg AutomationConfig) string {
	if cfg.ReplyMode == ReplyAnswer || cfg.ReplyMode == ReplySteps {
		return cfg.ReplyMode
	}
	if ch.ReplyMode == ReplySteps {
		return ReplySteps
	}
	return ReplyAnswer
}

// ChannelRepo stores channels and which conversation each outside chat is.
type ChannelRepo interface {
	Create(ctx context.Context, c Channel) (Channel, error)
	Update(ctx context.Context, c Channel) error
	Get(ctx context.Context, id string) (Channel, error)
	List(ctx context.Context, projectID string) ([]Channel, error) // "" = all
	Delete(ctx context.Context, id string) error
	SetStatus(ctx context.Context, id, botName, lastError string, lastMessage *time.Time) error
	Thread(ctx context.Context, channelID, chatID string) (string, error)
	SetThread(ctx context.Context, channelID, chatID, conversationID string) error
	// Prune lets go what ties outside chats to conversations quiet since
	// before (their reply links and rules; the conversations stay).
	Prune(ctx context.Context, before time.Time) (int, error)
}

// MCPServer is an MCP server office manages; agent runs reach it through
// the office gateway /mcp/s/<Name> (ADR-091).
type MCPServer struct {
	ID, Name             string
	Kind                 string // http | stdio
	URL                  string // http
	Command              string // stdio
	Args                 []string
	EnvEnc, HeadersEnc   string // JSON objects, encrypted
	OAuthEnc             string // its OAuth state and tokens, encrypted ("" = none; ADR-092)
	Scope                string // machine | project:<id>
	Origin               string // manual | claude | codex | mcp.json
	Enabled              bool
	LastCheckAt          *time.Time
	LastCheckStatus      string // "" (never) | ok | error | needs_login
	LastCheckError       string
	LastTools            []MCPTool
	Agents               []string // agent ids that get it; empty = every agent (ADR-093)
	TrustedTools         []string // tools that write yet run without approval
	OriginRef            string   // JSON of the config it was moved from ("" = made in office)
	CreatedAt, UpdatedAt time.Time
}

// MCPTool is one tool a server listed at its last check.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	ReadOnly    *bool           `json:"read_only,omitempty"`    // its readOnlyHint, when it gave one
	InputSchema json.RawMessage `json:"input_schema,omitempty"` // for AI called through an API
}

// MCPCall is one tool call through the gateway (ADR-093).
type MCPCall struct {
	ID, ServerID, ServerName, Tool string
	Caller                         string // agent name, "" = a person's own CLI
	CallerKind                     string // claude | codex | api | person
	ProjectID, ConversationID      string
	JobID, ActionID                string
	Status                         string // ok | error | proposed | denied
	Error                          string
	DurationMS                     int64
	CreatedAt                      time.Time
}

// MCPCallStat sums a server's calls since a time.
type MCPCallStat struct {
	ServerID string     `json:"server_id"`
	Calls    int        `json:"calls"`
	Errors   int        `json:"errors"`
	Proposed int        `json:"proposed"`
	LastAt   *time.Time `json:"last_at"`
}

// MCPCallRepo keeps the gateway's call log.
type MCPCallRepo interface {
	Add(ctx context.Context, c MCPCall) error
	// List is newest first; serverID "" = every server.
	List(ctx context.Context, serverID string, limit int) ([]MCPCall, error)
	Stats(ctx context.Context, since time.Time) ([]MCPCallStat, error)
	Prune(ctx context.Context, before time.Time) (int, error)
}

// MCPServerRepo stores the office's MCP servers.
type MCPServerRepo interface {
	Create(ctx context.Context, m MCPServer) (MCPServer, error) // ErrConflict: the name is taken
	Update(ctx context.Context, m MCPServer) error
	Get(ctx context.Context, id string) (MCPServer, error)
	GetByName(ctx context.Context, name string) (MCPServer, error)
	List(ctx context.Context) ([]MCPServer, error)
	Delete(ctx context.Context, id string) error
	SetCheck(ctx context.Context, id, status, errMsg string, tools []MCPTool, at time.Time) error
	// SetOAuth saves only the OAuth state (Update leaves it as it is).
	SetOAuth(ctx context.Context, id, oauthEnc string) error
}

// Memory is one note an agent keeps across conversations (ADR-068).
type Memory struct {
	ID, ProjectID, AgentID string
	Text                   string
	Source                 string // person | agent | compact
	CreatedBy              string
	CreatedAt, UpdatedAt   time.Time
}

// MemoryRevision is the notes as they were before a compaction or a restore.
type MemoryRevision struct {
	ID, ProjectID, AgentID string
	Items                  []Memory
	Reason                 string
	CreatedAt              time.Time
}

// MemoryRepo stores agents' notes, oldest first.
type MemoryRepo interface {
	List(ctx context.Context, projectID, agentID string) ([]Memory, error)
	Get(ctx context.Context, id string) (Memory, error)
	Create(ctx context.Context, m Memory) (Memory, error)
	Update(ctx context.Context, id, text string) error
	Delete(ctx context.Context, id string) error
	// Replace puts items in place of all of the agent's notes (one transaction).
	Replace(ctx context.Context, projectID, agentID string, items []Memory) error
	SaveRevision(ctx context.Context, r MemoryRevision) (MemoryRevision, error)
	Revisions(ctx context.Context, projectID, agentID string) ([]MemoryRevision, error) // newest first
	GetRevision(ctx context.Context, id string) (MemoryRevision, error)
}
