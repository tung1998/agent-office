package deps

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const pkgNew = `{
  "name": "web",
  "private": true,
  "scripts": {
    "build": "nuxt build",
    "dev": "nuxt dev --port 3001",
    "lint": "eslint .",
    "test": "vitest run"
  },
  "dependencies": {
    "nuxt": "4.6.4",
    "vue": "3.5.0"
  }
}
`

// a scripts-only edit of web/package.json, as office's worktree diffs are
const scriptsDiff = `diff --git a/web/package.json b/web/package.json
index 1111111..2222222 100644
--- a/web/package.json
+++ b/web/package.json
@@ -3,7 +3,7 @@
   "private": true,
   "scripts": {
     "build": "nuxt build",
-    "dev": "nuxt dev",
+    "dev": "nuxt dev --port 3001",
     "lint": "eslint .",
     "test": "vitest run"
   },
`

// a dependency bump of the same file
const bumpDiff = `diff --git a/web/package.json b/web/package.json
--- a/web/package.json
+++ b/web/package.json
@@ -8,7 +8,7 @@
     "test": "vitest run"
   },
   "dependencies": {
-    "nuxt": "4.5.2",
+    "nuxt": "4.6.4",
     "vue": "3.5.0"
   }
 }
`

// A package.json whose diff only changes its scripts changes no dependency:
// read against the merged file (root) or from the diff alone (a card), it
// makes no install. A dependency bump, a lockfile, or a diff that cannot be
// read still does.
func TestChangesIgnoresScriptsOnly(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "web", "package.json"), pkgNew)
	write(t, filepath.Join(root, "web", "pnpm-lock.yaml"), "")
	for _, r := range []string{root, ""} {
		if got := ChangesOf(r, scriptsDiff, []string{"web/package.json"}); len(got) != 0 {
			t.Fatalf("root %q: scripts-only = %v", r, got)
		}
		if got := ChangesOf(r, bumpDiff, []string{"web/package.json"}); len(got) != 1 || got[0].Dir != "web" {
			t.Fatalf("root %q: bump = %v", r, got)
		}
		// the lockfile always counts
		if got := ChangesOf(r, scriptsDiff, []string{"web/package.json", "web/pnpm-lock.yaml"}); len(got) != 1 {
			t.Fatalf("root %q: scripts + lockfile = %v", r, got)
		}
		// no diff of the file (unknown): counts, as before
		if got := ChangesOf(r, "", []string{"web/package.json"}); len(got) != 1 {
			t.Fatalf("root %q: no diff = %v", r, got)
		}
	}
	// the project's file is the old one (a diff not merged yet): read forward
	old := strings.Replace(pkgNew, `"nuxt dev --port 3001"`, `"nuxt dev"`, 1)
	write(t, filepath.Join(root, "web", "package.json"), old)
	if got := ChangesOf(root, scriptsDiff, []string{"web/package.json"}); len(got) != 0 {
		t.Fatalf("scripts-only against the old file = %v", got)
	}
	// the file changed again since (the diff does not fit): read from the
	// diff's lines, as a card does
	write(t, filepath.Join(root, "web", "package.json"), "{}\n")
	if got := ChangesOf(root, scriptsDiff, []string{"web/package.json"}); len(got) != 0 {
		t.Fatalf("scripts-only diff that does not fit = %v", got)
	}
	if got := ChangesOf(root, bumpDiff, []string{"web/package.json"}); len(got) != 1 {
		t.Fatalf("bump diff that does not fit = %v", got)
	}
	// a new package.json counts
	created := "diff --git a/api/package.json b/api/package.json\nnew file mode 100644\n--- /dev/null\n+++ b/api/package.json\n@@ -0,0 +1 @@\n+{\"scripts\":{}}\n"
	if got := ChangesOf("", created, []string{"api/package.json"}); len(got) != 1 {
		t.Fatalf("new package.json = %v", got)
	}
	// packageManager is a dependency setting
	pm := "diff --git a/package.json b/package.json\n--- a/package.json\n+++ b/package.json\n@@ -1,3 +1,3 @@\n {\n   \"name\": \"x\",\n-  \"packageManager\": \"pnpm@9.0.0\",\n+  \"packageManager\": \"pnpm@9.1.0\",\n"
	if got := ChangesOf("", pm, []string{"package.json"}); len(got) != 1 {
		t.Fatalf("packageManager = %v", got)
	}
	// on one line (minified) the diff alone cannot tell: counts
	minified := "diff --git a/package.json b/package.json\n--- a/package.json\n+++ b/package.json\n@@ -1 +1 @@\n-{\"scripts\":{\"a\":\"x\"}}\n+{\"scripts\":{\"a\":\"y\"}}\n"
	if got := ChangesOf("", minified, []string{"package.json"}); len(got) != 1 {
		t.Fatalf("one-line package.json from the diff alone = %v", got)
	}
	r2 := t.TempDir()
	write(t, filepath.Join(r2, "package.json"), `{"scripts":{"a":"y"}}`+"\n")
	if got := ChangesOf(r2, minified, []string{"package.json"}); len(got) != 0 {
		t.Fatalf("one-line scripts-only against the file = %v", got)
	}
}

