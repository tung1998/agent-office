package chat

import (
	"context"
	"errors"
	"time"

	"github.com/tung1998/agent-office/internal/errdigest"
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
