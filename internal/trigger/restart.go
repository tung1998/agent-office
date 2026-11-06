package trigger

import (
	"context"
	"log/slog"
	"time"

	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

// RequeueCut puts back the automations' runs office restarted under
// (ADR-257): jobs is what was running when it went down (read before
// FailRunning ended them as "restart"). A run whose automation resumes on a
// restart (Automation.ResumesOnRestart) and that was put back fewer than
// storage.MaxJobResumes times is pending again, due now: the runner runs it
// as before, with the rights it was given when it was made (never more). An
// agent's run goes on in the chat it was cut in. A step of a run (its
// ParentJobID) goes on with its run; a workflow's caller waits for the run
// itself (ADR-256), so the caller passes jobs without them. It returns the
// jobs put back.
func RequeueCut(ctx context.Context, st storage.Store, jobs []storage.Job, now time.Time) []storage.Job {
	var out []storage.Job
	for _, j := range jobs {
		if j.Origin != "automation" || j.ParentJobID != "" || (j.Kind != "chat_turn" && j.Kind != "script") {
			continue
		}
		a, err := st.Automations().Get(ctx, j.OriginID)
		if err != nil || !a.ResumesOnRestart() {
			continue
		}
		// its restarts alone count against the cap: the times a passing API
		// failure put it back (RetryN) have a count of their own (ADR-261)
		back, err := st.Jobs().Requeue(ctx, j.ID, "restart", storage.MaxJobResumes+j.RetryN, now)
		if err != nil { // put back too often (it keeps bringing office down), or not cut
			slog.Info("trigger: cut run not put back", "job", j.ID, "resumes", j.Resumes, "err", err)
			continue
		}
		if back.RetryAt != nil { // put back last for a restart, not a passing failure
			_ = st.Jobs().SetRetry(ctx, back.ID, nil, back.RetryN)
			back.RetryAt = nil
		}
		out = append(out, back)
	}
	return out
}

// resumeIn is where a run put back after a restart goes on (ADR-257): an
// agent's chat run in the chat it was cut in, told so, not asked again from
// the start. "" = it runs as a new run (a script, a workflow, a run cut
// before it had a chat).
func resumeIn(a storage.Automation, j storage.Job) string {
	if j.Resumes == 0 || j.ConversationID == "" || j.Kind != "chat_turn" || a.Action != "chat" {
		return ""
	}
	return j.ConversationID
}

// resumedPrompt is what a run going on in its chat is sent: put back last for
// a passing API failure (RetryAt marks it while it waits) or for a restart.
func resumedPrompt(j storage.Job) string {
	if j.RetryAt != nil {
		return prompts.Render("trigger/api-resumed", nil)
	}
	return prompts.Render("trigger/resumed", nil)
}

// TransientCode is the error code of a run that died on a passing failure of
// the AI service (chat.TransientCode: the chat engine ends a turn so).
const TransientCode = "api_transient"

// TransientWaits are the waits before an automation's run cut by a passing
// API failure goes on, by how often it was put back (tests make them short).
var TransientWaits = []time.Duration{2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

// requeueTransient puts back an agent's run that died on a passing failure of
// the AI service (ADR-260): pending again a while later, to go on in the chat
// it ran in, like a run cut by a restart, at most storage.MaxJobResumes times
// all told and as the settings allow. false: it stays failed.
func (r *Runner) requeueTransient(ctx context.Context, a storage.Automation, j storage.Job, conv string) bool {
	if j.ParentJobID != "" || j.Kind != "chat_turn" || a.Action != "chat" || IsChannel(j.Trigger) {
		return false // a bot's message is answered now, its error included
	}
	cur, err := r.store.Jobs().Get(ctx, j.ID)
	if err != nil || cur.Status != "failed" || cur.ErrorCode != TransientCode {
		return false
	}
	st, _ := usage.New(r.store, nil).Settings(ctx)
	// its own count (RetryN), not its restarts': a run cut by a restart once
	// still has all its tries, starting from the shortest wait (ADR-261)
	limit := min(st.Retries(), storage.MaxJobResumes)
	if cur.ConversationID == "" && conv != "" { // where it goes on
		cur.ConversationID = conv
		if r.store.Jobs().Update(ctx, cur) != nil {
			return false
		}
	}
	if cur.ConversationID == "" || limit == 0 || cur.RetryN >= limit {
		return false // put back too often: it fails as before
	}
	wait := TransientWaits[min(cur.RetryN, len(TransientWaits)-1)]
	at := r.now().UTC().Add(wait)
	back, err := r.store.Jobs().Requeue(ctx, j.ID, TransientCode, cur.Resumes+1, at)
	if err != nil {
		return false
	}
	// retry_at marks it put back for a passing failure until it runs
	// (resumedPrompt); retry_n counts these puts alone
	_ = r.store.Jobs().SetRetry(ctx, back.ID, &at, cur.RetryN+1)
	slog.Info("trigger: run put back after a passing API failure", "job", j.ID, "wait", wait, "tries", cur.RetryN+1)
	return true
}

// CutLeft are the bot runs among cut (what was running when office went
// down) that RequeueCut did not put back: the person who asked in the bot
// chat is told, not left waiting for an answer that never comes (ADR-258).
// A step of a run is left out: its run carries the message.
func CutLeft(cut, back []storage.Job) []storage.Job {
	put := map[string]bool{}
	for _, j := range back {
		put[j.ID] = true
	}
	var out []storage.Job
	for _, j := range cut {
		if j.Origin == "automation" && j.ParentJobID == "" && IsChannel(j.Trigger) && !put[j.ID] {
			out = append(out, j)
		}
	}
	return out
}
