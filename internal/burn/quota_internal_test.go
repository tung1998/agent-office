package burn

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/usage"
	"github.com/tung1998/agent-office/internal/worktree"
)

// A Burn waits for quota as the chat reads it (ADR-269): only when every
// connection its agent may run on is out (its fallbacks too), and until the
// first of them resets. It used to read the agent's own connection alone, so
// its Claude out of quota paused it though a fallback Codex was free.
func TestBurnLimitHitReadsTheWholeChain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmp := t.TempDir()
	st, err := sqlite.Open(filepath.Join(tmp, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	main, err := provs.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	backup, err := provs.Create(ctx, provider.Input{Name: "Codex", Kind: storage.ProviderClaudeCLI, BaseURL: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: tmp})
	ag, err := st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "a", Name: "A", ModelTier: "balanced",
		ProviderID: main.ID, FallbackProviderIDs: []string{backup.ID}})
	if err != nil {
		t.Fatal(err)
	}
	engine := chat.NewEngine(st, provs, usage.New(st, time.UTC))
	s := New(st, engine, worktree.New(filepath.Join(tmp, "trees")))
	out := func(id string, reset time.Time) {
		_ = st.Settings().Set(ctx, chat.LimitsKey(id), chat.Limits{Status: "rejected", UpdatedAt: time.Now(),
			Windows: map[string]chat.LimitWindow{"quota": {Utilization: 1, ResetsAt: reset}}})
	}
	soon, later := time.Now().Add(time.Hour).Truncate(time.Second), time.Now().Add(5*time.Hour).Truncate(time.Second)
	out(main.ID, later)
	if until, hit := s.limitHit(ctx, ag.ID); hit {
		t.Fatalf("its fallback is free, yet the Burn waits until %v", until)
	}
	out(backup.ID, soon)
	until, hit := s.limitHit(ctx, ag.ID)
	if !hit || !until.Equal(soon) {
		t.Fatalf("all out: hit=%v until=%v, want %v (the first to reset)", hit, until, soon)
	}
}
