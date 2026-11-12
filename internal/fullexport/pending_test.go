package fullexport

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyCloneError(t *testing.T) {
	t.Parallel()
	for out, want := range map[string]string{
		"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.": ReasonAuth,
		"remote: Repository not found.\nfatal: repository 'https://github.com/x/y.git/' not found":      ReasonAuth,
		"fatal: unable to access 'https://x/': The requested URL returned error: 403":                   ReasonAuth,
		"ssh: Could not resolve hostname github.com: nodename nor servname provided":                    ReasonNetwork,
		"fatal: unable to access 'https://x/': Could not resolve host: x":                               ReasonNetwork,
		"fatal: '/tmp/gone.git' does not appear to be a git repository":                                 ReasonAuth,
		"something else": ReasonOther,
	} {
		if got := ClassifyCloneError(out); got != want {
			t.Errorf("%q → %s, want %s", out, got, want)
		}
	}
}

// ADR-279: a repo this machine cannot clone yet: the project's code waits,
// unpushed commits and uncommitted changes kept, until a retry (here with
// the repo's new place) or a folder cloned by hand takes them.
func TestCodeWaitsForAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	run(t, root, "init", "-q", "--bare", origin)
	src := filepath.Join(root, "shop")
	run(t, root, "clone", "-q", origin, src)
	write(t, filepath.Join(src, "a.txt"), "v1\n")
	run(t, src, "add", ".")
	run(t, src, "commit", "-qm", "pushed")
	run(t, src, "push", "-q", "origin", "HEAD:main")
	write(t, filepath.Join(src, "a.txt"), "v2\n")
	run(t, src, "commit", "-qam", "not pushed")
	write(t, filepath.Join(src, "a.txt"), "v3 dirty\n")
	write(t, filepath.Join(src, "new.md"), "untracked\n")

	var buf bytes.Buffer
	if _, err := ExportProjects(ctx, &buf, ProjectsExportOptions{Projects: []ProjectRef{{ID: "r1", Name: "Shop", Path: src}}, Passphrase: pass,
		Pack: func(string) ([]byte, error) { return []byte(`{"version":1}`), nil }}); err != nil {
		t.Fatal(err)
	}
	// the repo is out of reach from "the other machine"
	moved := filepath.Join(root, "moved.git")
	if err := os.Rename(origin, moved); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "new", ".office")
	st, err := StageProjects(ctx, &buf, pass, filepath.Join(home, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	p := st.Manifest.Projects[0]
	if reason, _ := ProbeRemote(ctx, p.Remote); reason == "" {
		t.Fatal("probe says a gone repo is reachable")
	}
	to := filepath.Join(root, "new", "shop")
	r := st.RestoreCode(ctx, p, to)
	if !r.NoCode || r.Reason == "" {
		t.Fatalf("restore of an unreachable repo = %+v", r)
	}
	if err := st.KeepCode(home, "rp_new", p, r); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(st.Dir) // the preview is gone; what waits must not be
	pc, ok, err := LoadPending(home, "rp_new")
	if err != nil || !ok || !pc.Project.Bundle || !pc.Project.Patch || pc.Project.Untracked != 1 {
		t.Fatalf("pending = %+v %v %v", pc, ok, err)
	}

	// retry with the same URL still fails, and says why again
	if ok, _, err := RetryPending(ctx, home, "rp_new", to, ""); ok || err != nil {
		t.Fatalf("retry on the same gone URL = %v %v", ok, err)
	}
	// retry with the repo's new place: cloned, and what was kept is back
	ok, note, err := RetryPending(ctx, home, "rp_new", to, moved)
	if !ok || err != nil {
		t.Fatalf("retry = %v %q %v", ok, note, err)
	}
	if got := read(t, filepath.Join(to, "a.txt")); got != "v3 dirty\n" {
		t.Errorf("a.txt = %q", got)
	}
	if !strings.Contains(run(t, to, "log", "--format=%s"), "not pushed") || read(t, filepath.Join(to, "new.md")) != "untracked\n" {
		t.Error("unpushed commit or untracked file lost")
	}
	if _, waiting, _ := LoadPending(home, "rp_new"); waiting {
		t.Error("still waiting after a good clone")
	}
}

// A folder the person cloned by hand takes what was kept, without touching
// their own files; a folder of another repo is refused.
func TestAttachClonedFolder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	run(t, root, "init", "-q", "--bare", origin)
	src := filepath.Join(root, "src")
	run(t, root, "clone", "-q", origin, src)
	write(t, filepath.Join(src, "a.txt"), "v1\n")
	run(t, src, "add", ".")
	run(t, src, "commit", "-qm", "base")
	run(t, src, "push", "-q", "origin", "HEAD:main")
	write(t, filepath.Join(src, "a.txt"), "local change\n")
	write(t, filepath.Join(src, "mine.md"), "from export\n")

	dir := filepath.Join(root, "stage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := Project{ID: "r", Name: "R", Path: src, Slug: "r"}
	if err := describeProject(ctx, &p, dir, ""); err != nil {
		t.Fatal(err)
	}
	for _, rel := range untrackedFiles(ctx, src, "") {
		write(t, filepath.Join(dir, "untracked", rel), read(t, filepath.Join(src, rel)))
	}
	home := filepath.Join(root, ".office")
	if err := KeepPending(home, "rp", p, dir, restored{NoCode: true, Reason: ReasonAuth}); err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(root, "other")
	run(t, root, "init", "-q", other)
	if _, err := AttachPending(ctx, home, "rp", other); err == nil {
		t.Fatal("a folder of another repo was taken")
	}
	mine := filepath.Join(root, "byhand")
	run(t, root, "clone", "-q", origin, mine)
	write(t, filepath.Join(mine, "mine.md"), "the person's own\n")
	if _, err := AttachPending(ctx, home, "rp", mine); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(mine, "a.txt")) != "local change\n" {
		t.Error("the uncommitted change was not applied")
	}
	if read(t, filepath.Join(mine, "mine.md")) != "the person's own\n" {
		t.Error("an untracked file of the person was overwritten")
	}
}

