package api_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// "Bỏ qua" a diff: rejected, and its agent is not run again about it (a
// plain reject is: the control).
func TestSkipPatchDoesNotResume(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	var resumed atomic.Int32
	e.chat.SetDecidedWait(10 * time.Millisecond)
	e.chat.SetOnBotDecided(func(context.Context, string, string, []string) { resumed.Add(1) })
	patch := func() storage.Patch {
		c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "bot", Purpose: "channel"})
		m, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Content: "diff"})
		p, err := e.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: c.ID, MessageID: m.ID, Diff: "x", Files: []string{"a.txt"}, Status: "pending"})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	skipped := patch()
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/patches/"+skipped.ID+"/reject", map[string]any{"skip": true}, nil); resp.StatusCode != 200 {
		t.Fatalf("skip = %d %v", resp.StatusCode, b)
	}
	if p, _ := e.st.Chat().GetPatch(ctx, skipped.ID); p.Status != "rejected" {
		t.Fatalf("skipped: %+v", p)
	}
	time.Sleep(150 * time.Millisecond)
	if n := resumed.Load(); n != 0 {
		t.Fatalf("skipped diff resumed its agent %d times", n)
	}
	rejected := patch()
	do(t, admin, "POST", e.srv.URL+"/api/patches/"+rejected.ID+"/reject", map[string]any{}, nil)
	deadline := time.Now().Add(2 * time.Second)
	for resumed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if resumed.Load() != 1 {
		t.Fatalf("rejected diff: resumed %d", resumed.Load())
	}
}

// "Bỏ qua tất cả": every action and diff waiting is rejected, counted, no
// agent run again; only an admin may.
func TestSkipAllProposals(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	var resumed atomic.Int32
	e.chat.SetDecidedWait(10 * time.Millisecond)
	e.chat.SetOnBotDecided(func(context.Context, string, string, []string) { resumed.Add(1) })
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "bot", Purpose: "channel"})
	m, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "assistant", Content: "diff"})
	p, _ := e.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: c.ID, MessageID: m.ID, Diff: "x", Files: []string{"a.txt"}, Status: "pending"})
	push, _ := e.st.Actions().Create(ctx, storage.Action{ProjectID: pid, ConversationID: c.ID, Kind: "git_push", Target: "main", ProposedBy: "Lead", Status: "pending"})
	cmd, _ := e.st.Actions().Create(ctx, storage.Action{ProjectID: pid, Kind: "run_command", Target: "go test ./...", ProposedBy: "Lead", Status: "pending"})

	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/proposals/skip-all", map[string]any{}, nil); resp.StatusCode != 403 {
		t.Fatalf("member skip-all = %d", resp.StatusCode)
	}
	// another project's: left alone
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/proposals/skip-all", map[string]any{"project_id": "nope"}, nil); resp.StatusCode != 200 || b["skipped"] != float64(0) {
		t.Fatalf("other project = %d %v", resp.StatusCode, b)
	}
	resp, b := do(t, admin, "POST", e.srv.URL+"/api/proposals/skip-all", map[string]any{}, nil)
	if resp.StatusCode != 200 || b["skipped"] != float64(3) || b["actions"] != float64(2) || b["patches"] != float64(1) {
		t.Fatalf("skip-all = %d %v", resp.StatusCode, b)
	}
	for _, id := range []string{push.ID, cmd.ID} {
		if a, _ := e.st.Actions().Get(ctx, id); a.Status != "rejected" {
			t.Fatalf("action %s: %+v", id, a)
		}
	}
	if x, _ := e.st.Chat().GetPatch(ctx, p.ID); x.Status != "rejected" {
		t.Fatalf("patch: %+v", x)
	}
	if _, b := do(t, admin, "GET", e.srv.URL+"/api/incidents", nil, nil); b["count"] != float64(0) {
		t.Fatalf("incidents left: %v", b)
	}
	time.Sleep(150 * time.Millisecond)
	if n := resumed.Load(); n != 0 {
		t.Fatalf("skip-all resumed an agent %d times", n)
	}
}
