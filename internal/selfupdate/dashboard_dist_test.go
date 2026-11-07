package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCopyDashboardDist checks the helper the "Nhúng dashboard vào server"
// step uses: it replaces dst with src's contents and keeps dst usable as a
// `//go:embed dist` directory (the .gitkeep placeholder) even after a real
// build landed there.
func TestCopyDashboardDist(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "public")
	dst := filepath.Join(root, "dist")
	mustWrite(t, filepath.Join(src, "index.html"), "<html>v2</html>")
	mustWrite(t, filepath.Join(src, "assets", "app.css"), "body{}")
	mustWrite(t, dst, "") // dst pre-exists with stale content (prior build / placeholder)
	if err := os.Remove(dst); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dst, ".gitkeep"), "")
	mustWrite(t, filepath.Join(dst, "index.html"), "<html>v1 (stale)</html>")

	if err := copyDashboardDist(src, dst); err != nil {
		t.Fatalf("copyDashboardDist: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<html>v2</html>" {
		t.Errorf("index.html = %q, want the new build's content", got)
	}
	if _, err := os.ReadFile(filepath.Join(dst, "assets", "app.css")); err != nil {
		t.Errorf("assets/app.css missing after copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".gitkeep")); err != nil {
		t.Errorf(".gitkeep missing after copy, the embed directive would fail on a clean dist/: %v", err)
	}
}

// TestCopyDashboardDistRequiresIndexHTML guards against silently embedding
// an incomplete or wrong directory (e.g. a build that failed partway).
func TestCopyDashboardDistRequiresIndexHTML(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "public")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyDashboardDist(src, filepath.Join(root, "dist")); err == nil {
		t.Fatal("expected an error for a public/ with no index.html")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunEmbedsDashboardBeforeBuildingServer proves the step order: the
// dashboard build is copied into internal/webui/dist before "Build server"
// runs, so the new binary's go:embed picks up the new dashboard rather than
// a stale one. The fake "go" on PATH refuses to "build" until it sees the
// copied dist, so a regression back to the old order fails this test instead
// of silently shipping an out-of-date embedded dashboard.
func TestRunEmbedsDashboardBeforeBuildingServer(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	uiDir := filepath.Join(root, "dashboard")
	if err := os.MkdirAll(filepath.Join(uiDir, "node_modules"), 0o755); err != nil {
		t.Fatal(err) // present so the "Cài package dashboard" step is skipped
	}
	distCheck := filepath.Join(root, "internal", "webui", "dist", "index.html")

	fakeBin := t.TempDir()
	mustWriteScript(t, filepath.Join(fakeBin, "go"), `#!/bin/sh
if [ "$1" = "build" ]; then
  if [ ! -f "`+distCheck+`" ]; then
    echo "go build ran before the dashboard was embedded" >&2
    exit 1
  fi
  for a in "$@"; do
    if [ "$prev" = "-o" ]; then echo built > "$a"; fi
    prev="$a"
  done
  exit 0
fi
exit 0
`)
	mustWriteScript(t, filepath.Join(fakeBin, "npx"), `#!/bin/sh
mkdir -p .output.new/public
echo "<html>fake build</html>" > .output.new/public/index.html
exit 0
`)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	src := Source{Root: root, UIDir: uiDir}
	u := New(src, t.TempDir(), nil, func() {})
	u.run(Options{Test: false})

	st := u.State()
	if st.Status == "failed" {
		t.Fatalf("update failed: %s", st.Error)
	}
	if _, err := os.Stat(distCheck); err != nil {
		t.Fatalf("internal/webui/dist/index.html not populated: %v", err)
	}
}

func mustWriteScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
