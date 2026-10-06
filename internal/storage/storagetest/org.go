package storagetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func testProviders(t *testing.T, s storage.Store) {
	ctx := context.Background()
	p := s.Providers()
	a, err := p.Create(ctx, storage.Provider{Name: "Claude API", Kind: storage.ProviderAnthropic, APIKeyEnc: "enc", APIKeyHint: "abcd",
		TierModels: map[string]string{"strong": "m1"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(ctx, storage.Provider{Name: "Claude API", Kind: storage.ProviderOpenAI}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate name err = %v", err)
	}
	b, _ := p.Create(ctx, storage.Provider{Name: "GPT", Kind: storage.ProviderOpenAI, Enabled: true})
	if err := p.SetDefault(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.SetDefault(ctx, "prv_missing"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("SetDefault missing err = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := p.SetStatus(ctx, a.ID, "ok", "3 models", []string{"m1", "m2", "m3"}, now); err != nil {
		t.Fatal(err)
	}
	a.TierModels["fast"] = "m3"
	a.BaseURL = "https://example.test"
	if err := p.Update(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := p.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || len(got.Models) != 3 || got.TierModels["fast"] != "m3" || got.BaseURL != "https://example.test" ||
		got.CheckedAt == nil || !got.CheckedAt.Equal(now) || got.APIKeyEnc != "enc" {
		t.Fatalf("provider after updates = %+v", got)
	}
	list, _ := p.List(ctx)
	if len(list) != 2 || list[0].ID != b.ID || !list[0].IsDefault || list[1].IsDefault {
		t.Fatalf("List default first = %+v", list)
	}
	if err := p.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
}

func testOrg(t *testing.T, s storage.Store) {
	ctx := context.Background()
	tpl, err := s.OrgModels().Create(ctx, storage.OrgModel{Key: "team", Name: "Team", Kind: storage.KindTeam, Builtin: true,
		Governance: storage.Governance{Mode: "hierarchy"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OrgModels().Create(ctx, storage.OrgModel{Key: "team", Name: "Dup", Kind: storage.KindCustom}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate template key err = %v", err)
	}
	prov, _ := s.Providers().Create(ctx, storage.Provider{Name: "P", Kind: storage.ProviderAnthropic})
	lead, err := s.Agents().Create(ctx, storage.Agent{OrgModelID: tpl.ID, Key: "lead", Name: "Lead", Tier: storage.TierLead, ModelTier: "strong",
		ProviderID: prov.ID, Permissions: storage.Permissions{ReadOnly: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Agents().Create(ctx, storage.Agent{OrgModelID: tpl.ID, Key: "dev", Name: "Dev", Tier: storage.TierWorker, ModelTier: "fast",
		ReportsTo: []string{"lead"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Agents().Create(ctx, storage.Agent{OrgModelID: tpl.ID, Key: "lead", Name: "x", Tier: storage.TierWorker, ModelTier: "fast"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate agent key err = %v", err)
	}
	agents, _ := s.Agents().List(ctx, tpl.ID)
	if len(agents) != 2 || agents[0].Key != "lead" || agents[1].ReportsTo[0] != "lead" || !agents[0].Permissions.ReadOnly {
		t.Fatalf("agents = %+v", agents)
	}
	// on by default; paused/resumed only by SetEnabled, Update keeps it
	if agents[0].Disabled || agents[1].Disabled {
		t.Fatalf("new agents should be on: %+v", agents)
	}
	if err := s.Agents().SetEnabled(ctx, lead.ID, false); err != nil {
		t.Fatal(err)
	}
	off, _ := s.Agents().Get(ctx, lead.ID)
	if !off.Disabled {
		t.Fatalf("lead should be paused: %+v", off)
	}
	off.Name = "Lead 2"
	if err := s.Agents().Update(ctx, off); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agents().Get(ctx, lead.ID); !a.Disabled || a.Name != "Lead 2" {
		t.Fatalf("update changed paused state: %+v", a)
	}
	if err := s.Agents().SetEnabled(ctx, lead.ID, true); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agents().Get(ctx, lead.ID); a.Disabled {
		t.Fatalf("lead should be on again: %+v", a)
	}
	if err := s.Agents().SetEnabled(ctx, "agt_missing", false); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("SetEnabled missing err = %v", err)
	}
	// deleting the provider must not delete the agent, only unlink it
	if err := s.Providers().Delete(ctx, prov.ID); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agents().Get(ctx, lead.ID); a.ProviderID != "" {
		t.Fatalf("provider not unlinked: %+v", a)
	}

	repo, err := s.Repos().Create(ctx, storage.Repo{Name: "shop", Path: "/code/shop"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Repos().Create(ctx, storage.Repo{Name: "dup", Path: "/code/shop"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate path err = %v", err)
	}
	inst, err := s.OrgModels().Create(ctx, storage.OrgModel{RepoID: repo.ID, SourceTemplateID: tpl.ID, Key: "team", Name: "Team", Kind: storage.KindTeam})
	if err != nil {
		t.Fatalf("instance may reuse template key: %v", err)
	}
	if _, err := s.OrgModels().Create(ctx, storage.OrgModel{RepoID: repo.ID, Key: "solo", Name: "Solo", Kind: storage.KindSolo}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("second instance for repo err = %v", err)
	}
	got, err := s.OrgModels().GetForRepo(ctx, repo.ID)
	if err != nil || got.ID != inst.ID || got.SourceTemplateID != tpl.ID || got.IsTemplate() {
		t.Fatalf("GetForRepo = %+v, %v", got, err)
	}
	if _, err := s.Agents().Create(ctx, storage.Agent{OrgModelID: inst.ID, Key: "lead", Name: "Lead", Tier: storage.TierLead, ModelTier: "strong"}); err != nil {
		t.Fatal(err)
	}
	templates, _ := s.OrgModels().ListTemplates(ctx)
	if len(templates) != 1 {
		t.Fatalf("templates must exclude instances: %d", len(templates))
	}
	// deleting the repo cascades to its instance and agents
	if err := s.Repos().Delete(ctx, repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OrgModels().Get(ctx, inst.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("instance survived repo delete: %v", err)
	}
	if a, _ := s.Agents().List(ctx, inst.ID); len(a) != 0 {
		t.Fatalf("agents survived: %d", len(a))
	}
}

func testTx(t *testing.T, s storage.Store) {
	ctx := context.Background()
	boom := errors.New("boom")
	err := s.InTx(ctx, func(tx storage.Store) error {
		if _, err := tx.Repos().Create(ctx, storage.Repo{Name: "a", Path: "/a"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx err = %v", err)
	}
	if _, err := s.Repos().GetByPath(ctx, "/a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("rollback failed: %v", err)
	}
	if err := s.InTx(ctx, func(tx storage.Store) error {
		_, err := tx.Repos().Create(ctx, storage.Repo{Name: "b", Path: "/b"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Repos().GetByPath(ctx, "/b"); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
}

func testHelperProjects(t *testing.T, s storage.Store) {
	ctx := context.Background()
	a, err := s.Repos().Create(ctx, storage.Repo{Name: "Trợ lý máy"})
	if err != nil {
		t.Fatalf("project without path: %v", err)
	}
	if _, err := s.Repos().Create(ctx, storage.Repo{Name: "Trợ lý 2"}); err != nil {
		t.Fatalf("second project without path must be allowed: %v", err)
	}
	if _, err := s.Repos().Create(ctx, storage.Repo{Name: "x", Path: "/p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Repos().Create(ctx, storage.Repo{Name: "y", Path: "/p"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate real path err = %v", err)
	}
	// helper projects keep their model through the table rebuild
	m, err := s.OrgModels().Create(ctx, storage.OrgModel{RepoID: a.ID, Key: "solo", Name: "Solo", Kind: storage.KindSolo})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.OrgModels().GetForRepo(ctx, a.ID); err != nil || got.ID != m.ID {
		t.Fatalf("model for helper = %+v, %v", got, err)
	}
	if err := s.Repos().Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OrgModels().Get(ctx, m.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("cascade must still work after rebuild")
	}
}
