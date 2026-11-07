package storage

import (
	"context"
	"encoding/json"
	"time"
)

// Workflow is a workflow installed in a project: a copy of a library one
// (spec 2026-10-07-workflows-design), changed there on its own.
type Workflow struct {
	ID, ProjectID string
	Key, Name     string
	Description   string
	Source        string            // the whole file (YAML header + the coordinator's instructions)
	SourceKey     string            // the library workflow it was copied from ("" = made here)
	SourceHash    string            // that one's hash when copied (a newer one shows "có bản mới")
	Bindings      map[string]string // role → agent id ("" = the coordinator picks)
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// WorkflowRepo stores a project's workflows.
type WorkflowRepo interface {
	Create(ctx context.Context, w Workflow) (Workflow, error) // ErrConflict: the key is taken in the project
	Update(ctx context.Context, w Workflow) error
	// UpdateContent writes only key/name/description/source/bindings (not
	// Enabled), so a content save never clobbers a concurrent Enabled toggle
	// (and vice versa: ToggleEnabled never touches these columns).
	UpdateContent(ctx context.Context, w Workflow) error
	// UpdateContentIf is UpdateContent, but refuses with ErrConflict when the
	// row's current source/bindings do not match expectSource/expectBindings
	// (ADR-072: a content write that landed between the caller's Get and
	// this write).
	UpdateContentIf(ctx context.Context, w Workflow, expectSource string, expectBindings map[string]string) error
	// SetEnabled writes only the Enabled column.
	SetEnabled(ctx context.Context, id string, enabled bool) error
	// UpdateContentAndSourceIf is UpdateContentIf, but also writes
	// source_key/source_hash in the same statement (FromLibrary), so a
	// concurrent content edit landing between the caller's Get and this
	// write cannot leave source_hash pointing at a source the row no
	// longer has (ErrConflict when the row's source/bindings moved).
	UpdateContentAndSourceIf(ctx context.Context, w Workflow, expectSource string, expectBindings map[string]string) error
	Get(ctx context.Context, id string) (Workflow, error)
	GetByKey(ctx context.Context, projectID, key string) (Workflow, error)
	List(ctx context.Context, projectID string) ([]Workflow, error) // "" = every project
	Delete(ctx context.Context, id string) error
}

// Workflow run statuses.
const (
	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"
	RunStopped = "stopped"
)

// WorkflowRun is one run of a workflow. It runs in a chat of its own
// (ConversationID, purpose "workflow_run"); the chat that called it
// (CallerConversationID) shows only its input and its output.
type WorkflowRun struct {
	ID, ProjectID, ConversationID string
	CallerConversationID          string // "" = a run from before, held in ConversationID itself
	ParentRunID                   string // the run whose role called this one ("" = a chat called it)
	Depth                         int    // 0 = called from a chat, 1 = by a run called from one…
	WorkflowID, WorkflowKey       string
	WorkflowName                  string
	BodyHash                      string // the workflow as it was when run
	CoordinatorID                 string
	CoordinatorName               string
	Input                         string // what the person asked
	Status                        string // RunRunning | RunDone | RunFailed | RunStopped
	Turns                         int    // turns of the roles so far
	CostUSD                       float64
	Result, Error                 string
	Outputs                       map[string]string // its declared outputs, by key (ADR-103)
	Roles                         []RunRole
	Gates                         []RunGate
	Log                           []RunLog
	Actor                         string
	StartedAt                     time.Time
	FinishedAt                    *time.Time
	// Checkpoint is where a graph of steps is (ADR-256): the next office goes
	// on from there after a restart (nil: a coordinator's run, a run from before)
	Checkpoint *RunCheckpoint
}

// RunCheckpoint is the state of a graph of steps, saved as it goes (ADR-256).
type RunCheckpoint struct {
	Input   map[string]string    `json:"input,omitempty"`   // the values its caller gave by key
	Steps   map[string]StepState `json:"steps,omitempty"`   // the results of the steps that ended
	Visits  map[string]int       `json:"visits,omitempty"`  // how often each step started (max_loops)
	Pending []string             `json:"pending,omitempty"` // reached, waiting their turn
	Running map[string]string    `json:"running,omitempty"` // step id → its type: started, not ended
	Marks   map[string]StepMark  `json:"marks,omitempty"`   // what a running step waits on
	Ended   bool                 `json:"ended,omitempty"`   // an end step was reached
	Summary string               `json:"summary,omitempty"`
	Outputs map[string]string    `json:"outputs,omitempty"`
	Last    string               `json:"last,omitempty"` // the step that ended last
	N       int                  `json:"n,omitempty"`    // steps started so far (MaxStepsRun)
	// Resumes is how often an office went on with it after a restart that
	// cut a step in the middle (a wait or a card is not cut): bounded
	Resumes int `json:"resumes,omitempty"`
	// how the turns of its steps ran: the model tier, the most they may do,
	// the worktree, whether a trusted automation started it
	Tier        string `json:"tier,omitempty"`
	Ceiling     string `json:"ceiling,omitempty"`
	Tree        string `json:"tree,omitempty"`
	TreeNoPatch bool   `json:"tree_no_patch,omitempty"`
	TreePinned  bool   `json:"tree_pinned,omitempty"`
	Trusted     bool   `json:"trusted,omitempty"`
	DepthLeft   int    `json:"depth_left,omitempty"` // sub-workflow levels it may still open
	// CallerJob is the job of the chat that called it: it ends when the run does
	CallerJob string `json:"caller_job,omitempty"`
}

// StepState is what a step left for the steps after it (workflow.StepResult).
type StepState struct {
	Output string `json:"output"`
	Status string `json:"status,omitempty"`
	JSON   any    `json:"json,omitempty"`
}

// StepMark is what a running step waits on, so the next office waits on it too.
type StepMark struct {
	WaitUntil  *time.Time `json:"wait_until,omitempty"`   // a wait step: until when
	ActionID   string     `json:"action_id,omitempty"`    // an approval or a check: its card
	ChildRunID string     `json:"child_run_id,omitempty"` // a sub-workflow: its run
}

// Clone is a copy that shares nothing with c.
func (c *RunCheckpoint) Clone() *RunCheckpoint {
	if c == nil {
		return nil
	}
	b, _ := json.Marshal(c)
	var out RunCheckpoint
	_ = json.Unmarshal(b, &out)
	return &out
}

// RunRole is a role of a run: who fills it and how far it got.
type RunRole struct {
	Role      string  `json:"role"`
	Name      string  `json:"name"`
	Access    string  `json:"access"`
	AgentID   string  `json:"agent_id"`
	AgentName string  `json:"agent_name"`
	Status    string  `json:"status"` // idle | working | done | failed
	Rounds    int     `json:"rounds"` // follow-ups sent
	Turns     int     `json:"turns"`
	SessionID string  `json:"session_id,omitempty"` // its own session, resumed by follow-ups
	Runtime   string  `json:"runtime,omitempty"`
	Effort    string  `json:"effort,omitempty"`   // how hard the coordinator asked it to think ("" = the agent's own)
	Result    string  `json:"result,omitempty"`   // its last answer (cut)
	Workflow  string  `json:"workflow,omitempty"` // a sub-workflow fills it (its key)
	RunID     string  `json:"run_id,omitempty"`   // that one's latest run
	CostUSD   float64 `json:"cost_usd"`
	// Objection: what it pushed back on in its last answer (PHẢN BIỆN), until
	// it answers again or the coordinator overrules it in workflow_done
	Objection string `json:"objection,omitempty"`
}

// RunGate is a gate of a run.
type RunGate struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	Status   string `json:"status"` // open | waiting | passed | failed
	ActionID string `json:"action_id,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// RunLog is one line of what a run did.
type RunLog struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// WorkflowRunRepo stores runs.
type WorkflowRunRepo interface {
	Create(ctx context.Context, r WorkflowRun) (WorkflowRun, error)
	Update(ctx context.Context, r WorkflowRun) error
	Get(ctx context.Context, id string) (WorkflowRun, error)
	// List is newest first; empty ids match everything. conversationID
	// matches the run's own chat or the chat that called it.
	List(ctx context.Context, projectID, conversationID string, limit int) ([]WorkflowRun, error)
	// ListByWorkflow is List further filtered to one workflow, newest first; the
	// filter is applied in SQL before the limit, so a rarely-run workflow's
	// history is not pushed out by other workflows' runs.
	ListByWorkflow(ctx context.Context, projectID, conversationID, workflowID string, limit int) ([]WorkflowRun, error)
	// FailRunning ends the runs left running (the office restarted under them).
	FailRunning(ctx context.Context, detail string, at time.Time) (int64, error)
}
