package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

// erroringCostJobRepo embeds JobRepo so only CostSince needs a stub; every
// other method panics if called, which none of these tests should do.
type erroringCostJobRepo struct {
	JobRepo
	err error
}

func (r erroringCostJobRepo) CostSince(ctx context.Context, origin, originID string, since time.Time) (float64, error) {
	return 0, r.err
}

func TestOverDailyCapFailsClosedOnCostSinceError(t *testing.T) {
	a := Automation{ID: "aut_1", Limits: AutomationLimits{DailyCostUSD: 5}}
	jobs := erroringCostJobRepo{err: errors.New("database is locked")}

	_, over, err := a.OverDailyCap(context.Background(), jobs, time.Now())
	if !over {
		t.Fatalf("OverDailyCap() over = false on CostSince error, want true (fail closed)")
	}
	if err == nil {
		t.Fatalf("OverDailyCap() err = nil on CostSince error, want it surfaced")
	}
}

func TestOverDailyCapUsesSpentWhenNoError(t *testing.T) {
	a := Automation{ID: "aut_1", Limits: AutomationLimits{DailyCostUSD: 5}}
	jobs := erroringCostJobRepo{err: nil}

	_, over, err := a.OverDailyCap(context.Background(), jobs, time.Now())
	if over {
		t.Fatalf("OverDailyCap() over = true with spent 0 and no error, want false")
	}
	if err != nil {
		t.Fatalf("OverDailyCap() err = %v, want nil", err)
	}
}
