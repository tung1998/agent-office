package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
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

// A row whose updated_at was written in another layout (JavaScript's
// toISOString in an imported bundle, or RFC3339Nano from older office) must
// still update: the CAS once compared only office's 9-digit layout, so such
// a row answered ErrConflict to every save, forever.
func TestMCPServerUpdateOtherTimeLayouts(t *testing.T) {
	t.Parallel()
	for name, stored := range map[string]string{
		"js millis":   "2026-10-29T02:42:42.480Z",
		"rfc3339nano": "2026-10-29T02:42:42.48Z",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "o.db")
			st, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			if err := st.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			m, err := st.MCPServers().Create(ctx, storage.MCPServer{Name: "figma", Kind: "stdio", Command: "ssh", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if _, err := raw.ExecContext(ctx, `UPDATE mcp_servers SET updated_at=? WHERE id=?`, stored, m.ID); err != nil {
				t.Fatal(err)
			}

			got, err := st.MCPServers().Get(ctx, m.ID)
			if err != nil {
				t.Fatal(err)
			}
			got.Command = "npx"
			if err := st.MCPServers().Update(ctx, got); err != nil {
				t.Fatalf("update of a row stored as %q: %v", stored, err)
			}
			if again, _ := st.MCPServers().Get(ctx, m.ID); again.Command != "npx" {
				t.Fatalf("command = %q, want npx", again.Command)
			}
			// still a CAS: the stale copy loses
			if err := st.MCPServers().Update(ctx, got); !errors.Is(err, storage.ErrConflict) {
				t.Fatalf("stale update: got %v, want ErrConflict", err)
			}
		})
	}
}