// Once the repo is reachable again, the waiting project gets its code with
// nobody pressing a button; a project deleted meanwhile stops waiting.
func TestRetryReachable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	run(t, root, "init", "-q", "--bare", origin)
	src := filepath.Join(root, "src")
	run(t, root, "clone", "-q", origin, src)
	write(t, filepath.Join(src, "a.txt"), "v1\n")
	run(t, src, "add", ".")
	run(t, src, "commit", "-qm", "base")
	run(t, src, "push", "-q", "origin", "HEAD:main")
	home := filepath.Join(root, ".office")
	p := Project{ID: "r", Name: "R", Kind: KindRemote, Remote: origin, Branch: "main", Head: strings.TrimSpace(run(t, src, "rev-parse", "HEAD"))}
	_ = KeepPending(home, "waits", p, filepath.Join(root, "nothing"), restored{NoCode: true, Reason: ReasonAuth})
	_ = KeepPending(home, "deleted", p, filepath.Join(root, "nothing"), restored{NoCode: true, Reason: ReasonAuth})
	to := filepath.Join(root, "new")
	_ = os.MkdirAll(to, 0o755)
	moved := origin + ".away"
	_ = os.Rename(origin, moved)
	pathOf := func(id string) (string, error) {
		if id == "deleted" {
			return "", nil
		}
		return to, nil
	}
	if done := RetryReachable(ctx, home, pathOf); len(done) != 0 {
		t.Fatalf("cloned an unreachable repo: %v", done)
	}
	if _, ok, _ := LoadPending(home, "deleted"); ok {
		t.Error("a deleted project still waits")
	}
	_ = os.Rename(moved, origin) // access is back
	if done := RetryReachable(ctx, home, pathOf); len(done) != 1 || done[0] != "waits" {
		t.Fatalf("done = %v", done)
	}
	if read(t, filepath.Join(to, "a.txt")) != "v1\n" {
		t.Error("code not cloned")
	}
}

// A sync over a project that has its code here: the other machine's
// unpushed commits come as office-export/* branches, its uncommitted change
// is applied only when clean (else kept as a patch), the work here untouched.
func TestMergeIntoExistingClone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	run(t, root, "init", "-q", "--bare", origin)
	src := filepath.Join(root, "src")
	run(t, root, "clone", "-q", origin, src)
	write(t, filepath.Join(src, "a.txt"), "v1\n")
	write(t, filepath.Join(src, "b.txt"), "b1\n")
	run(t, src, "add", ".")
	run(t, src, "commit", "-qm", "base")
	run(t, src, "push", "-q", "origin", "HEAD:main")
	here := filepath.Join(root, "here")
	run(t, root, "clone", "-q", origin, here)
	write(t, filepath.Join(here, "mine.txt"), "work here\n")

	write(t, filepath.Join(src, "a.txt"), "v2\n")
	run(t, src, "commit", "-qam", "unpushed there")
	write(t, filepath.Join(src, "b.txt"), "b2 dirty\n")
	var buf bytes.Buffer
	if _, err := ExportProjects(ctx, &buf, ProjectsExportOptions{Projects: []ProjectRef{{ID: "r", Name: "R", Path: src}}, Passphrase: pass,
		Pack: func(string) ([]byte, error) { return []byte(`{"version":1}`), nil }}); err != nil {
		t.Fatal(err)
	}
	st, err := StageProjects(ctx, &buf, pass, filepath.Join(root, "stage"))
	if err != nil {
		t.Fatal(err)
	}
	note := st.MergeInto(ctx, st.Manifest.Projects[0], here)
	if !strings.Contains(run(t, here, "log", "--format=%s", "office-export/main"), "unpushed there") {
		t.Fatalf("unpushed commit not brought as a branch (%s)", note)
	}
	if read(t, filepath.Join(here, "b.txt")) != "b2 dirty\n" {
		t.Errorf("the clean uncommitted change was not applied: %s", note)
	}
	if read(t, filepath.Join(here, "mine.txt")) != "work here\n" || read(t, filepath.Join(here, "a.txt")) != "v1\n" {
		t.Error("the work here was touched")
	}
	other := filepath.Join(root, "other")
	run(t, root, "init", "-q", other)
	if n := st.MergeInto(ctx, st.Manifest.Projects[0], other); !strings.Contains(n, "khác repo") {
		t.Errorf("merge into another repo: %q", n)
	}
}
