package chat

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/usage"
)

// erroringCostStore wraps a real storage.Store but makes Jobs().CostSince
// fail, so capReached can be tested against an unreadable spend, not just a
// missing or zero one.
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

func TestCapReachedBlocksOnCostSinceError(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, err := sqlite.Open(filepath.Join(tmp, "o.db"))
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
	root, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: a.ID, Trigger: "webhook", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}

	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	wrapped := erroringCostStore{Store: st, err: errors.New("database is locked")}
	e := NewEngine(wrapped, provs, usage.New(wrapped, time.UTC))

	err = e.capReached(ctx, root.ID, a.ID)
	var be *usage.BudgetError
	if !errors.As(err, &be) {
		t.Fatalf("capReached() = %v, want a *usage.BudgetError on a CostSince error (fail closed)", err)
	}
}

func TestCapReachedAllowsWhenUnderCap(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	st, err := sqlite.Open(filepath.Join(tmp, "o.db"))
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
	root, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p.ID, Kind: "chat_turn", Origin: "automation", OriginID: a.ID, Trigger: "webhook", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}

	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	e := NewEngine(st, provs, usage.New(st, time.UTC))

	if err := e.capReached(ctx, root.ID, a.ID); err != nil {
		t.Fatalf("capReached() = %v, want nil with no spend and no error", err)
	}
}
