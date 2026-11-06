package storage

import (
	"context"
	"time"
)

// Advice is an advisor's second opinion for an agent stuck on the same
// failure (ADR-262), or its review of such a run's answer before it ends.
type Advice struct {
	ID             string
	ProjectID      string
	ConversationID string
	JobID          string
	MessageID      string // the answer of the run it went to ("" = not yet)
	AgentID        string // the stuck agent
	AgentName      string
	AdvisorID      string
	AdvisorName    string
	Provider       string
	Model          string
	Trigger        string // advice.TriggerRepeat | advice.TriggerDone
	Command        string
	Error          string
	Answer         string
	Objection      bool   // done: the advisor objects to the answer
	Status         string // AdviceAnswered | AdviceFailed | AdviceSkipped
	Reason         string // skipped/failed: why
	CostUSD        *float64
	CreatedAt      time.Time
}

const (
	AdviceAnswered = "answered"
	AdviceFailed   = "failed"
	AdviceSkipped  = "skipped"
)

// AdviceStats sums the advices since a time, for the overview.
type AdviceStats struct {
	Asked    int     `json:"asked"`    // answered ones (skipped not counted)
	Skipped  int     `json:"skipped"`  // no advisor, over budget, failed
	Objected int     `json:"objected"` // reviews that objected
	RunsDone int     `json:"runs_done"`
	Runs     int     `json:"runs"` // runs that got an answered advice
	CostUSD  float64 `json:"cost_usd"`
}

// AdviceRepo keeps the advices.
type AdviceRepo interface {
	Create(ctx context.Context, a Advice) (Advice, error)
	// Conversation: a chat's advices, oldest first.
	Conversation(ctx context.Context, conversationID string, limit int) ([]Advice, error)
	// SetMessage links the advices of job to the answer it became.
	SetMessage(ctx context.Context, jobID, messageID string) error
	// SpentSince is what the advices cost since t.
	SpentSince(ctx context.Context, t time.Time) (float64, error)
	Stats(ctx context.Context, since time.Time) (AdviceStats, error)
}
