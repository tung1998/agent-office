package chat_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// QuotaStop: the agent's only connection reported its quota full until a
// reset: out until then; a reset passed or no report: free; a rejected
// report with no window: out for the cooldown (ADR-268).
func TestQuotaStop(t *testing.T) {
	var prov storage.Provider
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, err := provs.Create(context.Background(), provider.Input{Name: "Codex", Kind: storage.ProviderClaudeCLI, BaseURL: "/nonexistent/claude"})
		if err != nil {
			t.Fatal(err)
		}
		prov = p
		return p
	})
	ctx := context.Background()
	agents, _ := f.st.Agents().List(ctx, f.project.ID)
	if len(agents) == 0 {
		t.Fatal("no agent")
	}
	ag := agents[0]
	if _, out := f.engine.QuotaStop(ctx, ag); out {
		t.Fatal("no report: must be free")
	}
	set := func(l chat.Limits) {
		if err := f.st.Settings().Set(ctx, chat.LimitsKey(prov.ID), l); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	reset := now.Add(5 * 24 * time.Hour).Truncate(time.Second)
	set(chat.Limits{Status: "rejected", UpdatedAt: now, Windows: map[string]chat.LimitWindow{"quota": {Utilization: 1, ResetsAt: reset}}})
	if until, out := f.engine.QuotaStop(ctx, ag); !out || !until.Equal(reset) {
		t.Fatalf("full window: until=%v out=%v, want %v", until, out, reset)
	}
	set(chat.Limits{Status: "rejected", UpdatedAt: now.Add(-6 * 24 * time.Hour), Windows: map[string]chat.LimitWindow{"quota": {Utilization: 1, ResetsAt: now.Add(-time.Minute)}}})
	if _, out := f.engine.QuotaStop(ctx, ag); out {
		t.Fatal("a window already reset holds the connection")
	}
	set(chat.Limits{Status: "rejected", UpdatedAt: now})
	if until, out := f.engine.QuotaStop(ctx, ag); !out || until.Sub(now) > chat.RejectedCooldown+time.Second || until.Before(now) {
		t.Fatalf("rejected, no window: until=%v out=%v", until, out)
	}
	set(chat.Limits{Status: "allowed", UpdatedAt: now, Windows: map[string]chat.LimitWindow{"five_hour": {Utilization: 0.4, ResetsAt: now.Add(time.Hour)}}})
	if _, out := f.engine.QuotaStop(ctx, ag); out {
		t.Fatal("a connection with quota left reads as out")
	}
}
