package chat

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// queuedFirst: the chat's first waiting card is approved and queued (Drain
// would run it now).
func (e *Engine) queuedFirst(ctx context.Context, conversationID string) bool {
	list, err := e.waiting(ctx, conversationID)
	return err == nil && len(list) > 0 && list[0].queued
}

// dropHeldFor settles the commits held for a diff (ADR-189) that will never
// reach the project: rejected (or skipped) by a person, or failed to merge.
// Left as they were, a pending one blocked every later card of the chat (and
// a workflow step waiting for the merge), a queued one ran and failed with
// "nothing to commit", hiding why. A commit that also names files outside
// that diff keeps waiting for those, without the diff's files.
func (e *Engine) dropHeldFor(ctx context.Context, p storage.Patch, status, detail, by string, at time.Time) {
	if p.MessageID == "" || (status != "rejected" && status != "failed") {
		return
	}
	acts, err := e.store.Actions().List(ctx, p.ConversationID, "", "")
	if err != nil {
		return
	}
	for _, a := range acts {
		if a.Kind != "git_commit" || !a.Args.AfterDiff || a.MessageID != p.MessageID || (a.Status != "pending" && a.Status != "queued") {
			continue
		}
		var rest []string
		for _, f := range a.Args.Files {
			if !slices.Contains(p.Files, f) {
				rest = append(rest, f)
			}
		}
		if len(rest) > 0 {
			args := a.Args
			args.Files = rest
			if err := e.store.Actions().SetArgs(ctx, a.ID, args); err != nil {
				slog.Warn("chat: held commit of a dropped diff", "action", a.ID, "err", err)
			}
			continue
		}
		a.Status, a.DecidedBy, a.DecidedAt = "rejected", by, &at
		a.Detail = "Diff đi kèm bị từ chối: không còn gì để commit" // i18n-ignore
		if status == "failed" {
			first, _, _ := strings.Cut(detail, "\n")
			a.Status, a.Detail = "failed", "Diff đi kèm không gộp được: "+first // i18n-ignore
		}
		if err := e.store.Actions().Update(ctx, a); err != nil {
			slog.Warn("chat: held commit of a dropped diff", "action", a.ID, "err", err)
		}
	}
}

// carryDeferred moves the commits held for the diffs a new one replaced
// (ADR-189, answers from) onto the new diff's answer (to): they come after it
// in the chat's order and commit its files too, instead of waiting before it
// (which blocked the new diff, then ran first and failed, or never ran). When
// the new answer asked a commit of its own, that one stands and the old ones
// are dropped: one commit, not a second failing "nothing to commit".
func (e *Engine) carryDeferred(ctx context.Context, conversationID string, from []string, to string, files []string) {
	if len(from) == 0 || to == "" {
		return
	}
	acts, err := e.store.Actions().List(ctx, conversationID, "", "")
	if err != nil {
		return
	}
	held := func(i int) bool {
		a := acts[i]
		return a.Kind == "git_commit" && a.Args.AfterDiff && (a.Status == "pending" || a.Status == "queued")
	}
	newer := false
	for i, a := range acts {
		if held(i) && a.MessageID == to {
			newer = true
		}
	}
	now := time.Now().UTC()
	for i, a := range acts {
		if !held(i) || !slices.Contains(from, a.MessageID) {
			continue
		}
		if newer {
			a.Status, a.Detail, a.DecidedBy, a.DecidedAt = "rejected", "Thay bằng commit của diff mới hơn", "office", &now
		} else {
			args := a.Args
			for _, f := range files {
				if !slices.Contains(args.Files, f) {
					args.Files = append(args.Files, f)
				}
			}
			if err := e.store.Actions().SetArgs(ctx, a.ID, args); err != nil {
				slog.Warn("chat: carry deferred commit", "action", a.ID, "err", err)
				continue
			}
			a.MessageID = to
		}
		if err := e.store.Actions().Update(ctx, a); err != nil {
			slog.Warn("chat: carry deferred commit", "action", a.ID, "err", err)
		}
	}
}
