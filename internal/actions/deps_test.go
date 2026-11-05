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

// In the run's own worktree the install runs at once when the run may run
// commands on its own, in
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
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", Agent: "a", Level: perm.Check, Access: at(perm.Check), Dir: tree}
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
	// an install runs the packages' scripts (postinstall…): only a run that
	// may run commands on its own does it; one that only proposes, or edits
	// without commands, asks a person
	sc.Level, sc.Access = perm.Propose, at(perm.Propose)
	if a, _ := svc.Propose(ctx, sc, "deps_install", "web", "again"); a.Status != "pending" {
		t.Fatalf("propose level installs on its own: %s", a.Status)
	}
	sc.Level, sc.Access, sc.RunRef = perm.Edit, perm.Access{Level: perm.Edit, Caps: []string{perm.CapApply}}, "r2"
	if a, _ := svc.Propose(ctx, sc, "deps_install", "web", "edit only"); a.Status != "pending" {
		t.Fatalf("edit without commands installs on its own: %s", a.Status)
	}
}

// A merged diff's install is a card in the project, even when the project
// installs on its own (the chat engine starts that one in the background,
// never inside Propose); once it ran, the worktree the diff came from links
// back to the project's folder. Another merged diff's card is another card.
func TestMergedInstallRelinksWorktree(t *testing.T) {
	fakeNPM(t, false)
	ctx, st, svc, proj := depsProject(t)
	pol := perm.LoadPolicy(ctx, st, proj.ID)
	pol.AutoDeps = true
	if err := perm.SavePolicy(ctx, st, proj.ID, pol); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	os.MkdirAll(filepath.Join(tree, "web", "node_modules", "old"), 0o755) // the worktree's own copy
	c1, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: proj.ID})
	sc := Scope{ProjectID: proj.ID, ConversationID: c1.ID, Agent: "office"}
	args := storage.ActionArgs{Deps: &storage.DepsArgs{Dir: "web", Manager: "npm"}, Patch: "pat_1", Tree: tree}
	a, err := svc.Propose(ctx, sc, "deps_install", "web", "merged", args)
	if err != nil || a.Status != "pending" || a.Args.Dir != "" {
		t.Fatalf("merged diff's card: %s %q %v", a.Status, a.Args.Dir, err)
	}
	other := args
	other.Patch = "pat_2"
	if b, _ := svc.Propose(ctx, sc, "deps_install", "web", "merged", other); b.ID == a.ID {
		t.Fatal("another diff's install reused the first card")
	}
	a, err = svc.Decide(ctx, a.ID, true, "auto:office")
	if err != nil || a.Status != "done" {
		t.Fatalf("install: %s %q %v", a.Status, a.Detail, err)
	}
	if _, err := os.Stat(filepath.Join(proj.Path, "web", "node_modules", "fresh")); err != nil {
		t.Fatal("the project's dependencies were not installed")
	}
	if fi, err := os.Lstat(filepath.Join(tree, "web", "node_modules")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the worktree did not link back: %v", err)
	}
}

// A worktree that already has libraries of its own (an earlier install that
// worked) keeps them when the next install fails: they are not swapped for
// the project's, nor left half-written. One that works drops the old copy.
func TestFailedInstallKeepsPrivateDeps(t *testing.T) {
	ctx, _, svc, proj := depsProject(t)
	os.MkdirAll(filepath.Join(proj.Path, "web", "node_modules", "shared"), 0o755)
	tree := t.TempDir()
	for _, f := range []string{"web/package.json", "web/package-lock.json"} {
		p := filepath.Join(tree, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("{}"), 0o644)
	}
	own := filepath.Join(tree, "web", "node_modules")
	os.MkdirAll(filepath.Join(own, "mine"), 0o755) // the earlier install's

	// an npm that writes something, then fails
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\nrm -rf node_modules/mine; mkdir -p node_modules/half\necho boom >&2\nexit 1\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sc := Scope{ProjectID: proj.ID, RunRef: "k1", Agent: "a", Level: perm.Check, Access: at(perm.Check), Dir: tree}
	a, err := svc.Propose(ctx, sc, "deps_install", "web", "bump")
	if err != nil || a.Status != "failed" || !strings.Contains(a.Detail, "boom") {
		t.Fatalf("failed install = %s %q %v", a.Status, a.Detail, err)
	}
	if fi, err := os.Lstat(own); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the worktree's own node_modules became the project's: %v", err)
	}
	if _, err := os.Stat(filepath.Join(own, "mine")); err != nil {
		t.Fatal("the earlier install's libraries were lost")
	}
	if _, err := os.Stat(filepath.Join(own, "half")); err == nil {
		t.Fatal("the failed install's leftovers were kept")
	}
	if !strings.Contains(a.Detail, "giữ thư viện riêng") {
		t.Fatalf("the card does not say the earlier ones were kept: %q", a.Detail)
	}

	// then one that works: the new libraries, no copy of the old left beside the worktree
	fakeNPM(t, false)
	sc.RunRef = "k2"
	if a, err := svc.Propose(ctx, sc, "deps_install", "web", "again"); err != nil || a.Status != "done" {
		t.Fatalf("install = %s %q %v", a.Status, a.Detail, err)
	}
	if _, err := os.Stat(filepath.Join(own, "fresh")); err != nil {
		t.Fatal("not installed")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(tree), ".deps-keep-*")); len(left) != 0 {
		t.Fatalf("saved copies left: %v", left)
	}
}

