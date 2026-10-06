package orgmodel_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func newStore(t *testing.T) storage.Store {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestBuiltinsAreValid(t *testing.T) {
	bs, err := orgmodel.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 3 || bs[0].Key != "solo" || bs[1].Key != "team" || bs[2].Key != "council" {
		t.Fatalf("builtins = %v", bs)
	}
	for _, b := range bs {
		if err := orgmodel.Validate(b); err != nil {
			t.Errorf("%s: %v", b.Key, err)
		}
	}
	leads := 0
	for _, a := range bs[2].Agents {
		if a.Tier == storage.TierLead {
			leads++
		}
	}
	if leads != 3 || bs[2].Governance.Quorum != 2 {
		t.Fatalf("council must have 3 peer leads, quorum 2: leads=%d", leads)
	}
}

func TestValidateRejects(t *testing.T) {
	base := func() orgmodel.Template {
		return orgmodel.Template{Key: "x", Name: "X", Kind: storage.KindCustom, Agents: []orgmodel.AgentSpec{
			{Key: "lead", Name: "L", Tier: "lead", ModelTier: "strong"},
			{Key: "m", Name: "M", Tier: "manager", ModelTier: "balanced", ReportsTo: []string{"lead"}},
			{Key: "w", Name: "W", Tier: "worker", ModelTier: "fast", ReportsTo: []string{"m"}},
		}}
	}
	cases := map[string]func(*orgmodel.Template){
		"unknown boss":     func(t *orgmodel.Template) { t.Agents[2].ReportsTo = []string{"ghost"} },
		"orphan worker":    func(t *orgmodel.Template) { t.Agents[2].ReportsTo = nil },
		"report to worker": func(t *orgmodel.Template) { t.Agents[1].ReportsTo = []string{"w"} },
		"cycle": func(t *orgmodel.Template) {
			t.Agents = append(t.Agents, orgmodel.AgentSpec{Key: "m2", Name: "M2", Tier: "manager", ModelTier: "fast", ReportsTo: []string{"m"}})
			t.Agents[1].ReportsTo = []string{"m2"}
		},
		"no lead":          func(t *orgmodel.Template) { t.Agents[0].Tier = "manager"; t.Agents[0].ReportsTo = []string{"m"} },
		"duplicate key":    func(t *orgmodel.Template) { t.Agents[2].Key = "m" },
		"bad model tier":   func(t *orgmodel.Template) { t.Agents[0].ModelTier = "huge" },
		"solo with 3":      func(t *orgmodel.Template) { t.Kind = storage.KindSolo },
		"council quorum":   func(t *orgmodel.Template) { t.Kind = storage.KindCouncil; t.Governance.Quorum = 2 },
		"veto non-lead":    func(t *orgmodel.Template) { t.Governance.Veto = []string{"w"} },
		"bad template key": func(t *orgmodel.Template) { t.Key = "Bad Key" },
	}
	if err := orgmodel.Validate(base()); err != nil {
		t.Fatalf("base invalid: %v", err)
	}
	for name, mutate := range cases {
		tpl := base()
		mutate(&tpl)
		var ve *orgmodel.ValidationError
		if err := orgmodel.Validate(tpl); !errors.As(err, &ve) {
			t.Errorf("%s: expected validation error, got %v", name, err)
		}
	}
}

func TestSeedApplyAndEdit(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := orgmodel.NewService(st)

	n, err := svc.SeedBuiltins(ctx)
	if err != nil || n != 3 {
		t.Fatalf("seed = %d, %v", n, err)
	}
	if n, _ := svc.SeedBuiltins(ctx); n != 0 {
		t.Fatalf("second seed inserted %d", n)
	}
	team, _ := st.OrgModels().GetTemplateByKey(ctx, "team")
	repo, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: "/code/shop"})

	inst, err := svc.ApplyToRepo(ctx, repo.ID, team.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyToRepo(ctx, repo.ID, team.ID, false); !errors.Is(err, orgmodel.ErrHasInstance) {
		t.Fatalf("second apply err = %v", err)
	}
	instAgents, _ := st.Agents().List(ctx, inst.ID)
	tplAgents, _ := st.Agents().List(ctx, team.ID)
	if len(instAgents) != len(tplAgents) || instAgents[0].ID == tplAgents[0].ID {
		t.Fatal("instance must be a copy of the template agents")
	}

	// editing the instance does not touch the template
	eng := findAgent(instAgents, "engineer")
	eng.Name = "Kỹ sư backend"
	eng.LLMModel = "claude-sonnet-5"
	if _, err := svc.SaveAgent(ctx, eng); err != nil {
		t.Fatal(err)
	}
	if a := findAgent(mustList(t, st, team.ID), "engineer"); a.Name == "Kỹ sư backend" {
		t.Fatal("template changed by instance edit")
	}

	// renaming a manager key keeps reporting lines
	pm := findAgent(instAgents, "project-manager")
	pm.Key = "delivery-manager"
	if _, err := svc.SaveAgent(ctx, pm); err != nil {
		t.Fatal(err)
	}
	if a := findAgent(mustList(t, st, inst.ID), "engineer"); !contains(a.ReportsTo, "delivery-manager") {
		t.Fatalf("reports_to not renamed: %v", a.ReportsTo)
	}

	// adding an invalid agent is rejected, valid one accepted
	if _, err := svc.SaveAgent(ctx, storage.Agent{OrgModelID: inst.ID, Key: "orphan", Name: "O", Tier: "worker", ModelTier: "fast"}); err == nil {
		t.Fatal("orphan worker accepted")
	}
	added, err := svc.SaveAgent(ctx, storage.Agent{OrgModelID: inst.ID, Key: "designer", Name: "Designer", Tier: "worker", ModelTier: "fast", ReportsTo: []string{"architect"}})
	if err != nil {
		t.Fatal(err)
	}
	// deleting a manager that others depend on is rejected
	if err := svc.DeleteAgent(ctx, findAgent(mustList(t, st, inst.ID), "architect").ID); err == nil || !strings.Contains(err.Error(), "architect") {
		t.Fatalf("delete depended-on agent err = %v", err)
	}
	if err := svc.DeleteAgent(ctx, added.ID); err != nil {
		t.Fatal(err)
	}

	// clone and reset
	clone, err := svc.CloneTemplate(ctx, inst.ID, "team-shop", "Team cho shop")
	if err != nil || !clone.IsTemplate() || clone.Builtin {
		t.Fatalf("clone = %+v, %v", clone, err)
	}
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	svc.UpdateModel(ctx, solo.ID, func(m *storage.OrgModel) { m.Name = "Solo đã sửa" })
	reset, err := svc.ResetBuiltin(ctx, "solo")
	if err != nil || reset.Name != "Solo" {
		t.Fatalf("reset = %+v, %v", reset, err)
	}
	if _, err := svc.ResetBuiltin(ctx, "team-shop"); !errors.Is(err, orgmodel.ErrBuiltinReset) {
		t.Fatalf("reset custom err = %v", err)
	}

	// replace the repo model with council
	council, _ := st.OrgModels().GetTemplateByKey(ctx, "council")
	inst2, err := svc.ApplyToRepo(ctx, repo.ID, council.ID, true)
	if err != nil || inst2.Kind != storage.KindCouncil {
		t.Fatalf("replace = %+v, %v", inst2, err)
	}
	if inst2.ID != inst.ID {
		t.Fatal("replace must keep the model id (history and project link)")
	}
}

