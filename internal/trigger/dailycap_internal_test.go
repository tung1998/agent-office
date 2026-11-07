package trigger

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// erroringCostStore wraps a real storage.Store but makes Jobs().CostSince
// fail, so reopenCapped must be tested against an unreadable spend, not just
// a missing or zero one.
type erroringCostStore struct {
	storage.Store
	err error
}

func (s erroringCostStore) Jobs() storage.JobRepo {
	return erroringCostJobRepo{JobRepo: s.Store.Jobs(), err: s.err}
}

type erroringCostJobRepo struct {
	storage.JobRepo
	err error
}

func (r erroringCostJobRepo) CostSince(ctx context.Context, origin, originID string, since time.Time) (float64, error) {
	return 0, r.err
}

type noopExec struct{}

func (noopExec) RunChat(ctx context.Context, projectID, agentID, conversationID, prompt, editMode string) (string, string, error) {
	return "", "", nil
}

func TestReopenCappedLeavesDisabledOnCostSinceError(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Automations().Create(ctx, storage.Automation{
		ProjectID: p.ID, Name: "a", Enabled: true, Source: "webhook", Action: "chat",
		Limits: storage.AutomationLimits{DailyCostUSD: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Automations().Disable(ctx, a.ID, DailyCapCode, "chạm trần", nil); err != nil {
		t.Fatal(err)
	}

	r := New(erroringCostStore{Store: st, err: errors.New("database is locked")}, noopExec{})
	r.Tick(ctx, time.Now())

	got, err := st.Automations().Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Fatalf("Tick() re-enabled the automation on a CostSince error, want it left disabled")
	}
}

func TestReopenCappedReenablesWhenUnderCap(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Automations().Create(ctx, storage.Automation{
		ProjectID: p.ID, Name: "a", Enabled: true, Source: "webhook", Action: "chat",
		Limits: storage.AutomationLimits{DailyCostUSD: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Automations().Disable(ctx, a.ID, DailyCapCode, "chạm trần", nil); err != nil {
		t.Fatal(err)
	}

	r := New(st, noopExec{})
	r.Tick(ctx, time.Now())

	got, err := st.Automations().Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled {
		t.Fatalf("Tick() left the automation disabled with no spend and no error, want it re-enabled")
	}
}
