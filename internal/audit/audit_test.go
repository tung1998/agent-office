package audit_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/audit"
)

func TestSnapshotRedactsSecrets(t *testing.T) {
	type cfg struct {
		SecretHash string
		Cron       string
	}
	type auto struct {
		Name        string
		Config      cfg
		InputTokens int            `json:"input_tokens"`
		APIKey      string         `json:"api_key"`
		Empty       string         `json:"token"`
		Headers     map[string]any `json:"headers"`
		HasAPIKey   bool           `json:"has_api_key"`
	}
	got := audit.Snapshot(auto{Name: "n", Config: cfg{SecretHash: "abc", Cron: "* * * * *"}, InputTokens: 5, APIKey: "sk-1", HasAPIKey: true,
		Headers: map[string]any{"Authorization": "Bearer x", "x_token": "y"}})
	if got["Name"] != "n" || got["input_tokens"] != float64(5) || got["has_api_key"] != true {
		t.Fatalf("plain fields changed: %v", got)
	}
	if got["api_key"] != "***" || got["Config"].(map[string]any)["SecretHash"] != "***" || got["Config"].(map[string]any)["Cron"] != "* * * * *" {
		t.Fatalf("secrets not redacted: %v", got)
	}
	if got["token"] != "" {
		t.Fatalf("empty secret should stay empty (not set), got %v", got["token"])
	}
	h := got["headers"].(map[string]any)
	if h["Authorization"] != "***" || h["x_token"] != "***" {
		t.Fatalf("nested map secrets: %v", h)
	}
	if audit.Snapshot(nil) != nil {
		t.Fatal("nil snapshot")
	}
}

func TestEntryFromWho(t *testing.T) {
	ctx := audit.With(context.Background(), audit.Who{Kind: "agent", Name: "Lead", ApprovedBy: "a@x.io", Via: "chat", ConversationID: "cnv_1", JobID: "job_1", ActionID: "act_1"})
	e := audit.Entry(ctx, audit.Change{Action: "automation.update", ResourceID: "aut_1", ProjectID: "prj_1",
		Before: map[string]any{"name": "a"}, After: map[string]any{"name": "b"}})
	if e.ActorKind != "agent" || e.ActorName != "Lead" || e.ApprovedBy != "a@x.io" || e.Via != "chat" || e.Actor != "agent:Lead" {
		t.Fatalf("who: %+v", e)
	}
	if e.Resource != "automation" || e.Target != "aut_1" || e.ResourceID != "aut_1" || e.ConversationID != "cnv_1" || e.JobID != "job_1" || e.ActionID != "act_1" || !e.OK {
		t.Fatalf("change: %+v", e)
	}
	if e.Before["name"] != "a" || e.After["name"] != "b" {
		t.Fatalf("snapshots: %+v", e)
	}
	failed := audit.Entry(ctx, audit.Change{Action: "automation.update", Err: errors.New("boom")})
	if failed.OK || failed.Detail["error"] != "boom" {
		t.Fatalf("failed change: %+v", failed)
	}
}

func TestEntryFallsBackToActorString(t *testing.T) {
	cases := map[string][2]string{
		"human:a@x.io": {"human", "a@x.io"},
		"auto:Nightly": {"automation", "Nightly"},
		"user:usr_1":   {"human", "usr_1"},
		"monitor:api":  {"system", "monitor:api"},
		"":             {"system", "system"},
	}
	for in, want := range cases {
		ctx := context.Background()
		if in != "" {
			ctx = actor.With(ctx, in)
		}
		e := audit.Entry(ctx, audit.Change{Action: "x.y"})
		if e.ActorKind != want[0] || e.ActorName != want[1] {
			t.Errorf("%q → %s/%s, want %v", in, e.ActorKind, e.ActorName, want)
		}
	}
}

// Review minors: a rotated secret shows it changed; more secret names; Detail
// is redacted too.
func TestSecretChangesAndNames(t *testing.T) {
	ctx := context.Background()
	e := audit.Entry(ctx, audit.Change{Action: "provider.update",
		Before: map[string]any{"api_key": "sk-1", "token": "same", "name": "a"},
		After:  map[string]any{"api_key": "sk-2", "token": "same", "name": "a"},
		Detail: map[string]any{"password": "p", "note": "ok"}})
	if e.Before["api_key"] == e.After["api_key"] || !strings.HasPrefix(e.After["api_key"].(string), "***") {
		t.Fatalf("rotated secret: %v → %v", e.Before["api_key"], e.After["api_key"])
	}
	if e.Before["token"] != e.After["token"] {
		t.Fatalf("an unchanged secret looks changed: %v → %v", e.Before["token"], e.After["token"])
	}
	if e.Detail["password"] != "***" || e.Detail["note"] != "ok" {
		t.Fatalf("detail = %v", e.Detail)
	}
	s := audit.Snapshot(map[string]any{"private_key": "k", "access_key": "k", "credentials": "c", "dsn": "postgres://u:p@h", "heartbeat_path": "/hb/tok123", "commit_hash": "abc"})
	for _, k := range []string{"private_key", "access_key", "credentials", "dsn", "heartbeat_path"} {
		if s[k] != "***" {
			t.Errorf("%s not redacted: %v", k, s[k])
		}
	}
}
