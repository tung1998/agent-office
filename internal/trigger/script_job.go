package trigger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// runScriptJob runs an automation's script (no AI), stores what it printed,
// and hands the result to an agent when the automation says so (ADR-041).
// A script that exits non-zero reports a result, not a breakdown: only a
// script that cannot run or runs out of time counts toward auto-disable.
func (r *Runner) runScriptJob(ctx context.Context, a storage.Automation, j storage.Job, now time.Time, answer func(string, error, bool)) {
	dir, _ := os.UserHomeDir()
	if p, err := r.store.Repos().Get(ctx, a.ProjectID); err == nil && p.Path != "" {
		dir = p.Path
	}
	env := []string{"OFFICE_PAYLOAD=" + j.Payload, "OFFICE_TRIGGER=" + j.Trigger, "OFFICE_JOB_ID=" + j.ID, "OFFICE_AUTOMATION=" + a.Name}
	out, code, timedOut, runErr := RunScript(ctx, dir, a.Script, env, j.Payload)
	_ = r.store.Jobs().SetOutput(ctx, j.ID, out, code)
	status, errCode, msg := "done", "", ""
	switch {
	case runErr != nil:
		status, errCode, msg = "failed", "script_error", runErr.Error()
	case timedOut:
		status, errCode, msg = "failed", "timeout", "script chạy quá thời gian cho phép"
	case code != 0:
		status, errCode, msg = "failed", "script_error", fmt.Sprintf("script thoát với mã %d", code)
	}
	_, _ = r.store.Jobs().Finish(ctx, j.ID, status, errCode, msg, r.now().UTC())

	var failed error
	if status == "failed" {
		failed = errors.New(msg)
	}
	answer(withoutSignals(out), failed, true) // what it printed is the answer

	if a, err := r.store.Automations().Get(ctx, a.ID); err == nil {
		t := r.now().UTC()
		a.LastRunAt = &t
		if runErr != nil || timedOut {
			a.Failures++
			limit := a.Limits.DisableAfterFailures
			if limit <= 0 {
				limit = 5
			}
			if a.Failures >= limit {
				a.Enabled, a.DisabledCode, a.DisabledReason = false, "failures", firstNonEmpty(msg, "script lỗi")
			}
		} else {
			a.Failures = 0
		}
		_ = r.store.Automations().Update(ctx, a)
	}
}

// withoutSignals is a script's output without its @@agent lines.
func withoutSignals(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "@@agent:") {
			keep = append(keep, l)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}
