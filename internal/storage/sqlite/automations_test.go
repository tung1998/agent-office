package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Update (internal/api/automations.go's updateAutomation) is CAS'd on
// updated_at, so a Runner safety-disable (Disable) landing between the
// admin's re-fetch and its own Update loses nothing: the admin's stale
// Enabled=true write is rejected instead of silently reviving a cost-capped
// or schedule-ended automation.
func TestUpdateConflictsWithConcurrentDisable(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	a, err := st.Automations().Create(ctx, storage.Automation{
		ProjectID: p.ID, Name: "a", Source: "schedule", Action: "chat", AgentID: "ag", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// updateAutomation's re-fetch under lockAutomations (automations.go:469).
	admin := a
	// the Runner hits the daily cost cap (or EndsAt) and disables it (runner.go:415/126).
	if err := st.Automations().Disable(ctx, a.ID, "daily_cost", "chạm trần chi phí trong ngày", nil); err != nil {
		t.Fatal(err)
	}
	// the admin's save, built from its now-stale snapshot (still Enabled=true).
	admin.Name = "a edited"
	if err := st.Automations().Update(ctx, admin); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("stale admin save over a concurrent Disable = %v, want ErrConflict", err)
	}
	got, err := st.Automations().Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.DisabledCode != "daily_cost" {
		t.Fatalf("admin save resurrected the cost-capped automation: %+v", got)
	}
}

// UpdateSpec must keep a conversation RecordRun just recorded, and a secret
// UpdateSecretHash just rotated, even though both are read through the same
// config column UpdateSpec writes.
func TestUpdateSpecKeepsConfigSecretsSetConcurrently(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	a, err := st.Automations().Create(ctx, storage.Automation{
		ProjectID: p.ID, Name: "a", Source: "webhook", Action: "chat", AgentID: "ag", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := a
	now := time.Now().UTC()
	if err := st.Automations().RecordRun(ctx, a.ID, now, "ok", "", 5, "conv1"); err != nil {
		t.Fatal(err)
	}
	if err := st.Automations().UpdateSecretHash(ctx, a.ID, "newhash"); err != nil {
		t.Fatal(err)
	}
	snapshot.Name = "a edited"
	if err := st.Automations().UpdateSpec(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	got, err := st.Automations().Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.ConversationID != "conv1" {
		t.Fatalf("UpdateSpec lost the conversation RecordRun kept: %+v", got.Config)
	}
	if got.Config.SecretHash != "newhash" {
		t.Fatalf("UpdateSpec lost the secret hash UpdateSecretHash rotated: %+v", got.Config)
	}
}

// UpdateSpec writes the limits a proposal changes (trigger.Spec.Apply sets
// them): a raised daily cost cap or run time must not be dropped.
func TestUpdateSpecWritesLimits(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	a, err := st.Automations().Create(ctx, storage.Automation{
		ProjectID: p.ID, Name: "a", Source: "schedule", Action: "chat", AgentID: "ag", Enabled: true,
		Limits: storage.AutomationLimits{DailyCostUSD: 15, MaxMinutes: 120},
	})
	if err != nil {
		t.Fatal(err)
	}
	a.Limits = storage.AutomationLimits{DailyCostUSD: 20, MaxMinutes: 240}
	if err := st.Automations().UpdateSpec(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := st.Automations().Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Limits.DailyCostUSD != 20 || got.Limits.MaxMinutes != 240 {
		t.Fatalf("UpdateSpec dropped the limits: %+v", got.Limits)
	}
}
