package perm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/perm"
)

// fakeHome sets $HOME to a fresh temp dir for the test and returns it.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestCheckExtraDirRejects(t *testing.T) {
	home := fakeHome(t)
	projectParent := t.TempDir()
	project := filepath.Join(projectParent, "shop")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link-to-ssh")
	if err := os.Symlink(sshDir, link); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"root":              "/",
		"home":              home,
		"project's parent":  projectParent,
		"~/.ssh":            sshDir,
		"symlink to ~/.ssh": link,
	}
	for name, dir := range cases {
		if err := perm.CheckExtraDir(project, dir); err == nil {
			t.Errorf("%s: expected rejection for %q, got no error", name, dir)
		}
	}
}

func TestCheckExtraDirAccepts(t *testing.T) {
	fakeHome(t)
	projectParent := t.TempDir()
	project := filepath.Join(projectParent, "shop")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir() // a sibling repo: not an ancestor/descendant of project or home
	if err := perm.CheckExtraDir(project, other); err != nil {
		t.Fatalf("a valid sibling dir rejected: %v", err)
	}
}

// ADR-074 TOCTOU fix: a dir valid when saved can turn dangerous after a
// symlink is repointed; FilterValidExtraDirs re-checks it right before a run
// and drops it instead of trusting the stored path.
func TestFilterValidExtraDirsDropsRepointedSymlink(t *testing.T) {
	home := fakeHome(t)
	project := t.TempDir()
	other := t.TempDir() // the symlink's original, harmless target
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "extra-dir-link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	// valid when first checked (points at a harmless temp dir)
	if got := perm.FilterValidExtraDirs(project, []string{link}); len(got) != 1 {
		t.Fatalf("a valid symlink was dropped: %v", got)
	}
	// the attack: repoint the same path, already saved, at ~/.ssh
	os.Remove(link)
	if err := os.Symlink(sshDir, link); err != nil {
		t.Fatal(err)
	}
	if got := perm.FilterValidExtraDirs(project, []string{link}); len(got) != 0 {
		t.Fatalf("repointed symlink to ~/.ssh was not dropped: %v", got)
	}
}

// ADR-074: ~/.agent-office is office's own data folder (office.db,
// secret.key, every project's data office manages) — never a safe extra dir.
func TestCheckExtraDirRejectsAgentOfficeHome(t *testing.T) {
	home := fakeHome(t)
	project := t.TempDir()
	officeHome := filepath.Join(home, ".agent-office")
	if err := os.MkdirAll(officeHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := perm.CheckExtraDir(project, officeHome); err == nil {
		t.Fatal("~/.agent-office was accepted")
	}
	sub := filepath.Join(officeHome, "projects", "x")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := perm.CheckExtraDir(project, sub); err == nil {
		t.Fatal("a folder under ~/.agent-office was accepted")
	}
}

// ADR-074: a ".office" folder anywhere on the path — this project's or
// another project's — always holds office's data/secrets, never safe to read.
func TestCheckExtraDirRejectsAnyDotOfficeFolder(t *testing.T) {
	fakeHome(t)
	project := t.TempDir()
	otherProjectOffice := filepath.Join(t.TempDir(), "other-project", ".office")
	if err := os.MkdirAll(otherProjectOffice, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := perm.CheckExtraDir(project, otherProjectOffice); err == nil {
		t.Fatal("another project's .office was accepted")
	}
	// even nested deeper under it
	deeper := filepath.Join(otherProjectOffice, "data", "x")
	if err := os.MkdirAll(deeper, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := perm.CheckExtraDir(project, deeper); err == nil {
		t.Fatal("a folder nested under another project's .office was accepted")
	}
}

// A dir unrelated to .office/.agent-office is still accepted, same as before.
func TestCheckExtraDirAcceptsUnrelatedDir(t *testing.T) {
	fakeHome(t)
	project := t.TempDir()
	other := t.TempDir()
	if err := perm.CheckExtraDir(project, other); err != nil {
		t.Fatalf("an unrelated dir was rejected: %v", err)
	}
}

func TestCheckExtraDirRejectsRelativeAndMissing(t *testing.T) {
	fakeHome(t)
	project := t.TempDir()
	if err := perm.CheckExtraDir(project, "relative/dir"); err == nil {
		t.Fatal("a relative path was accepted")
	}
	if err := perm.CheckExtraDir(project, filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("a missing dir was accepted")
	}
}
