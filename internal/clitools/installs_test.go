package clitools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// npmPrefixTool installs a fake npm global package of codex under prefix
// (<prefix>/lib/node_modules/…) printing version, and links it at link.
func npmPrefixTool(t *testing.T, prefix, link, version string) {
	t.Helper()
	pdir := filepath.Join(prefix, "lib", "node_modules", "@openai", "codex", "bin")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBin(t, pdir, "codex", "#!/bin/sh\necho codex-cli "+version+"\n")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(link)
	if err := os.Symlink(filepath.Join(pdir, "codex"), link); err != nil {
		t.Fatal(err)
	}
}

// A CLI no connection runs is not on the update card and not watched,
// though it is installed (ADR-280).
func TestUnusedCLINotWatched(t *testing.T) {
	m, dir := testManager(t)
	npmTool(t, dir, "codex", "@openai/codex", "1.0.0")
	m.SetUses(func(context.Context) map[string][]string { return map[string][]string{} })
	s, _ := m.Status(context.Background(), "codex")
	if !s.Installed || s.Used {
		t.Fatalf("installed=%v used=%v, want installed and not used", s.Installed, s.Used)
	}
	m.latest = map[string]latestEntry{"codex": {version: "2.0.0", at: m.clock(), ttl: LatestTTL}}
	if n, _ := m.Check(context.Background(), &AutoSetting{Mode: AutoNotify}); len(n) != 0 {
		t.Fatalf("notices for an unused CLI: %+v", n)
	}
}

// Two connections on two npm copies of the same CLI: an update reaches
// both, each in its own prefix, not only the one first on PATH (ADR-280).
func TestUpdateEveryInstall(t *testing.T) {
	m, dir := testManager(t)
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	first, second := filepath.Join(dir, "codex"), filepath.Join(b, "bin", "codex")
	npmPrefixTool(t, a, first, "1.0.0")
	npmPrefixTool(t, b, second, "1.0.0")
	// npm -g [--prefix P] pkg@latest: rewrites the package in P (a by default)
	writeBin(t, dir, "npm", `#!/bin/sh
prefix=`+a+`
while [ $# -gt 0 ]; do
  case "$1" in --prefix) prefix=$2; shift;; esac
  shift
done
printf '#!/bin/sh\necho codex-cli 2.0.0\n' > "$prefix/lib/node_modules/@openai/codex/bin/codex"
`)
	m.SetUses(func(context.Context) map[string][]string {
		return map[string][]string{"codex": {first, second}}
	})
	s, _ := m.Status(context.Background(), "codex")
	if !s.Used || len(s.Installs) != 2 {
		t.Fatalf("used=%v installs=%+v, want both copies", s.Used, s.Installs)
	}
	if _, err := m.UpdateNow("codex", "test"); err != nil {
		t.Fatal(err)
	}
	u := waitUpgrade(t, m, "codex", "done", "failed", "rolled_back")
	if u.State != "done" {
		t.Fatalf("upgrade = %+v", u)
	}
	for _, p := range []string{first, second} {
		if v := m.versionAt(p); v != "2.0.0" {
			t.Errorf("%s at %q, want 2.0.0", p, v)
		}
	}
}

// Without SetUses (no connections known) an installed CLI counts as used,
// on the copy first on PATH, as before.
func TestUsesDefault(t *testing.T) {
	m, dir := testManager(t)
	npmTool(t, dir, "codex", "@openai/codex", "1.0.0")
	s, _ := m.Status(context.Background(), "codex")
	if !s.Used || len(s.Installs) != 0 {
		t.Fatalf("used=%v installs=%+v", s.Used, s.Installs)
	}
}
