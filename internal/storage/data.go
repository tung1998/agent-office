package storage

import (
	"context"
	"time"
)

// Data cleanup (ADR-095): what the work left behind, and taking it out.

// How a chat or a finished task was cleaned: its content taken out, or put in
// a few lines; its title stays and it takes no more messages.
const (
	CleanContent = "content"
	CleanSummary = "summary"
)

// The kinds of data a project keeps.
const (
	DataChat = "chat" // the project's chats (bots' and automations' too)
	DataBurn = "burn" // a Burn's own chat
	DataTask = "task" // a finished task: its steps, diffs and follow-up chat
)

// DataUsage is how much one kind of data a project keeps.
type DataUsage struct {
	ProjectID string
	Kind      string
	Items     int   // chats, or tasks
	Cleaned   int   // of which already cleaned
	Messages  int   // messages, or steps
	Bytes     int64 // text kept in the database (content, tools, diffs)
}

// DataItem is one chat or finished task that cleanup may take.
type DataItem struct {
	Kind      string
	ID        string // the conversation, or the task
	ProjectID string
	Title     string
	Cleaned   string
	UpdatedAt time.Time
	Messages  int
	Bytes     int64
}

// DataFilter picks the items: a project ("" = all), kinds (none = all), last
// active before a time (zero = any), cleaned ones too or not.
type DataFilter struct {
	ProjectID      string
	Kinds          []string
	Before         time.Time
	IDs            []string // only these ("" = any)
	IncludeCleaned bool
	Limit          int
}

// DataRepo reads what the projects keep (cleanup itself is on ChatRepo and TaskRepo).
type DataRepo interface {
	Usage(ctx context.Context) ([]DataUsage, error)
	Items(ctx context.Context, f DataFilter) ([]DataItem, error)
	// AttachmentIDs are the attachments a message or a task points to.
	AttachmentIDs(ctx context.Context) ([]string, error)
}
