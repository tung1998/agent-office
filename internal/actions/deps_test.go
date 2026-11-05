package actions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// fakeNPM puts an npm on PATH that fills node_modules/fresh of the folder
// it runs in (or fails with "boom" when fail).
func fakeNPM(t *testing.T, fail bool) {
	t.Helper()
	bin := t.TempDir()
	body := "#!/bin/sh\nmkdir -p node_modules/fresh && echo installed in \"$PWD\"\n"
	if fail {
		body = "#!/bin/sh\necho boom >&2\nexit 1\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func depsProject(t *testing.T) (context.Context, storage.Store, *Service, storage.Repo) {
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
	root := t.TempDir()
	for _, f := range []string{"web/package.json", "web/package-lock.json"} {
		p := filepath.Join(root, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("{}"), 0o644)
	}
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: root})
	return ctx, st, New(st, nil), proj
}

// In the run's own worktree the install runs at once from "edit" up, in
// folders of the worktree's own: the project's node_modules is not touched.
func TestDepsInstallInWorktree(t *testing.T) {
	fakeNPM(t, false)
	ctx, _, svc, proj := depsProject(t)
	shared := filepath.Join(proj.Path, "web", "node_modules")
	os.MkdirAll(shared, 0o755)
	tree := t.TempDir()
	for _, f := range []string{"web/package.json", "web/package-lock.json"} {
		p := filepath.Join(tree, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("{}"), 0o644)
	}
	if err := os.Symlink(shared, filepath.Join(tree, "web", "node_modules")); err != nil {
		t.Fatal(err)
	}
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", Agent: "a", Level: perm.Edit, Access: at(perm.Edit), Dir: tree}
	a, err := svc.Propose(ctx, sc, "deps_install", "web", "nuxt 4.6.4")
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "done" || a.Target != "web (npm)" || a.Args.Dir != tree || !strings.Contains(a.Detail, "installed in") {
		t.Fatalf("worktree install = %s %q %q %q", a.Status, a.Target, a.Args.Dir, a.Detail)
	}
	if _, err := os.Stat(filepath.Join(shared, "fresh")); err == nil {
		t.Fatal("the install wrote into the project's node_modules")
	}
	if fi, err := os.Lstat(filepath.Join(tree, "web", "node_modules")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the worktree's node_modules is still the project's link: %v", err)
	}
	if _, err := svc.Propose(ctx, sc, "deps_install", "../x", "x"); err == nil {
		t.Fatal("a folder outside the project was accepted")
	}
	// an install runs the packages' scripts: an agent that only proposes asks
	sc.Level, sc.Access = perm.Propose, at(perm.Propose)
	if a, _ := svc.Propose(ctx, sc, "deps_install", "web", "again"); a.Status != "pending" {
		t.Fatalf("propose level installs on its own: %s", a.Status)
	}
}

// In the project: a card for a person, unless the project installs merged
// dependency changes on its own (policy AutoDeps); then the worktree the
// diff came from links back to the project's folder.
func TestInstallAutoWhenPolicyAllows(t *testing.T) {
	fakeNPM(t, false)
	ctx, st, svc, proj := depsProject(t)
	tree := t.TempDir()
	os.MkdirAll(filepath.Join(tree, "web", "node_modules", "old"), 0o755) // the worktree's own copy
	c1, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: proj.ID})
	c2, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: proj.ID})
	sc := Scope{ProjectID: proj.ID, ConversationID: c1.ID, Agent: "office"}
	args := storage.ActionArgs{Deps: &storage.DepsArgs{Dir: "web", Manager: "npm"}, Patch: "pat_1", Tree: tree}
	a, err := svc.Propose(ctx, sc, "deps_install", "web", "merged", args)
	if err != nil || a.Status != "pending" || a.Args.Dir != "" {
		t.Fatalf("no policy: %s %q %v", a.Status, a.Args.Dir, err)
	}
	pol := perm.LoadPolicy(ctx, st, proj.ID)
	pol.AutoDeps = true
	if err := perm.SavePolicy(ctx, st, proj.ID, pol); err != nil {
		t.Fatal(err)
	}
	sc.ConversationID = c2.ID
	a, err = svc.Propose(ctx, sc, "deps_install", "web", "merged", args)
	if err != nil || a.Status != "done" || !strings.HasPrefix(a.DecidedBy, "auto:") {
		t.Fatalf("auto: %s %q %v", a.Status, a.DecidedBy, err)
	}
	if _, err := os.Stat(filepath.Join(proj.Path, "web", "node_modules", "fresh")); err != nil {
		t.Fatal("the project's dependencies were not installed")
	}
	if fi, err := os.Lstat(filepath.Join(tree, "web", "node_modules")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the worktree did not link back: %v", err)
	}
}

// A failed install stays on its card with what the tool said.
func TestDepsInstallFailureKeptOnCard(t *testing.T) {
	fakeNPM(t, true)
	ctx, st, svc, proj := depsProject(t)
	c1, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: proj.ID})
	sc := Scope{ProjectID: proj.ID, ConversationID: c1.ID, Agent: "office"}
	a, err := svc.Propose(ctx, sc, "deps_install", "web", "merged", storage.ActionArgs{Deps: &storage.DepsArgs{Dir: "web"}, Patch: "pat_1"})
	if err != nil {
		t.Fatal(err)
	}
	done, err := svc.Decide(ctx, a.ID, true, "human:a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "failed" || !strings.Contains(done.Detail, "boom") || !strings.Contains(done.Detail, "web (npm)") {
		t.Fatalf("failure = %s %q", done.Status, done.Detail)
	}
}
