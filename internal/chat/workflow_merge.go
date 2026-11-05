package chat

import (
	"context"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// mergeWaitPoll: how often a step waiting for the run's diffs looks again.
var mergeWaitPoll = 3 * time.Second

// unmergedWork: a diff of the chat, or a commit held for one (ADR-189),
// still waits to reach the project (pending for a person, or queued).
func (e *Engine) unmergedWork(ctx context.Context, conversationID string) (bool, error) {
	ps, err := e.store.Chat().ListPatches(ctx, conversationID)
	if err != nil {
		return false, err
	}
	for _, p := range ps {
		if p.Status == "pending" || p.Status == "queued" {
			return true, nil
		}
	}
	acts, err := e.store.Actions().List(ctx, conversationID, "", "")
	if err != nil {
		return false, err
	}
	for _, a := range acts {
		if a.Kind == "git_commit" && a.Args.AfterDiff && (a.Status == "pending" || a.Status == "queued") {
			return true, nil
		}
	}
	return false, nil
}

// waitMerged holds a step that reads the project (code, check) while what
// the run's roles changed waits to be merged (and committed): run now it
// would judge a project without the work and fail for nothing. It says so
// once in the run's chat; a stopped run ends the wait.
func (e *Engine) waitMerged(ctx context.Context, own storage.Conversation, s workflow.Step) error {
	told := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		waiting, err := e.unmergedWork(ctx, own.ID)
		if err != nil || !waiting {
			return nil // unknown: the step runs as before
		}
		if !told {
			e.post(own, s, "Chờ diff (và commit đi kèm) của lượt trước được duyệt và gộp vào project rồi mới chạy bước này.") // i18n-ignore
			told = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(mergeWaitPoll):
		}
	}
}

// readsProject: a step whose result depends on the project's files.
func readsProject(s workflow.Step) bool {
	return s.Type == workflow.StepCode || s.Type == workflow.StepCheck
}
