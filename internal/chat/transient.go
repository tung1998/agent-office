package chat

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/errdigest"
	"github.com/tung1998/agent-office/internal/prompts"
	"github.com/tung1998/agent-office/internal/usage"
)

// ErrTransient marks a run that died on a passing failure of the AI service
// (an overloaded API, a 5xx, a dropped stream), not on anything in the run:
// the same turn, run again a little later, most likely goes through (ADR-260).
var ErrTransient = errors.New("lỗi API tạm thời")

// TransientCode is the error code of a job that ended on ErrTransient.
const TransientCode = "api_transient"

// Transient: err is a passing failure of the AI service, worth running the
// turn again later. Never one the person must act on (signed out, a broken
// CLI, out of tokens or budget) nor a stop.
func Transient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || connectionBroken(err) || OutOfTokens(err) {
		return false
	}
	var be *usage.BudgetError
	if errors.As(err, &be) {
		return false
	}
	return errors.Is(err, ErrTransient) || errdigest.TransientText(err.Error())
}

// TransientRetryDelay is how long a CLI that died on a passing API failure
// is left before its session goes on (tests make it short).
var TransientRetryDelay = time.Minute

// transientError wraps msg as a passing failure: errors.Is(err, ErrTransient).
type transientError struct{ msg string }

func (e transientError) Error() string        { return e.msg }
func (e transientError) Is(target error) bool { return target == ErrTransient }

// passingCut is the passing API failure a run died or stopped writing on,
// in a session it can go on in; nil when there is none (ADR-261).
func passingCut(ctx context.Context, res RunResult, err error) error {
	if ctx.Err() != nil || res.SessionID == "" {
		return nil
	}
	if err != nil {
		if res.Text == "" && Transient(err) {
			return err
		}
		return nil
	}
	if res.cutBy != "" {
		if cut := errors.New(res.cutBy); Transient(cut) {
			return cut
		}
	}
	return nil
}

// goOnInSession runs once more, in its session, a turn a Codex or Gemini CLI
// died or stopped writing on a passing API failure, a while later, as Claude
// does (ADR-260/261): it is told to go on from where it stopped, and what it
// writes follows what it wrote. Still failing, the first run stands: its
// error, to be put back later as a whole, or the part it wrote, marked cut.
func goOnInSession(ctx context.Context, req RunRequest, emit func(Event), first RunResult, firstErr, why error, run func(RunRequest, func(Event)) (RunResult, error)) (RunResult, error) {
	select {
	case <-ctx.Done():
		return first, firstErr
	case <-time.After(TransientRetryDelay):
	}
	again := req
	again.SessionID = first.SessionID
	again.Prompt = prompts.Render("chat/api-resumed", map[string]any{"Error": truncate(why.Error(), 300)}) + "\n\n" + req.Prompt
	sep := first.Text != ""
	res, err := run(again, func(ev Event) {
		if ev.Type == "text" && sep { // what it writes now follows the part it wrote
			sep = false
			emit(Event{Type: "text", Text: "\n\n"})
		}
		emit(ev)
	})
	if err != nil || (res.Text == "" && first.Text != "") {
		return first, firstErr
	}
	res.Text = strings.TrimSpace(strings.Join(slices.DeleteFunc([]string{first.Text, res.Text}, func(s string) bool { return s == "" }), "\n\n"))
	res.Tools = append(slices.Clip(first.Tools), res.Tools...)
	res.Usage.InputTokens += first.Usage.InputTokens
	res.Usage.OutputTokens += first.Usage.OutputTokens
	res.Usage.DurationMS += first.Usage.DurationMS
	res.SessionID = cmp.Or(res.SessionID, first.SessionID)
	if res.Limits == nil {
		res.Limits = first.Limits
	}
	res.Notice = cmp.Or(res.Notice, "⚠ Lỗi API tạm thời giữa chừng ("+truncate(why.Error(), 120)+"), đã tự chạy tiếp")
	return res, nil
}
