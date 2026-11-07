package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// Two Get-mutate-Update calls starting from the same row must not let the
// second silently clobber the first's field (lost update): Update is CAS'd
// on updated_at, so the second call gets ErrConflict instead.
func TestMCPServerUpdateConflict(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "demo", Kind: "http", URL: "https://example.com", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	a, _ := st.MCPServers().Get(ctx, m.ID)
	b, _ := st.MCPServers().Get(ctx, m.ID)

	a.Enabled = false
	if err := st.MCPServers().Update(ctx, a); err != nil {
		t.Fatalf("first update: %v", err)
	}

	b.Agents = []string{"agt_1"}
	if err := st.MCPServers().Update(ctx, b); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("second update from the same stale row: got %v, want ErrConflict", err)
	}

	got, _ := st.MCPServers().Get(ctx, m.ID)
	if got.Enabled {
		t.Fatal("the first update's field was lost")
	}
	if len(got.Agents) != 0 {
		t.Fatal("the conflicting update's field was applied despite ErrConflict")
	}
}

// SetResources writes independently of Update/SetCheck, and a fresh Get
// reads back what it saved.
func TestMCPServerSetResources(t *testing.T) {
	ctx := context.Background()
	st, _ := openStore(t)
	m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "demo", Kind: "http", URL: "https://example.com", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(m.LastResources) != 0 {
		t.Fatalf("LastResources at creation = %+v, want empty", m.LastResources)
	}

	want := []storage.MCPResource{{URI: "file:///notes.md", Name: "notes", Description: "team notes", MimeType: "text/markdown"}}
	if err := st.MCPServers().SetResources(ctx, m.ID, want); err != nil {
		t.Fatalf("SetResources: %v", err)
	}
	got, err := st.MCPServers().Get(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LastResources) != 1 || got.LastResources[0] != want[0] {
		t.Fatalf("LastResources = %+v, want %+v", got.LastResources, want)
	}

	if err := st.MCPServers().SetResources(ctx, m.ID, nil); err != nil {
		t.Fatalf("SetResources(nil): %v", err)
	}
	got, err = st.MCPServers().Get(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.LastResources) != 0 {
		t.Fatalf("LastResources after clearing = %+v, want empty", got.LastResources)
	}
}
