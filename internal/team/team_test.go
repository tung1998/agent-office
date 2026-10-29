package team_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/team"
	librarytemplates "github.com/tung1998/agent-office/templates/library"
)

type installs struct{ keys []string }

func (i *installs) InstallDefaults(_ context.Context, _ string, keys []string) error {
	i.keys = append(i.keys, keys...)
	return nil
}

func setup(t *testing.T) (storage.Store, *team.Service, *installs, storage.Repo) {
	t.Helper()
	st, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	r, err := st.Repos().Create(context.Background(), storage.Repo{Name: "shop", Path: "/code/shop"})
	if err != nil {
		t.Fatal(err)
	}
	wf := &installs{}
	return st, team.NewService(st, wf), wf, r
}

var council = []string{"office/planner", "office/executor", "office/auditor", "office/backend-dev", "office/data-analyst", "office/devops"}

func TestAddFromLibrarySaveDeleteRestore(t *testing.T) {
	ctx := context.Background()
	st, svc, _, r := setup(t)
	if err := svc.AddFromLibrary(ctx, r.ID, council...); err != nil {
		t.Fatal(err)
	}
	// adding them again changes nothing: the project has them
	if err := svc.AddFromLibrary(ctx, r.ID, council...); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddFromLibrary(ctx, r.ID, "office/nope"); !errors.Is(err, team.ErrNoLibraryAgent) {
		t.Fatalf("unknown library agent: %v", err)
	}
	agents, _ := st.Agents().List(ctx, r.ID)
	r, _ = st.Repos().Get(ctx, r.ID)
	if d, _ := storage.DefaultAgent(r, agents); d.Key != "planner" || len(agents) != 6 {
		t.Fatalf("default = %s, %d agents", d.Key, len(agents))
	}
	planner := agents[0]
	// a bad key is refused; the default moves on when it is deleted
	if _, err := svc.SaveAgent(ctx, storage.Agent{ProjectID: r.ID, Key: "Bad Key", Name: "x", ModelTier: "fast"}, storage.Agent{}); err == nil {
		t.Fatal("bad key accepted")
	}
	old := planner
	planner.Name = "Kế hoạch"
	if _, err := svc.SaveAgent(ctx, planner, old); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteAgent(ctx, planner.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = st.Repos().Get(ctx, r.ID)
	agents, _ = st.Agents().List(ctx, r.ID)
	if r.DefaultAgentID != agents[0].ID || len(agents) != 5 {
		t.Fatalf("default after delete = %q", r.DefaultAgentID)
	}
	// restore the one before the delete: the planner is back, the others kept their ids
	revs, _ := svc.Revisions(ctx, r.ID, 10)
	if len(revs) != 2 || revs[0].Action != "agent.delete:planner" {
		t.Fatalf("revisions = %+v", revs)
	}
	executor := agents[0]
	if _, err := svc.Restore(ctx, revs[0].ID); err != nil {
		t.Fatal(err)
	}
	agents, _ = st.Agents().List(ctx, r.ID)
	r, _ = st.Repos().Get(ctx, r.ID)
	if len(agents) != 6 || agents[0].Key != "planner" || agents[0].Name != "Kế hoạch" || agents[1].ID != executor.ID {
		t.Fatalf("after restore = %+v", agents)
	}
	if d, _ := storage.DefaultAgent(r, agents); d.Key != "planner" {
		t.Fatalf("default after restore = %s", d.Key)
	}
	// the first agent of an empty project is its default
	r2, _ := st.Repos().Create(ctx, storage.Repo{Name: "b", Path: "/b"})
	a, err := svc.SaveAgent(ctx, storage.Agent{ProjectID: r2.ID, Key: "solo", Name: "Solo", ModelTier: "strong"}, storage.Agent{})
	if err != nil {
		t.Fatal(err)
	}
	if r2, _ = st.Repos().Get(ctx, r2.ID); r2.DefaultAgentID != a.ID {
		t.Fatalf("first agent not default: %q", r2.DefaultAgentID)
	}
}

// raceStore wraps a storage.Store so its Agents().List call — the point
// SaveAgent reaches right after it used to read the repo row, before
// opening its transaction — triggers a rename landing on that same repo
// row, simulating a concurrent updateRepo commit.
type raceStore struct {
	storage.Store
	onAgentsList func()
}

func (s raceStore) Agents() storage.AgentRepo {
	return raceAgentRepo{AgentRepo: s.Store.Agents(), onList: s.onAgentsList}
}

type raceAgentRepo struct {
	storage.AgentRepo
	onList func()
}

func (r raceAgentRepo) List(ctx context.Context, projectID string) ([]storage.Agent, error) {
	agents, err := r.AgentRepo.List(ctx, projectID)
	if r.onList != nil {
		r.onList()
	}
	return agents, err
}

func TestMergeKeepsExistingAddsNew(t *testing.T) {
	ctx := context.Background()
	st, svc, _, r := setup(t)
	solo := library(t, team.StarterAgent)

	// (a) an empty project gets the agents as-is
	if err := svc.Merge(ctx, r.ID, solo, "library"); err != nil {
		t.Fatal(err)
	}
	agents, _ := st.Agents().List(ctx, r.ID)
	if len(agents) != len(solo.Agents) {
		t.Fatalf("empty project merge = %d agents, want %d", len(agents), len(solo.Agents))
	}

	// hand-edit the existing agent, and add one the library doesn't know about
	existing := agents[0]
	old := existing
	existing.Instructions = "custom instructions"
	existing.Permissions.ReadOnly = !existing.Permissions.ReadOnly
	if _, err := svc.SaveAgent(ctx, existing, old); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveAgent(ctx, storage.Agent{ProjectID: r.ID, Key: "custom", Name: "Custom", ModelTier: "fast"}, storage.Agent{}); err != nil {
		t.Fatal(err)
	}
	before, _ := st.Agents().List(ctx, r.ID)

	// (b)(c)(d): merge more agents (one the project has); existing keys untouched, custom kept, new keys added
	council := library(t, append([]string{team.StarterAgent}, council...)...)
	if err := svc.Merge(ctx, r.ID, council, "library"); err != nil {
		t.Fatal(err)
	}
	after, _ := st.Agents().List(ctx, r.ID)
	byKey := map[string]storage.Agent{}
	for _, a := range after {
		byKey[a.Key] = a
	}
	for _, a := range before {
		got, ok := byKey[a.Key]
		if !ok {
			t.Fatalf("existing agent %q removed by merge", a.Key)
		}
		if got.Instructions != a.Instructions || got.Permissions.ReadOnly != a.Permissions.ReadOnly || got.Name != a.Name {
			t.Fatalf("merge changed existing agent %q: %+v -> %+v", a.Key, a, got)
		}
	}
	beforeKeys := map[string]bool{}
	for _, a := range before {
		beforeKeys[a.Key] = true
	}
	added := 0
	for _, spec := range council.Agents {
		if beforeKeys[spec.Key] {
			continue
		}
		added++
		if _, ok := byKey[spec.Key]; !ok {
			t.Fatalf("new agent %q not added by merge", spec.Key)
		}
	}
	if len(after) != len(before)+added {
		t.Fatalf("after merge = %d agents, want %d", len(after), len(before)+added)
	}
}

func TestLibraryIsValid(t *testing.T) {
	list, err := team.Library()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 100 || !list[0].Featured || list[0].Set != "office" {
		t.Fatalf("library: %d agents, first %+v", len(list), list[0])
	}
	ids := map[string]bool{}
	names := map[string]string{}
	for _, e := range list {
		if ids[e.ID] {
			t.Errorf("%s: duplicate id", e.ID)
		}
		ids[e.ID] = true
		name := strings.ToLower(strings.TrimSpace(e.Name))
		if other, exists := names[name]; exists {
			t.Errorf("%s and %s: duplicate name %q", other, e.ID, e.Name)
		}
		names[name] = e.ID
		if !slices.Contains(team.LibraryCategories, e.Category) {
			t.Errorf("%s: unknown category %q", e.ID, e.Category)
		}
		if e.Name == "" || e.Description == "" || e.Words == 0 {
			t.Errorf("%s: name, description and instructions are required", e.ID)
		}
		if !slices.Contains([]string{"read", "propose", "check", "edit", "operate"}, e.Level) {
			t.Errorf("%s: level %q", e.ID, e.Level)
		}
		a, err := team.LibraryAgentByID(e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if a.Name != e.Name || a.Spec().Name != e.Name {
			t.Errorf("%s: list name %q differs from detail/spec %q", e.ID, e.Name, a.Name)
		}
		// ADR-074: nothing shipped turns on full access
		if a.Permissions.FullAccess || len(a.Permissions.ExtraDirs) > 0 || a.Instructions == "" {
			t.Errorf("%s: %+v", e.ID, a.Permissions)
		}
		if err := team.Validate([]team.AgentSpec{{Key: a.Key, Name: a.Name, ModelTier: a.ModelTier}}); err != nil {
			t.Error(err)
		}
	}
	for id, want := range map[string]string{
		"aitmpl/code-architect": "code-architect (1)",
		"ecc/code-architect":    "code-architect (2)",
	} {
		a, err := team.LibraryAgentByID(id)
		if err != nil || a.Name != want {
			t.Errorf("%s: name %q, want %q; error: %v", id, a.Name, want, err)
		}
	}
	for _, bad := range []string{"office/../office/devops", "office/nope", "devops", "x/devops"} {
		if _, err := team.LibraryAgentByID(bad); !errors.Is(err, team.ErrNoLibraryAgent) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// library is library agents as a snapshot, the first the default.
func library(t *testing.T, ids ...string) team.Snapshot {
	t.Helper()
	var snap team.Snapshot
	for _, id := range ids {
		a, err := team.LibraryAgentByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Default == "" {
			snap.Default = a.Key
		}
		snap.Agents = append(snap.Agents, a.Spec())
	}
	return snap
}

// ADR-185: the skills a library agent comes with are shipped library skills.
func TestLibrarySkillsExist(t *testing.T) {
	list, err := team.Library()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range list {
		for _, name := range e.Skills {
			n++
			if _, err := fs.Stat(librarytemplates.FS, "skills/"+name+"/SKILL.md"); err != nil {
				t.Errorf("%s: skill %q is not in templates/library/skills", e.ID, name)
			}
		}
	}
	if n == 0 {
		t.Fatal("no library agent comes with skills")
	}
}
