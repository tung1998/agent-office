package scan_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/scan"
)

func write(t *testing.T, root, rel, content string) {
	p := filepath.Join(root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"name":"shop","description":"Online shop","dependencies":{"nuxt":"^4","@stripe/stripe-js":"1","ioredis":"5"},"devDependencies":{"vitest":"1"}}`)
	write(t, root, "README.md", "# Shop\n\nStorefront for our brand.\n")
	write(t, root, "CLAUDE.md", "Always run pnpm test before commit.")
	write(t, root, "AGENTS.md", "Use conventional commits.")
	write(t, root, ".claude/agents/pr-reviewer.md", "---\nname: pr-reviewer\ndescription: Reviews PRs\n---\nReview every PR for bugs.")
	write(t, root, ".cursor/rules/style.mdc", "Prefer composition API.")
	write(t, root, "docker-compose.yml", "services: {}")
	write(t, root, ".github/workflows/ci.yml", "on: push")
	write(t, root, "app/pages/index.vue", "<template/>")
	write(t, root, "server/api/x.ts", "export {}")
	write(t, root, "node_modules/big/index.js", "x")
	write(t, root, ".env.example", "STRIPE_KEY=sk_live_secret\nREDIS_URL=redis://x\n")

	s, err := scan.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	has := func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	}
	if s.Name != "shop" || s.Description != "Online shop" {
		t.Fatalf("name/desc = %q %q", s.Name, s.Description)
	}
	if !has(s.Frameworks, "Nuxt") || !has(s.Services, "Stripe") || !has(s.Services, "Redis") {
		t.Fatalf("frameworks=%v services=%v", s.Frameworks, s.Services)
	}
	if !has(s.Infra, "docker-compose.yml") || !has(s.Infra, "GitHub Actions") {
		t.Fatalf("infra = %v", s.Infra)
	}
	if len(s.AgentDocs) != 4 {
		t.Fatalf("agent docs = %+v", s.AgentDocs)
	}
	var sub *scan.AgentDoc
	for i := range s.AgentDocs {
		if s.AgentDocs[i].Kind == "subagent" {
			sub = &s.AgentDocs[i]
		}
	}
	if sub == nil || sub.Name != "pr-reviewer" || sub.Description != "Reviews PRs" {
		t.Fatalf("subagent = %+v", sub)
	}
	if !has(s.EnvVars, "STRIPE_KEY") {
		t.Fatalf("env vars = %v", s.EnvVars)
	}
	if strings.Contains(s.Text(), "sk_live_secret") {
		t.Fatal("env values must never be included")
	}
	if strings.Contains(strings.Join(s.TopDirs, ","), "node_modules") {
		t.Fatalf("node_modules not skipped: %v", s.TopDirs)
	}
	if s.Languages["Vue"] == 0 || s.Languages["TypeScript"] == 0 {
		t.Fatalf("languages = %v", s.Languages)
	}
	if !strings.Contains(s.Text(), "Review every PR for bugs.") || len(s.Text()) > 40000 {
		t.Fatalf("text too long or missing docs: %d", len(s.Text()))
	}
}
