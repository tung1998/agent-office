package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/deciding"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

var (
	// mergeWaitPoll: how often a step waiting for the run's diffs looks again.
	mergeWaitPoll = 3 * time.Second
	// mergeWaitRemind: how often the run's chat is told the step still waits.
	mergeWaitRemind = 30 * time.Minute
	// mergeWaitMax: how long a step waits at most; then it runs on the project
	// as it is, saying so (a diff no one decides must not hold the run until
	// its deadline).
	mergeWaitMax = 2 * time.Hour
	// depsWaitMax: how long a step waits at most when all that is left is a
	// merged diff's install card no one decided (the project does not install
	// on its own): the diff is in, so the step runs on the old libraries
	// rather than hold the run for hours. An install running is waited for.
	depsWaitMax = 15 * time.Minute
)

// depsWaitPrefix starts an install card waiting for a person in what
// unmergedWork lists; depsRunningPrefix one being installed.
const depsWaitPrefix = "cài thư viện " // i18n-ignore

const depsRunningPrefix = "đang cài thư viện " // i18n-ignore

// onlyDepsCards: everything left is install cards waiting for a person.
func onlyDepsCards(waiting []string) bool {
	for _, w := range waiting {
		if !strings.HasPrefix(w, depsWaitPrefix) {
			return false
		}
	}
	return len(waiting) > 0
}

// maxRunChats caps the chats looked at for a run and its sub-runs.
const maxRunChats = 50

// runChats are the run's own chat and those of the runs it called
// (sub-workflow, coordinate steps: each runs in a chat of its own), down
// the tree: their diffs reach the same project.
func (e *Engine) runChats(ctx context.Context, own storage.Conversation) []string {
	out := []string{own.ID}
	for i := 0; i < len(out) && len(out) < maxRunChats; i++ {
		runs, err := e.store.WorkflowRuns().List(ctx, own.ProjectID, out[i], 200)
		if err != nil {
			continue
		}
		for _, r := range runs {
			if r.CallerConversationID == out[i] && r.ConversationID != "" && !slices.Contains(out, r.ConversationID) && len(out) < maxRunChats {
				out = append(out, r.ConversationID)
			}
		}
	}
	return out
}

// unmergedWork lists what of the run (its chat, its sub-runs' chats) still
// waits to reach the project: a diff pending for a person or queued, a
// commit held for one (ADR-189), or the install of a merged diff's
// dependencies (ADR-251).
func (e *Engine) unmergedWork(ctx context.Context, own storage.Conversation) ([]string, error) {
	var out []string
	for _, id := range e.runChats(ctx, own) {
		ps, err := e.store.Chat().ListPatches(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if p.Status == "pending" || p.Status == "queued" {
				out = append(out, "diff "+truncate(strings.Join(p.Files, ", "), 80))
			}
		}
		acts, err := e.store.Actions().List(ctx, id, "", "")
		if err != nil {
			return nil, err
		}
		for _, a := range acts {
			if a.Kind == "git_commit" && a.Args.AfterDiff && (a.Status == "pending" || a.Status == "queued") {
				out = append(out, "commit "+truncate(a.Target, 80))
			}
			// a merged diff's dependencies not installed in the project yet
			// (a card waiting, or installing): a check would run on the old ones (ADR-251)
			if followUp(a) && (a.Status == "pending" || a.Status == "queued") {
				prefix := depsWaitPrefix
				if _, busy := deciding.Is(a.ID); busy || a.Status == "queued" {
					prefix = depsRunningPrefix
				}
				out = append(out, prefix+truncate(a.Target, 80))
			}
		}
	}
	return out, nil
}

// waitMerged holds a step that reads the project (code, check) while what
// the run's roles (or its sub-runs) changed waits to be merged (and
// committed): run now it would judge a project without the work and fail
// for nothing. It says so in the run's chat, again every mergeWaitRemind;
// after mergeWaitMax the step runs anyway, saying what it runs without. A
// stopped run ends the wait.
func (e *Engine) waitMerged(ctx context.Context, own storage.Conversation, s workflow.Step) error {
	start := time.Now()
	var told, depsSince time.Time
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		waiting, err := e.unmergedWork(ctx, own)
		if err != nil && ctx.Err() != nil {
			// stopped while reading: a stopped step, not one whose diffs are
			// in (the store's error was the deadline's, ADR-254)
			return ctx.Err()
		}
		if err != nil || len(waiting) == 0 {
			return nil // unknown: the step runs as before
		}
		what := strings.Join(waiting, "; ")
		now := time.Now()
		onlyDeps := onlyDepsCards(waiting)
		if !onlyDeps {
			depsSince = time.Time{}
		} else if depsSince.IsZero() {
			depsSince = now
		}
		switch {
		case now.Sub(start) >= mergeWaitMax:
			e.post(own, s, fmt.Sprintf("Đã đợi %s mà vẫn chưa gộp: %s. Bước này chạy luôn trên project như hiện có, nên kết quả chưa tính phần đó.", // i18n-ignore
				waited(now.Sub(start)), what))
			return nil
		case onlyDeps && now.Sub(depsSince) >= depsWaitMax:
			// the diff is merged; only its install waits for a person
			e.post(own, s, fmt.Sprintf("Thẻ %s chưa ai duyệt sau %s. Bước này chạy luôn trên thư viện cũ của project, chưa cài lại theo lockfile mới. Muốn office tự cài sau mỗi lần gộp thì bật \"Tự cài thư viện\" trong Chính sách của project.", // i18n-ignore
				what, waited(now.Sub(depsSince))))
			return nil
		case told.IsZero() && onlyDeps:
			e.post(own, s, fmt.Sprintf("Diff đã gộp, chỉ còn chờ duyệt thẻ %s rồi mới chạy bước này (tối đa %s, sau đó chạy luôn trên thư viện cũ). Bật \"Tự cài thư viện\" trong Chính sách để office tự cài.", // i18n-ignore
				what, waited(depsWaitMax)))
			told = now
		case told.IsZero():
			e.post(own, s, "Chờ diff (và commit, cài thư viện đi kèm) của lượt trước được duyệt và gộp vào project rồi mới chạy bước này: "+what+".") // i18n-ignore
			told = now
		case now.Sub(told) >= mergeWaitRemind:
			e.post(own, s, fmt.Sprintf("Bước này vẫn đợi (đã %s, tối đa %s): %s. Duyệt hoặc từ chối các thẻ đó trong chat.", // i18n-ignore
				waited(now.Sub(start)), waited(mergeWaitMax), what))
			told = now
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(mergeWaitPoll):
		}
	}
}

// waited is a wait as the chat says it.
func waited(d time.Duration) string {
	if d >= time.Minute {
		return d.Round(time.Minute).String()
	}
	return d.Round(time.Millisecond).String()
}

// readsProject: a step whose result depends on the project's files.
func readsProject(s workflow.Step) bool {
	return s.Type == workflow.StepCode || s.Type == workflow.StepCheck
}
