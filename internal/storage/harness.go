package storage

import (
	"context"
	"time"
)

// HarnessFacts are what a project's agents did over a window, raw, for the
// harness review (ADR-181): the diffs and how they ended, the actions, the
// turns, the commands agents ran, what the person wrote, Burn's reviews and
// the agents' notes. internal/harness turns them into evidence.
type HarnessFacts struct {
	Patches      []HarnessPatch
	Actions      []HarnessAction
	Jobs         []HarnessJob
	Tools        []HarnessTools // assistant messages that called tools
	UserMessages []HarnessMessage
	Burn         []HarnessBurnItem
	BurnLessons  string
	Memories     int // the project's agent notes, all time
	NewMemories  int // written in the window
	MemoryTopics []string
}

// HarnessPatch is one diff of a chat or task.
type HarnessPatch struct {
	ID, ConversationID, Status, DecidedBy, Detail string
	Files                                         int
	CreatedAt                                     time.Time
}

// HarnessAction is one proposed action (command, commit, push…).
type HarnessAction struct {
	ID, ConversationID, Kind, Target, Status, DecidedBy, Detail string
	CreatedAt                                                   time.Time
	DecidedAt                                                   time.Time // zero while it waits
}

// HarnessJob is one turn or task run.
type HarnessJob struct {
	ID, ConversationID, Kind, Status, ErrorCode, Error, AgentID string
	CreatedAt                                                   time.Time
}

// HarnessTools are the tool calls of one answer (messages.tools JSON).
type HarnessTools struct {
	ConversationID string
	Tools          string
	CreatedAt      time.Time
}

// HarnessMessage is one message the person wrote (content cut).
type HarnessMessage struct {
	ID, ConversationID, Content string
	CreatedAt                   time.Time
}

// HarnessBurnItem is one piece of work Burn found and how it ended.
type HarnessBurnItem struct {
	Title, Kind, Status, Reviewed, ReviewNote string
	Attempts                                  int
}

// HarnessRepo reads the facts for the harness review.
type HarnessRepo interface {
	Facts(ctx context.Context, projectID string, since time.Time) (HarnessFacts, error)
}
