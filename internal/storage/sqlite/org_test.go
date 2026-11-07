package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func openProviders(t *testing.T) (storage.ProviderRepo, context.Context) {
	path := filepath.Join(t.TempDir(), "office.db")
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s.Providers(), ctx
}

// SetTierModels writes only while the row still matches the snapshot it was
// handed (base_url, kind, tier_models unchanged since it was read).
func TestSetTierModelsWritesWhenSnapshotMatches(t *testing.T) {
	repo, ctx := openProviders(t)
	p, err := repo.Create(ctx, storage.Provider{Name: "GPT", Kind: storage.ProviderOpenAI, BaseURL: "http://x/v1"})
	if err != nil {
		t.Fatal(err)
	}
	newTiers := map[string]string{"strong": "gpt-5", "fast": "gpt-5-mini"}
	ok, err := repo.SetTierModels(ctx, p.ID, newTiers, p.TierModels, p.BaseURL, p.Kind)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected write to apply")
	}
	got, _ := repo.Get(ctx, p.ID)
	if got.TierModels["strong"] != "gpt-5" || got.TierModels["fast"] != "gpt-5-mini" {
		t.Fatalf("tiers = %v", got.TierModels)
	}
}

// If tier_models changed (e.g. an admin save) since the snapshot was taken,
// the write is skipped and the row is left untouched.
func TestSetTierModelsSkipsWhenTierModelsChanged(t *testing.T) {
	repo, ctx := openProviders(t)
	p, err := repo.Create(ctx, storage.Provider{Name: "GPT", Kind: storage.ProviderOpenAI, BaseURL: "http://x/v1"})
	if err != nil {
		t.Fatal(err)
	}
	staleTiers := p.TierModels // snapshot taken before the concurrent save below
	if err := repo.Update(ctx, storage.Provider{ID: p.ID, Name: "GPT", Kind: p.Kind, BaseURL: p.BaseURL,
		TierModels: map[string]string{"strong": "admin-picked-model"}}); err != nil {
		t.Fatal(err)
	}

	newTiers := map[string]string{"strong": "gpt-5", "fast": "gpt-5-mini"}
	ok, err := repo.SetTierModels(ctx, p.ID, newTiers, staleTiers, p.BaseURL, p.Kind)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("expected write to be skipped")
	}
	got, _ := repo.Get(ctx, p.ID)
	if got.TierModels["strong"] != "admin-picked-model" || got.TierModels["fast"] != "" {
		t.Fatalf("concurrent save was overwritten: %v", got.TierModels)
	}
}

func openRepos(t *testing.T) (storage.RepoRepo, context.Context) {
	path := filepath.Join(t.TempDir(), "office.db")
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s.Repos(), ctx
}

// UpdateInfo and SetDefaultAgent each write only their own column, so a
// rename and a default-agent change racing on the same row both stick
// regardless of write order.
func TestRepoUpdateInfoAndSetDefaultAgentDontClobberEachOther(t *testing.T) {
	repo, ctx := openRepos(t)
	r, err := repo.Create(ctx, storage.Repo{Name: "old-name", Path: "/tmp/x"})
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.SetDefaultAgent(ctx, r.ID, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateInfo(ctx, r.ID, "new-name", "new-desc"); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, r.ID)
	if got.Name != "new-name" || got.DefaultAgentID != "agent-1" {
		t.Fatalf("got = %+v", got)
	}

	if err := repo.UpdateInfo(ctx, r.ID, "newer-name", "newer-desc"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetDefaultAgent(ctx, r.ID, "agent-2"); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, r.ID)
	if got.Name != "newer-name" || got.DefaultAgentID != "agent-2" {
		t.Fatalf("got = %+v", got)
	}
}

// UpdateInfoRemote is CAS'd on updated_at, so two concurrent "Edit project"
// saves that both read the same row (admin A changing git_remote, admin B
// still on A's pre-change description) can't have the second silently
// overwrite the first's write: the loser gets ErrStale instead of landing.
func TestRepoUpdateInfoRemoteConflictsWithConcurrentWrite(t *testing.T) {
	repo, ctx := openRepos(t)
	r, err := repo.Create(ctx, storage.Repo{Name: "old-name", Path: "/tmp/x", Description: "old-desc"})
	if err != nil {
		t.Fatal(err)
	}
	// two independent reads of the same row, like admin A and admin B each
	// opening the edit modal before either saves
	fromA := r
	fromB := r

	if err := repo.UpdateInfoRemote(ctx, r.ID, fromA.Name, "from A", fromA.GitRemote, fromA.UpdatedAt); err != nil {
		t.Fatalf("first update: %v", err)
	}

	if err := repo.UpdateInfoRemote(ctx, r.ID, fromB.Name, "from B", fromB.GitRemote, fromB.UpdatedAt); !errors.Is(err, storage.ErrStale) {
		t.Fatalf("stale second update = %v, want ErrStale", err)
	}

	got, err := repo.Get(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "from A" {
		t.Fatalf("stale write landed despite ErrStale: %+v", got)
	}
}

// UpdateInfoRemote answers storage.ErrNotFound (not ErrStale) when the row
// was deleted between the caller's read and its write.
func TestRepoUpdateInfoRemoteNotFoundWhenDeleted(t *testing.T) {
	repo, ctx := openRepos(t)
	r, err := repo.Create(ctx, storage.Repo{Name: "old-name", Path: "/tmp/x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateInfoRemote(ctx, r.ID, "new-name", "new-desc", "", r.UpdatedAt); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("update after delete = %v, want ErrNotFound", err)
	}
}
