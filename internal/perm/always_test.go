package perm_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func TestSuggestPattern(t *testing.T) {
	for line, want := range map[string]string{
		"git switch main":       "git switch *",
		"go test ./internal/x":  "go test *",
		"ls":                    "ls",
		"pnpm typecheck":        "pnpm typecheck",
		"git -C x status":       "git -C x status", // a flag second: exact
		"git branch -a":         "git branch -a",   // git branch * would cover -D
		"git reset --soft HEAD": "git reset --soft HEAD",
		"rm -rf dist":           "",
		"git push origin main":  "",
		"git reset --hard":      "",
		"psql -c drop table x":  "",
		"bash -c ls":            "",
		"env FOO=1 make":        "",
		"sudo make install":     "",
		"cat a | wc":            "",
		"echo 'a b' c":          "",
	} {
		got, ok := perm.SuggestPattern(line)
		if got != want || ok != (want != "") {
			t.Errorf("SuggestPattern(%q) = %q %v, want %q", line, got, ok, want)
		}
	}
}

func alwaysStore(t *testing.T) (storage.Store, storage.Repo, storage.Agent) {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p"}) // no folder: every built-in pack
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	if _, err := org.ApplyToRepo(ctx, proj.ID, solo.ID, false); err != nil {
		t.Fatal(err)
	}
	model, _ := st.OrgModels().GetForRepo(ctx, proj.ID)
	agents, _ := st.Agents().List(ctx, model.ID)
	return st, proj, agents[0]
}

func TestAllowAlways(t *testing.T) {
	ctx := context.Background()
	st, proj, ag := alwaysStore(t)
	pol := perm.DefaultPolicy()
	pol.Packs = []perm.Pack{{ID: "tools", Label: "Tools", Commands: []string{"make build"}}}
	perm.SavePolicy(ctx, st, proj.ID, pol)
	safe := perm.LoadPolicy(ctx, st, proj.ID).Safe

	// in a built-in pack already: no pack changes; the default picks are kept
	x, err := perm.AllowAlways(ctx, st, proj.ID, ag.ID, "go mod tidy")
	if err != nil || x.Added || x.NewPack || x.Pack != "Go" {
		t.Fatalf("in catalog = %+v %v", x, err)
	}
	got, _ := st.Agents().Get(ctx, ag.ID)
	if got.Permissions.Commands == nil || !slices.Contains(*got.Permissions.Commands, "go mod tidy") || len(*got.Permissions.Commands) != len(safe)+1 {
		t.Fatalf("picks = %v (safe %d)", got.Permissions.Commands, len(safe))
	}
	for _, c := range safe {
		if !slices.Contains(*got.Permissions.Commands, c) {
			t.Fatalf("lost safe command %q", c)
		}
	}

	// a project pack of the same program takes it
	if x, err = perm.AllowAlways(ctx, st, proj.ID, ag.ID, "make test"); err != nil || !x.Added || x.NewPack || x.Pack != "Tools" {
		t.Fatalf("same program = %+v %v", x, err)
	}
	// none: a pack is made
	if x, err = perm.AllowAlways(ctx, st, proj.ID, ag.ID, "git switch *"); err != nil || !x.Added || !x.NewPack || x.Pack != "git (đã duyệt)" {
		t.Fatalf("new pack = %+v %v", x, err)
	}
	// once made, the next of that program goes there
	if x, err = perm.AllowAlways(ctx, st, proj.ID, ag.ID, "git fetch *"); err != nil || x.NewPack || x.Pack != "git (đã duyệt)" {
		t.Fatalf("auto pack again = %+v %v", x, err)
	}
	p := perm.LoadPolicy(ctx, st, proj.ID)
	i := slices.IndexFunc(p.Packs, func(x perm.Pack) bool { return x.ID == "auto-git" })
	if i < 0 || !slices.Equal(p.Packs[i].Commands, []string{"git switch *", "git fetch *"}) || !slices.Contains(p.Packs[0].Commands, "make test") {
		t.Fatalf("packs = %+v", p.Packs)
	}
	got, _ = st.Agents().Get(ctx, ag.ID)
	acc := perm.Resolve(got, perm.Check, p)
	for _, c := range []string{"go mod tidy", "make test", "git switch *", "git fetch *", "git status"} {
		if !slices.Contains(acc.Commands, c) {
			t.Fatalf("access lacks %q: %v", c, acc.Commands)
		}
	}
	if _, err := perm.AllowAlways(ctx, st, proj.ID, ag.ID, "git reset *"); err == nil {
		t.Fatal("a pattern covering git reset --hard must be refused")
	}
	if _, err := perm.AllowAlways(ctx, st, proj.ID, ag.ID, "rm *"); err == nil {
		t.Fatal("rm must be refused")
	}
}
