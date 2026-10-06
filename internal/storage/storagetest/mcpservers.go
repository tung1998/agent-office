package storagetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func testMCPServers(t *testing.T, s storage.Store) {
	ctx := context.Background()
	r := s.MCPServers()
	a, err := r.Create(ctx, storage.MCPServer{Name: "context7", URL: "https://mcp.example/mcp", HeadersEnc: "enc", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.Kind != "http" || a.Scope != "machine" || a.Origin != "manual" {
		t.Fatalf("created = %+v", a)
	}
	if _, err := r.Create(ctx, storage.MCPServer{Name: "context7"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate name err = %v", err)
	}
	b, _ := r.Create(ctx, storage.MCPServer{Name: "abc", URL: "https://b.example/mcp"})
	a.URL, a.Enabled = "https://mcp.example/v2", false
	if err := r.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	b.Name = "context7"
	if err := r.Update(ctx, b); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("rename onto a taken name err = %v", err)
	}
	ro := true
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := r.SetCheck(ctx, a.ID, "ok", "", []storage.MCPTool{{Name: "resolve", Description: "d", ReadOnly: &ro}}, now); err != nil {
		t.Fatal(err)
	}
	got, err := r.GetByName(ctx, "context7")
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://mcp.example/v2" || got.Enabled || got.HeadersEnc != "enc" || got.LastCheckStatus != "ok" ||
		got.LastCheckAt == nil || !got.LastCheckAt.Equal(now) || len(got.LastTools) != 1 || got.LastTools[0].ReadOnly == nil || !*got.LastTools[0].ReadOnly {
		t.Fatalf("after updates = %+v", got)
	}
	if err := r.SetOAuth(ctx, got.ID, "oauth-enc"); err != nil {
		t.Fatal(err)
	}
	if err := r.Update(ctx, got); err != nil { // an edit keeps the login
		t.Fatal(err)
	}
	if got, _ = r.Get(ctx, got.ID); got.OAuthEnc != "oauth-enc" {
		t.Fatalf("oauth after update = %q", got.OAuthEnc)
	}
	got.Agents, got.TrustedTools, got.OriginRef = []string{"agt_1"}, []string{"write"}, `{"type":"user"}`
	if err := r.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.Get(ctx, got.ID); len(got.Agents) != 1 || got.Agents[0] != "agt_1" || len(got.TrustedTools) != 1 || got.OriginRef != `{"type":"user"}` {
		t.Fatalf("assignment after update = %+v", got)
	}
	if b, _ = r.Get(ctx, b.ID); b.Agents == nil || len(b.Agents) != 0 || b.TrustedTools == nil {
		t.Fatalf("defaults = %+v", b)
	}
	list, _ := r.List(ctx)
	if len(list) != 2 || list[0].Name != "abc" {
		t.Fatalf("List by name = %+v", list)
	}
	if err := r.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, a.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get deleted err = %v", err)
	}
}

func testMCPCalls(t *testing.T, s storage.Store) {
	ctx := context.Background()
	r := s.MCPCalls()
	old := time.Now().UTC().Add(-48 * time.Hour)
	for _, c := range []storage.MCPCall{
		{ServerID: "m1", ServerName: "a", Tool: "t1", Status: "ok", CreatedAt: old},
		{ServerID: "m1", ServerName: "a", Tool: "t2", Status: "error", Error: "boom"},
		{ServerID: "m1", ServerName: "a", Tool: "t3", Status: "proposed", ActionID: "act_1"},
		{ServerID: "m2", ServerName: "b", Tool: "t1", Status: "ok", Caller: "Dev", CallerKind: "claude"},
	} {
		if err := r.Add(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	list, err := r.List(ctx, "m1", 10)
	if err != nil || len(list) != 3 || list[2].Tool != "t1" {
		t.Fatalf("List m1 = %+v %v", list, err)
	}
	if all, _ := r.List(ctx, "", 2); len(all) != 2 {
		t.Fatalf("List limit = %+v", all)
	}
	stats, err := r.Stats(ctx, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]storage.MCPCallStat{}
	for _, st := range stats {
		by[st.ServerID] = st
	}
	if st := by["m1"]; st.Calls != 2 || st.Errors != 1 || st.Proposed != 1 || st.LastAt == nil {
		t.Fatalf("stats m1 = %+v", st)
	}
	if n, _ := r.Prune(ctx, time.Now().UTC().Add(-24*time.Hour)); n != 1 {
		t.Fatalf("pruned %d", n)
	}
}
