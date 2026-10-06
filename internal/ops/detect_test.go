package ops

import (
	"os"
	"path/filepath"
	"testing"
)

func w(t *testing.T, p, s string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()
	w(t, filepath.Join(root, "package.json"), `{"scripts":{"dev":"nuxt dev","build":"nuxt build","start":"node .output/server/index.mjs","postinstall":"nuxt prepare","test":"vitest run","test:watch":"vitest"}}`)
	w(t, filepath.Join(root, "pnpm-lock.yaml"), "")
	w(t, filepath.Join(root, "docker-compose.yml"), "services: {}")
	w(t, filepath.Join(root, "Makefile"), "build:\n\tgo build\nVAR := x\n.PHONY: build\n")
	w(t, filepath.Join(root, "apps/admin/package.json"), `{"scripts":{"dev":"vite"}}`)
	d := Detect(root)
	if d.PackageManager != "pnpm" || len(d.Compose) != 1 {
		t.Fatalf("%+v", d)
	}
	by := map[string]Suggestion{}
	for _, s := range d.Suggestions {
		by[s.Name] = s
	}
	if s := by["dev"]; s.Command != "pnpm dev" || s.Kind != "service" || !s.Recommended {
		t.Fatalf("dev: %+v", s)
	}
	if s := by["build"]; s.Kind != "job" {
		t.Fatalf("build: %+v", s)
	}
	if by["test:watch"].Kind != "service" || by["postinstall"].Name != "" {
		t.Fatalf("watch/postinstall: %+v", by)
	}
	if s := by["make build"]; s.Command != "make build" {
		t.Fatalf("make: %+v", by)
	}
	if s := by["apps/admin: dev"]; s.Cwd != "apps/admin" || s.Command != "npm run dev" {
		t.Fatalf("monorepo: %+v", s)
	}
}