// A pip install's requirements files are checked when the card is made: one
// outside the package (.., absolute, an option) or missing is refused before
// a person is asked; a good one is cleaned and shows on the card.
func TestCheckDepsRejectsBadFiles(t *testing.T) {
	ctx, _, svc, proj := depsProject(t)
	os.MkdirAll(filepath.Join(proj.Path, "ml", "requirements"), 0o755)
	os.WriteFile(filepath.Join(proj.Path, "ml", "requirements.txt"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(proj.Path, "ml", "requirements", "dev.txt"), []byte("x\n"), 0o644)
	sc := Scope{ProjectID: proj.ID, RunRef: "f1", Agent: "a", Level: perm.Propose, Access: at(perm.Propose)}
	for _, f := range []string{"../secret.txt", "/etc/passwd", "--index-url=http://x", "requirements/../../x.txt", "missing.txt"} {
		args := storage.ActionArgs{Deps: &storage.DepsArgs{Dir: "ml", Manager: "pip", Files: []string{f}}}
		if a, err := svc.Propose(ctx, sc, "deps_install", "ml", "x", args); err == nil {
			t.Errorf("%q: card made (%s %q)", f, a.Status, a.Target)
		}
	}
	args := storage.ActionArgs{Deps: &storage.DepsArgs{Dir: "ml", Files: []string{"./requirements/dev.txt"}}}
	a, err := svc.Propose(ctx, sc, "deps_install", "ml", "dev deps", args)
	if err != nil || a.Target != "ml (pip: requirements/dev.txt)" || a.Args.Deps.Files[0] != "requirements/dev.txt" {
		t.Fatalf("good file: %v %q %v", err, a.Target, a.Args.Deps)
	}
}

// A merged diff that deleted one requirements file and changed another still
// gets its install card, for the file that is left; one that deleted all of
// them has nothing to install. Requirements files on a Node package are
// refused (pip would never read them).
func TestMergedDepsDropsDeletedFiles(t *testing.T) {
	ctx, st, svc, proj := depsProject(t)
	os.MkdirAll(filepath.Join(proj.Path, "ml"), 0o755)
	os.WriteFile(filepath.Join(proj.Path, "ml", "requirements.txt"), []byte("x\n"), 0o644)
	c1, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: proj.ID})
	sc := Scope{ProjectID: proj.ID, ConversationID: c1.ID, Agent: "office"}
	args := storage.ActionArgs{Deps: &storage.DepsArgs{Dir: "ml", Manager: "pip", Files: []string{"requirements-dev.txt", "requirements.txt"}}, Patch: "pat_1"}
	a, err := svc.Propose(ctx, sc, "deps_install", "ml", "merged", args)
	if err != nil || a.Target != "ml (pip: requirements.txt)" || len(a.Args.Deps.Files) != 1 {
		t.Fatalf("merged card = %v %q %+v", err, a.Target, a.Args.Deps)
	}
	gone := storage.ActionArgs{Deps: &storage.DepsArgs{Dir: "ml", Manager: "pip", Files: []string{"requirements-dev.txt"}}, Patch: "pat_2"}
	if a, err := svc.Propose(ctx, sc, "deps_install", "ml", "merged", gone); err == nil {
		t.Fatalf("a card with every file deleted: %s %q", a.Status, a.Target)
	}
	rsc := Scope{ProjectID: proj.ID, RunRef: "n1", Agent: "a", Level: perm.Propose, Access: at(perm.Propose)}
	os.WriteFile(filepath.Join(proj.Path, "web", "requirements.txt"), []byte("x\n"), 0o644)
	node := storage.ActionArgs{Deps: &storage.DepsArgs{Dir: "web", Files: []string{"requirements.txt"}}}
	if a, err := svc.Propose(ctx, rsc, "deps_install", "web", "x", node); err == nil {
		t.Fatalf("files on a Node package: %s %q", a.Status, a.Target)
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

	// failed in a worktree: it is not left with an empty node_modules, it
	// uses the project's again
	shared := filepath.Join(proj.Path, "web", "node_modules")
	os.MkdirAll(shared, 0o755)
	tree := t.TempDir()
	os.MkdirAll(filepath.Join(tree, "web"), 0o755)
	os.WriteFile(filepath.Join(tree, "web", "package.json"), []byte("{}"), 0o644)
	os.Symlink(shared, filepath.Join(tree, "web", "node_modules"))
	wsc := Scope{ProjectID: proj.ID, RunRef: "r9", Agent: "a", Level: perm.Check, Access: at(perm.Check), Dir: tree}
	w, err := svc.Propose(ctx, wsc, "deps_install", "web", "x", storage.ActionArgs{Deps: &storage.DepsArgs{Dir: "web", Manager: "npm"}})
	if err != nil || w.Status != "failed" {
		t.Fatalf("worktree failure = %s %v", w.Status, err)
	}
	if fi, err := os.Lstat(filepath.Join(tree, "web", "node_modules")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the worktree was left without the project's node_modules: %v", err)
	}
}
