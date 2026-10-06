package usage_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/usage"
)

func newStore(t *testing.T) storage.Store {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestEstimate(t *testing.T) {
	c, ok := usage.Estimate("claude-sonnet-5", 1_000_000, 100_000, nil)
	if !ok || c != 3.0 { // 2 + 0.1*10
		t.Fatalf("sonnet = %v %v", c, ok)
	}
	if _, ok := usage.Estimate("anthropic.claude-haiku-4-5-20260101", 1, 1, nil); !ok {
		t.Fatal("prefix/date suffix not normalised")
	}
	if _, ok := usage.Estimate("gpt-5", 1, 1, nil); ok {
		t.Fatal("unknown model must not be priced")
	}
	if c, ok := usage.Estimate("gpt-5", 1_000_000, 0, map[string]usage.Price{"gpt-5": {Input: 1.25, Output: 10}}); !ok || c != 1.25 {
		t.Fatalf("override = %v %v", c, ok)
	}
}

func TestRecordBudgetSummary(t *testing.T) {
	ctx := actor.With(context.Background(), "human:a@b.c")
	st := newStore(t)
	loc := time.FixedZone("ICT", 7*3600)
	svc := usage.New(st, loc)
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, loc)
	svc.SetClock(func() time.Time { return now })

	prov := storage.Provider{ID: "", Name: "Claude"}
	repo, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: "/shop"})

	r1, err := svc.Record(ctx, usage.Meta{Kind: "chat", ProjectID: repo.ID}, prov, "claude-sonnet-5", llm.Result{Model: "claude-sonnet-5", InputTokens: 1_000_000, OutputTokens: 100_000}, nil)
	if err != nil || r1.CostSource != "estimate" || *r1.CostUSD != 3.0 || r1.Actor != "human:a@b.c" {
		t.Fatalf("estimate run = %+v, %v", r1, err)
	}
	r2, _ := svc.Record(ctx, usage.Meta{Kind: "chat"}, prov, "claude-opus-5-5", llm.Result{InputTokens: 10, OutputTokens: 5, CostUSD: 0.5}, nil)
	if r2.CostSource != "provider" || *r2.CostUSD != 0.5 {
		t.Fatalf("provider run = %+v", r2)
	}
	r3, _ := svc.Record(ctx, usage.Meta{Kind: "chat"}, prov, "gpt-5", llm.Result{InputTokens: 10, OutputTokens: 5}, nil)
	if r3.CostUSD != nil || r3.CostSource != "unknown" {
		t.Fatalf("unknown run = %+v", r3)
	}
	r4, _ := svc.Record(ctx, usage.Meta{Kind: "chat"}, prov, "claude-sonnet-5", llm.Result{}, errors.New("HTTP 500"))
	if r4.Status != "error" {
		t.Fatalf("error run = %+v", r4)
	}

	// no limit: allowed
	if err := svc.Check(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	// project limit reached (3.0 spent on shop)
	svc.SaveSettings(ctx, usage.Settings{ProjectLimits: map[string]float64{repo.ID: 2}})
	var be *usage.BudgetError
	if err := svc.Check(ctx, repo.ID); !errors.As(err, &be) || be.Scope != "project shop" {
		t.Fatalf("project budget err = %v", err)
	}
	if err := svc.Check(ctx, ""); err != nil {
		t.Fatalf("other work must not be blocked by a project limit: %v", err)
	}
	// office limit (3.5 spent today)
	svc.SaveSettings(ctx, usage.Settings{DailyLimitUSD: 3.5})
	if err := svc.Check(ctx, ""); !errors.As(err, &be) || be.Scope != "office" {
		t.Fatalf("office budget err = %v", err)
	}
	// a new day resets the budget
	now = now.Add(24 * time.Hour)
	if err := svc.Check(ctx, ""); err != nil {
		t.Fatalf("new day: %v", err)
	}

	sum, err := svc.Summarize(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.ByDay) != 7 || sum.Today != 0 || sum.Period != 3.5 || sum.ByDay[5].CostUSD != 3.5 || sum.ByDay[5].UnknownCost != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if len(sum.ByModel) != 3 || sum.ByProject[0].Label != "shop" {
		t.Fatalf("breakdowns = %+v / %+v", sum.ByModel, sum.ByProject)
	}
}

func TestProviderCallBlockedByBudget(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	box, _ := secrets.Load(filepath.Join(t.TempDir(), "k"))
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"model":"claude-haiku-4-5","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1000000,"output_tokens":0}}`))
	}))
	defer srv.Close()
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	key := "sk-ant-test-key-0000"
	p, _ := provs.Create(ctx, provider.Input{Name: "C", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})

	u.SaveSettings(ctx, usage.Settings{DailyLimitUSD: 0.5})
	if _, err := provs.Call(ctx, p, llm.Request{Model: "claude-haiku-4-5", Prompt: "x"}, usage.Meta{Kind: "chat"}); err != nil {
		t.Fatal(err)
	}
	var be *usage.BudgetError
	if _, err := provs.Call(ctx, p, llm.Request{Model: "claude-haiku-4-5", Prompt: "x"}, usage.Meta{Kind: "chat"}); !errors.As(err, &be) {
		t.Fatalf("second call err = %v", err)
	}
	if calls != 1 {
		t.Fatalf("blocked call must not reach the provider, calls=%d", calls)
	}
	runs, _ := st.Runs().List(ctx, storage.RunFilter{Limit: 10})
	if len(runs) != 2 || runs[0].Status != "blocked" {
		t.Fatalf("runs = %+v", runs)
	}
}
