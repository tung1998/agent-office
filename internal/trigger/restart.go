package trigger

import (
	"context"
	"log/slog"
	"time"

	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/storage"
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
		back, err := st.Jobs().Requeue(ctx, j.ID, "restart", storage.MaxJobResumes, now)
		if err != nil { // put back too often (it keeps bringing office down), or not cut
			slog.Info("trigger: cut run not put back", "job", j.ID, "resumes", j.Resumes, "err", err)
			continue
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

// resumedPrompt is what a run going on in its chat is sent.
func resumedPrompt() string { return prompts.Render("trigger/resumed", nil) }

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
