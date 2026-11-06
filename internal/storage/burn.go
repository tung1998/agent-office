package storage

import (
	"context"
	"time"
)

// Active: the Burn runs (working, waiting for its limit or for a technical
// error to clear, or finishing what it has).
func (b BurnSession) Active() bool {
	return b.State == "running" || b.State == "waiting_limit" || b.State == "blocked" || b.State == "draining"
}

// BurnSession is a project's Burn: its settings and whether it runs.
type BurnSession struct {
	ID, ProjectID, ConversationID, AgentID string
	ModelTier                              string // strong | balanced | fast
	MaxParallel                            int    // pieces worked on at once (ADR-117)
	ResultMode                             string // unused: every run is a worktree on its branch (ADR-123)
	RunBranch                              string // this run's branch, burn/<when>; its worktree is named after it
	Focus                                  string
	Order                                  string // roadmap | bugs | auto: what it looks for first (the general template)
	Template                               string // what its workers look for (ADR-128): general | ux | ideas | security | performance | test | docs | custom
	HuntPrompt                             string // the custom template's prompt
	ReviewProfileID                        string // its review profile (ADR-113); "" = none
	Scanned                                string // what its scans looked at, latest last (ADR-116)
	CodeMap                                string // what its scans learned of the codebase (ADR-130)
	Verify                                 string // commands run on a piece reported done, one a line; \"\" = guessed (ADR-131)
	Lessons                                string // what its scans learned of pieces turned down or failed (ADR-131)
	Coverage                               string // its scans' checklist of the areas to cover, "- [ ]"/"- [x]" lines (ADR-141)
	ReviewCap                              int    // pieces done and not merged yet past which it waits for the person (ADR-135); 0 = none
	StopAfter                              int    // pieces done in a run after which it finishes (ADR-135); 0 = none
	NotifyChannelID, NotifyChatID          string // a bot's chat its summary goes to when it stops (ADR-120); "" = none
	EndsAt                                 *time.Time
	Plan                                   BurnPlan // how long a run lasts, when it turns on by itself
	State                                  string   // running | waiting_limit | blocked (a technical error, ADR-152) | draining (finishing what is in progress) | stopped
	WaitingUntil                           *time.Time
	StartedBy                              string
	StartedAt                              *time.Time
	CreatedAt, UpdatedAt                   time.Time
}

// BurnPlan is how a Burn runs once turned on, and when it turns on by itself.
type BurnPlan struct {
	// End: when a run stops. reset (the agent's weekly limit reset, else 8
	// hours; "" too) | hours (Hours after it starts) | clock (the next Clock,
	// HH:MM) | none (until turned off)
	End   string `json:"end,omitempty"`
	Hours int    `json:"hours,omitempty"`
	Clock string `json:"clock,omitempty"`
	// Auto: it turns on by itself at At (HH:MM) on Days (0 Sunday … 6)
	Auto bool   `json:"auto,omitempty"`
	Days []int  `json:"days,omitempty"`
	At   string `json:"at,omitempty"`
	TZ   string `json:"tz,omitempty"` // the time zone of Clock and At ("" = the machine's)
}

// BurnReviewProfile is a saved review setup of a project (ADR-113): the
// stages reviewed, each with its own reviewer.
type BurnReviewProfile struct {
	ID, ProjectID, Name  string
	Stages               map[string]BurnReviewStage // issue | plan | result; absent = not reviewed
	CreatedAt, UpdatedAt time.Time
}

// BurnReviewStage is who reviews a stage: an agent ("" = the Burn's), and
// the workflow it runs ("" = it answers itself).
type BurnReviewStage struct {
	AgentID  string `json:"agent_id"`
	Workflow string `json:"workflow"`
}

// BurnItem is a piece of work a Burn found, and how it went.
type BurnItem struct {
	ID, SessionID, Title, Kind, Detail string
	// Kind is unfinished | upgrade | bug | idea (a worker's), quest (the
	// person's, done as given), or "" (a worker still looking).
	// Owner: who follows it up, an agent key of the project ("" = its
	// default agent; ADR-197).
	Owner                     string
	Status                    string   // found | queued | doing | paused | done | failed | skipped
	RunBranch                 string   // the run it belongs to (its run's branch); "" = a quest not started yet
	Files                     []string // the files it changed, once merged into the run (ADR-131)
	Priority                  int
	Branch, Worktree, Summary string
	Attempts, Subagents       int
	ReviewErrAttempts         int               // system errors (not AI limit) in a row from review(), ADR-120
	Reviewed                  []string          // review stages passed
	ReviewNote                string            // the reviewer's last word: guidance, or why not
	ReviewConversations       map[string]string // stage → its hidden review chat (ADR-114)
	WorkConversationID        string            // its hidden chat with the agent that does it (ADR-116)
	// Restarts: times in a row office restarted (or the Burn stopped) while
	// it was being done: not tries of its own (ADR-257).
	Restarts             int
	CostUSD              float64
	CreatedAt, UpdatedAt time.Time
}

// BurnRepo stores Burn sessions (one a project) and their items.
type BurnRepo interface {
	// Session is a project's (ErrNotFound: none yet).
	Session(ctx context.Context, projectID string) (BurnSession, error)
	// SessionByConversation: the Burn's own chat, or one of its pieces' work or review chats.
	SessionByConversation(ctx context.Context, conversationID string) (BurnSession, error)
	SessionByID(ctx context.Context, id string) (BurnSession, error)
	SaveSession(ctx context.Context, s BurnSession) (BurnSession, error)
	// SetScanned saves what its scans looked at, nothing else (it runs while
	// the person may stop it).
	SetScanned(ctx context.Context, id, scanned string) error
	// SetCodeMap saves its code map, nothing else (ADR-130).
	SetCodeMap(ctx context.Context, id, codeMap string) error
	// SetCoverage saves its coverage plan, nothing else (ADR-141).
	SetCoverage(ctx context.Context, id, coverage string) error
	// SetLessons saves its lessons, nothing else (ADR-131).
	SetLessons(ctx context.Context, id, lessons string) error
	Running(ctx context.Context) ([]BurnSession, error)
	// Scheduled: the Burns that turn on by themselves (Plan.Auto).
	Scheduled(ctx context.Context) ([]BurnSession, error)
	AddItem(ctx context.Context, it BurnItem) (BurnItem, error)
	UpdateItem(ctx context.Context, it BurnItem) error
	// UpdateItemFrom saves it only if the item's current status still equals
	// fromStatus (compare-and-swap); ErrConflict when it has since changed —
	// two burn_pick (or other status change) calls racing for the same item
	// must not both succeed.
	UpdateItemFrom(ctx context.Context, it BurnItem, fromStatus string) error
	Item(ctx context.Context, id string) (BurnItem, error)
	Items(ctx context.Context, sessionID string) ([]BurnItem, error)
	// DeleteItem drops a piece that never became one (a worker that found
	// nothing worth doing, ADR-126).
	DeleteItem(ctx context.Context, id string) error

	ReviewProfiles(ctx context.Context, projectID string) ([]BurnReviewProfile, error)
	ReviewProfile(ctx context.Context, id string) (BurnReviewProfile, error)
	SaveReviewProfile(ctx context.Context, p BurnReviewProfile) (BurnReviewProfile, error)
	// DeleteReviewProfile removes it; the Burns that followed it review no more.
	DeleteReviewProfile(ctx context.Context, id string) error
}
