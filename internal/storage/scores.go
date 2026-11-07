package storage

import (
	"context"
	"time"
)

// JobScore is how one finished job went, for the model/effort statistics
// (ADR-267): what it ran on, what kind of work it was, and its quality Q
// (0..100 from Parts; nil = no criterion applies or not scored).
type JobScore struct {
	JobID          string     `json:"job_id"`
	ProjectID      string     `json:"project_id"`
	ConversationID string     `json:"conversation_id"`
	MessageID      string     `json:"message_id"`
	AgentID        string     `json:"agent_id"`
	ProviderID     string     `json:"provider_id"`
	Model          string     `json:"model"`
	Effort         string     `json:"effort"`
	TaskKind       string     `json:"kind"`         // ask | bugfix | feature | review | burn | automation | workflow
	Difficulty     string     `json:"difficulty"`   // light | medium | heavy
	LabelManual    bool       `json:"label_manual"` // the person set the labels: a re-score keeps them
	Status         string     `json:"status"`       // the job's status when it ended
	Q              *int       `json:"q"`
	Parts          ScoreParts `json:"parts"`
	Feedback       int        `json:"feedback"` // -1 👎, 0 none, 1 👍
	CostUSD        float64    `json:"cost_usd"`
	DurationMS     int64      `json:"duration_ms"`
	FinishedAt     time.Time  `json:"finished_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Frozen         bool       `json:"frozen"` // past the re-score window: no longer changes
}

// ScoreParts are Q's criteria; nil = it does not apply to the job.
type ScoreParts struct {
	Merged       *bool `json:"merged,omitempty"`        // its diffs applied, none rejected
	NoRework     *bool `json:"no_rework,omitempty"`     // done without being sent back
	NoCorrection *bool `json:"no_correction,omitempty"` // the person did not correct it (👍/👎 first)
	ChecksGreen  *bool `json:"checks_green,omitempty"`  // build/test/typecheck it ran passed
}

// ScoreFilter narrows a score listing (zero fields = any).
type ScoreFilter struct {
	ProjectID      string
	ConversationID string
	AgentID        string
	Since          time.Time
}

// ScoreRepo keeps the job scores.
type ScoreRepo interface {
	// Upsert writes a score; labels the person set (LabelManual) are kept.
	Upsert(ctx context.Context, s JobScore) error
	// Update rewrites a score's Q, parts, feedback and frozen flag only if it
	// did not change since it was read (its UpdatedAt was prevUpdated):
	// ErrStale otherwise, ErrNotFound when there is none.
	Update(ctx context.Context, s JobScore, prevUpdated time.Time) error
	Get(ctx context.Context, jobID string) (JobScore, error)
	// ByMessage is the score of the job that wrote that answer.
	ByMessage(ctx context.Context, messageID string) (JobScore, error)
	// List: newest first.
	List(ctx context.Context, f ScoreFilter) ([]JobScore, error)
	// Open: scores not frozen that finished before t (to freeze).
	Open(ctx context.Context, before time.Time) ([]JobScore, error)
	// SetLabels is the person's own kind/difficulty for a job.
	SetLabels(ctx context.Context, jobID, kind, difficulty string) error
}