func mustList(t *testing.T, st storage.Store, id string) []storage.Agent {
	a, err := st.Agents().List(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func findAgent(as []storage.Agent, key string) storage.Agent {
	for _, a := range as {
		if a.Key == key {
			return a
		}
	}
	return storage.Agent{}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func TestRevisionsAndRestore(t *testing.T) {
	ctx := orgmodel.WithActor(context.Background(), "human:a@b.c")
	st := newStore(t)
	svc := orgmodel.NewService(st)
	svc.SeedBuiltins(ctx)
	team, _ := st.OrgModels().GetTemplateByKey(ctx, "team")
	repo, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: "/shop"})
	inst, _ := svc.ApplyToRepo(ctx, repo.ID, team.ID, false)

	eng := findAgent(mustList(t, st, inst.ID), "engineer")
	eng.Name = "Kỹ sư A"
	svc.SaveAgent(ctx, eng)
	eng.Name = "Kỹ sư B"
	svc.SaveAgent(ctx, eng)
	svc.UpdateModel(ctx, inst.ID, func(m *storage.OrgModel) { m.Name = "Team shop" })

	revs, err := svc.Revisions(ctx, inst.ID, 10)
	if err != nil || len(revs) != 3 {
		t.Fatalf("revisions = %d, %v", len(revs), err)
	}
	if revs[0].Action != "model.update" || revs[0].Actor != "human:a@b.c" || revs[2].Action != "agent.update:engineer" {
		t.Fatalf("revision order/actions = %s, %s", revs[0].Action, revs[2].Action)
	}
	// oldest snapshot = before the first edit
	if _, err := svc.Restore(ctx, revs[2].ID); err != nil {
		t.Fatal(err)
	}
	m, _ := st.OrgModels().Get(ctx, inst.ID)
	if a := findAgent(mustList(t, st, inst.ID), "engineer"); a.Name != "Kỹ sư" || m.Name != "Team" || m.RepoID != repo.ID {
		t.Fatalf("after restore: agent=%q model=%q", a.Name, m.Name)
	}
	// restore is itself undoable
	revs, _ = svc.Revisions(ctx, inst.ID, 10)
	if len(revs) != 4 || !strings.HasPrefix(revs[0].Action, "restore:") {
		t.Fatalf("restore not snapshotted: %v", revs[0].Action)
	}
	svc.Restore(ctx, revs[0].ID)
	if a := findAgent(mustList(t, st, inst.ID), "engineer"); a.Name != "Kỹ sư B" {
		t.Fatalf("undo restore: %q", a.Name)
	}
	// replacing the model keeps history
	council, _ := st.OrgModels().GetTemplateByKey(ctx, "council")
	svc.ApplyToRepo(ctx, repo.ID, council.ID, true)
	revs, _ = svc.Revisions(ctx, inst.ID, 50)
	if revs[0].Action != "model.replace" || revs[0].AgentCount != 9 {
		t.Fatalf("replace revision = %+v", revs[0])
	}
}

func TestProviderSurvivesCloneAndRestore(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := orgmodel.NewService(st)
	svc.SeedBuiltins(ctx)
	prov, _ := st.Providers().Create(ctx, storage.Provider{Name: "GPT", Kind: storage.ProviderOpenAI})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	a := mustList(t, st, solo.ID)[0]
	a.ProviderID = prov.ID
	if _, err := svc.SaveAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	clone, _ := svc.CloneTemplate(ctx, solo.ID, "solo-2", "Solo 2")
	if got := mustList(t, st, clone.ID)[0].ProviderID; got != prov.ID {
		t.Fatalf("clone lost provider: %q", got)
	}
	// restoring a snapshot whose provider was deleted falls back to default
	a = mustList(t, st, clone.ID)[0]
	a.Name = "x"
	svc.SaveAgent(ctx, a)
	st.Providers().Delete(ctx, prov.ID)
	revs, _ := svc.Revisions(ctx, clone.ID, 1)
	if _, err := svc.Restore(ctx, revs[0].ID); err != nil {
		t.Fatalf("restore with deleted provider: %v", err)
	}
	if got := mustList(t, st, clone.ID)[0].ProviderID; got != "" {
		t.Fatalf("expected fallback to default, got %q", got)
	}
}

// ADR-074 security: full access and extra dirs never come from a template —
// applying one (a fresh instance, a replace, an import) always creates the
// agent without them, however the source spec is set.
func TestApplyTemplateNeverTurnsOnFullAccess(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	svc := orgmodel.NewService(st)
	repo, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: "/shop"})
	tpl := orgmodel.Template{Key: "x", Name: "X", Kind: storage.KindCustom, Agents: []orgmodel.AgentSpec{
		{Key: "lead", Name: "L", Tier: storage.TierLead, ModelTier: "strong",
			Permissions: storage.Permissions{Level: perm.Operate, FullAccess: true, FullAccessBy: "admin@x.io", ExtraDirs: []string{"/tmp/x"}}},
	}}
	inst, err := svc.ApplyTemplate(ctx, repo.ID, tpl, "")
	if err != nil {
		t.Fatal(err)
	}
	a := mustList(t, st, inst.ID)[0]
	if a.Permissions.FullAccess || a.Permissions.FullAccessBy != "" || len(a.Permissions.ExtraDirs) != 0 {
		t.Fatalf("agent created from template has full access: %+v", a.Permissions)
	}

	// also a custom (library) template: CreateTemplate, then CloneTemplate
	created, err := svc.CreateTemplate(ctx, tpl)
	if err != nil {
		t.Fatal(err)
	}
	if a := mustList(t, st, created.ID)[0]; a.Permissions.FullAccess {
		t.Fatalf("library template agent has full access: %+v", a.Permissions)
	}
	clone, err := svc.CloneTemplate(ctx, created.ID, "x-2", "X 2")
	if err != nil {
		t.Fatal(err)
	}
	if a := mustList(t, st, clone.ID)[0]; a.Permissions.FullAccess {
		t.Fatalf("cloned template agent has full access: %+v", a.Permissions)
	}
}
