package chat_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// fakeClaude writes a claude CLI that logs each call to log; ok answers
// "từ <name>", otherwise it fails like a hit limit before doing anything.
func fakeClaude(t *testing.T, name string, ok bool) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "claude"), filepath.Join(dir, "calls")
	out := `echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Claude AI usage limit reached","usage":{"input_tokens":0,"output_tokens":0}}'`
	if ok {
		out = `echo '{"type":"result","subtype":"success","is_error":false,"result":"từ ` + name + `","usage":{"input_tokens":3,"output_tokens":2}}'`
	}
	os.WriteFile(bin, []byte(`#!/bin/sh
case "$*" in auth*) echo '{"loggedIn":true}'; exit 0;; esac
cat >/dev/null
echo x >> `+log+`
`+out+`
`), 0o755)
	return bin, log
}

func calls(log string) int {
	b, _ := os.ReadFile(log)
	return strings.Count(string(b), "x")
}

// fallbackFixture: the lead runs on A (the default) and falls back to B.
func fallbackFixture(t *testing.T, aOK bool) (fixture, storage.Provider, string, string) {
	binA, logA := fakeClaude(t, "A", aOK)
	binB, logB := fakeClaude(t, "B", true)
	var a, b storage.Provider
	f := setup(t, func(provs *provider.Service) storage.Provider {
		a, _ = provs.Create(context.Background(), provider.Input{Name: "A", Kind: storage.ProviderClaudeCLI, BaseURL: binA})
		b, _ = provs.Create(context.Background(), provider.Input{Name: "B", Kind: storage.ProviderClaudeCLI, BaseURL: binB})
		return a
	})
	ctx := context.Background()
	m, _ := f.st.OrgModels().GetForRepo(ctx, f.project.ID)
	agents, _ := f.st.Agents().List(ctx, m.ID)
	for _, ag := range agents {
		ag.FallbackProviderIDs = []string{b.ID}
		if err := f.st.Agents().Update(ctx, ag); err != nil {
			t.Fatal(err)
		}
	}
	return f, a, logA, logB
}

func answer(t *testing.T, f fixture) (string, []chat.Event) {
	t.Helper()
	ctx := context.Background()
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	turn, _, err := f.engine.Send(ctx, conv.ID, "chào", nil)
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, turn)
	last := events[len(events)-1]
	if last.Type != "done" {
		t.Fatalf("last event = %+v", last)
	}
	return last.Message.Content, events
}

// A connection that fails before doing anything hands the turn to the next one.
func TestFallsBackToNextConnection(t *testing.T) {
	f, _, logA, logB := fallbackFixture(t, false)
	got, events := answer(t, f)
	if got != "từ B" || calls(logA) != 1 || calls(logB) != 1 {
		t.Fatalf("answer = %q, calls A=%d B=%d", got, calls(logA), calls(logB))
	}
	switched := false
	for _, e := range events {
		if e.Type == "status" && strings.Contains(e.Text, "chuyển sang B") {
			switched = true
		}
	}
	if !switched {
		t.Fatalf("no switch status: %+v", events)
	}
	runs, _ := f.st.Runs().List(context.Background(), storage.RunFilter{Limit: 5})
	if len(runs) != 2 || runs[0].ProviderName != "B" || runs[1].Status != "error" {
		t.Fatalf("runs = %+v", runs)
	}
}

// The own connection answering: the fallback is never called.
func TestOwnConnectionFirst(t *testing.T) {
	f, _, logA, logB := fallbackFixture(t, true)
	if got, _ := answer(t, f); got != "từ A" || calls(logB) != 0 || calls(logA) != 1 {
		t.Fatalf("answer = %q, calls A=%d B=%d", got, calls(logA), calls(logB))
	}
}

// One known to be over its limit is tried last.
func TestOverLimitConnectionGoesLast(t *testing.T) {
	f, a, logA, logB := fallbackFixture(t, true)
	f.st.Settings().Set(context.Background(), chat.LimitsKey(a.ID), chat.Limits{Status: "rejected", UpdatedAt: time.Now().UTC(),
		Windows: map[string]chat.LimitWindow{"five_hour": {Utilization: 1, ResetsAt: time.Now().Add(time.Hour)}}})
	if got, _ := answer(t, f); got != "từ B" || calls(logA) != 0 {
		t.Fatalf("answer = %q, calls A=%d B=%d", got, calls(logA), calls(logB))
	}
}