const pyNew = `[project]
name = "ml"
version = "0.2.0"
dependencies = [
    "requests>=2",
    "numpy",
]

[tool.ruff]
line-length = 110
`

// A pyproject.toml edit of a tool's settings is no dependency change; one of
// [project] dependencies is.
func TestChangesPyprojectToolOnly(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "ml", "pyproject.toml"), pyNew)
	write(t, filepath.Join(root, "ml", "uv.lock"), "")
	ruff := "diff --git a/ml/pyproject.toml b/ml/pyproject.toml\n--- a/ml/pyproject.toml\n+++ b/ml/pyproject.toml\n@@ -8,3 +8,3 @@\n \n [tool.ruff]\n-line-length = 100\n+line-length = 110\n"
	version := "diff --git a/ml/pyproject.toml b/ml/pyproject.toml\n--- a/ml/pyproject.toml\n+++ b/ml/pyproject.toml\n@@ -1,3 +1,3 @@\n [project]\n name = \"ml\"\n-version = \"0.1.0\"\n+version = \"0.2.0\"\n"
	dep := "diff --git a/ml/pyproject.toml b/ml/pyproject.toml\n--- a/ml/pyproject.toml\n+++ b/ml/pyproject.toml\n@@ -4,4 +4,5 @@\n dependencies = [\n     \"requests>=2\",\n+    \"numpy\",\n ]\n \n"
	for _, r := range []string{root, ""} {
		if got := ChangesOf(r, ruff, []string{"ml/pyproject.toml"}); len(got) != 0 {
			t.Fatalf("root %q: tool.ruff = %v", r, got)
		}
		if got := ChangesOf(r, version, []string{"ml/pyproject.toml"}); len(got) != 0 {
			t.Fatalf("root %q: version = %v", r, got)
		}
	}
	if got := ChangesOf(root, dep, []string{"ml/pyproject.toml"}); len(got) != 1 || got[0].Manager != "uv" {
		t.Fatalf("dependency added = %v", got)
	}
	// with uv.lock in the diff it counts whatever pyproject says
	if got := ChangesOf(root, ruff, []string{"ml/pyproject.toml", "ml/uv.lock"}); len(got) != 1 {
		t.Fatalf("with uv.lock = %v", got)
	}
}

// pip installs the requirements file the diff changed, not always
// requirements.txt: requirements-dev.txt, requirements/prod.txt.
func TestPipUsesChangedRequirementsFile(t *testing.T) {
	got := Changes("", []string{"ml/requirements-dev.txt", "svc/requirements/prod.txt", "svc/requirements/base.txt", "app/requirements.txt"})
	want := []Change{
		{Dir: "ml", Manager: "pip", Files: []string{"requirements-dev.txt"}},
		{Dir: "svc", Manager: "pip", Files: []string{"requirements/prod.txt", "requirements/base.txt"}},
		{Dir: "app", Manager: "pip"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %+v, want %+v", got, want)
	}
	if argv := strings.Join(Command(want[1]), " "); !strings.HasSuffix(argv, "pip install -r requirements/prod.txt -r requirements/base.txt") {
		t.Fatalf("command = %q", argv)
	}
	if argv := strings.Join(Command(want[2]), " "); !strings.HasSuffix(argv, "pip install -r requirements.txt") {
		t.Fatalf("default command = %q", argv)
	}
	if l := want[0].Label(); l != "ml (pip: requirements-dev.txt)" {
		t.Fatalf("label = %q", l)
	}

	fakeBin(t, map[string]string{"python3": `mkdir -p .venv/bin
printf '#!/bin/sh\necho pip "$@"\n' > .venv/bin/python
chmod +x .venv/bin/python
`})
	root := t.TempDir()
	write(t, filepath.Join(root, "ml", "requirements-dev.txt"), "pytest\n")
	out, err := Install(context.Background(), root, want[0])
	if err != nil || !strings.Contains(out, "pip -m pip install -r requirements-dev.txt") {
		t.Fatalf("install = %q, %v", out, err)
	}
	// a file outside the package is refused
	if _, err := Install(context.Background(), root, Change{Dir: "ml", Manager: "pip", Files: []string{"../x.txt"}}); err == nil {
		t.Fatal("a requirements file outside the package was accepted")
	}
}
