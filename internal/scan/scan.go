// Package scan reads a project folder without running anything or calling a
// model: manifests, frameworks, services, infra, README, and the agent
// instruction files the team already has (CLAUDE.md, AGENTS.md, .claude/agents…).
// The result is a compact summary the setup assistant sends to the AI.
package scan

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// AgentDoc is an existing agent instruction file.
type AgentDoc struct {
	Path        string `json:"path"` // relative to the project
	Kind        string `json:"kind"` // instructions | subagent | skill | rules
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content"` // truncated
	Truncated   bool   `json:"truncated"`
}

// Summary is what the scan learned.
type Summary struct {
	Path         string         `json:"path"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Manifests    []string       `json:"manifests"`
	Dependencies []string       `json:"dependencies"` // notable package names
	Frameworks   []string       `json:"frameworks"`
	Services     []string       `json:"services"` // external services / SDKs
	Infra        []string       `json:"infra"`
	Languages    map[string]int `json:"languages"` // file counts
	TopDirs      []string       `json:"top_dirs"`
	EnvVars      []string       `json:"env_vars"` // names only, from .env.example
	AgentDocs    []AgentDoc     `json:"agent_docs"`
	Readme       string         `json:"readme"`
	FileCount    int            `json:"file_count"`
	Truncated    bool           `json:"truncated"` // walk stopped early
}

const (
	maxFiles     = 20000
	maxDocBytes  = 4000
	maxReadme    = 3000
	maxAgentDocs = 20
	maxDeps      = 60
)

var skip = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true, ".output": true, ".nuxt": true,
	".next": true, "target": true, "__pycache__": true, ".venv": true, "venv": true, ".idea": true, ".vscode": true,
	"coverage": true, ".cache": true, ".pnpm-store": true, ".office": true, "tmp": true, "logs": true,
}

var langByExt = map[string]string{
	".go": "Go", ".ts": "TypeScript", ".tsx": "TypeScript", ".js": "JavaScript", ".jsx": "JavaScript", ".mjs": "JavaScript",
	".vue": "Vue", ".svelte": "Svelte", ".php": "PHP", ".py": "Python", ".rb": "Ruby", ".java": "Java", ".kt": "Kotlin",
	".rs": "Rust", ".cs": "C#", ".swift": "Swift", ".dart": "Dart", ".sql": "SQL", ".scss": "SCSS", ".css": "CSS",
	".c": "C", ".cpp": "C++", ".ex": "Elixir",
}

// Scan inspects root.
func Scan(root string) (*Summary, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("scan: %s không phải thư mục", root)
	}
	s := &Summary{Path: abs, Name: filepath.Base(abs), Languages: map[string]int{}}
	deps := map[string]bool{}

	readManifests(abs, s, deps)
	s.Readme = readHead(firstExisting(abs, "README.md", "readme.md", "README", "README.rst"), maxReadme)
	s.EnvVars = envNames(firstExisting(abs, ".env.example", ".env.sample", ".env.dist"))
	s.AgentDocs = agentDocs(abs)

	entries, _ := os.ReadDir(abs)
	for _, e := range entries {
		if e.IsDir() && !skip[e.Name()] && !strings.HasPrefix(e.Name(), ".") {
			s.TopDirs = append(s.TopDirs, e.Name())
		}
	}

	infra := map[string]bool{}
	_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != abs && skip[name] {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(abs, p)
			switch rel {
			case ".github/workflows":
				infra["GitHub Actions"] = true
			case "k8s", "kubernetes", "helm", "charts":
				infra["Kubernetes"] = true
			case "terraform", "infra/terraform":
				infra["Terraform"] = true
			case "migrations", "database/migrations", "db/migrate", "prisma":
				infra["DB migrations ("+rel+")"] = true
			}
			return nil
		}
		s.FileCount++
		if s.FileCount > maxFiles {
			s.Truncated = true
			return filepath.SkipAll
		}
		if lang, ok := langByExt[strings.ToLower(filepath.Ext(name))]; ok {
			s.Languages[lang]++
		}
		switch {
		case name == "Dockerfile" || strings.HasPrefix(name, "Dockerfile."):
			infra["Dockerfile"] = true
		case strings.HasPrefix(name, "docker-compose") || strings.HasPrefix(name, "compose.y"):
			infra[name] = true
		case name == "bitbucket-pipelines.yml" || name == ".gitlab-ci.yml" || name == "Jenkinsfile":
			infra[name] = true
		case strings.HasSuffix(name, ".tf"):
			infra["Terraform"] = true
		case name == "vercel.json" || name == "netlify.toml" || name == "fly.toml" || name == "serverless.yml":
			infra[name] = true
		}
		return nil
	})
	s.Infra = keys(infra)

	s.Dependencies = keys(deps)
	if len(s.Dependencies) > maxDeps {
		s.Dependencies = s.Dependencies[:maxDeps]
	}
	s.Frameworks, s.Services = detect(deps, s.EnvVars)
	// JSON clients get [] rather than null for empty lists.
	for _, l := range []*[]string{&s.Manifests, &s.Dependencies, &s.Frameworks, &s.Services, &s.Infra, &s.TopDirs, &s.EnvVars} {
		if *l == nil {
			*l = []string{}
		}
	}
	if s.AgentDocs == nil {
		s.AgentDocs = []AgentDoc{}
	}
	return s, nil
}

// ---- manifests ----

func readManifests(root string, s *Summary, deps map[string]bool) {
	if raw, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		s.Manifests = append(s.Manifests, "package.json")
		var p struct {
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Deps        map[string]string `json:"dependencies"`
			DevDeps     map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(raw, &p) == nil {
			if p.Name != "" {
				s.Name = p.Name
			}
			s.Description = p.Description
			for k := range p.Deps {
				deps[k] = true
			}
			for k := range p.DevDeps {
				deps[k] = true
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "composer.json")); err == nil {
		s.Manifests = append(s.Manifests, "composer.json")
		var p struct {
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Require     map[string]string `json:"require"`
		}
		if json.Unmarshal(raw, &p) == nil {
			if s.Description == "" {
				s.Description = p.Description
			}
			for k := range p.Require {
				if !strings.HasPrefix(k, "ext-") && k != "php" {
					deps[k] = true
				}
			}
		}
	}
	if f, err := os.Open(filepath.Join(root, "go.mod")); err == nil {
		s.Manifests = append(s.Manifests, "go.mod")
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if m, ok := strings.CutPrefix(line, "module "); ok {
				s.Name = filepath.Base(strings.TrimSpace(m))
				continue
			}
			if fields := strings.Fields(strings.TrimPrefix(line, "require ")); len(fields) >= 2 && strings.Contains(fields[0], ".") && !strings.HasSuffix(line, "// indirect") {
				deps[fields[0]] = true
			}
		}
		f.Close()
	}
	for _, name := range []string{"requirements.txt", "pyproject.toml", "Cargo.toml", "Gemfile", "pom.xml", "build.gradle"} {
		p := filepath.Join(root, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		s.Manifests = append(s.Manifests, name)
		if name == "requirements.txt" {
			for _, l := range strings.Split(readHead(p, 8000), "\n") {
				l = strings.TrimSpace(l)
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				deps[strings.ToLower(regexp.MustCompile(`[<>=!~\[ ;]`).Split(l, 2)[0])] = true
			}
		}
	}
}

// detect maps dependency and env names to frameworks and external services.
func detect(deps map[string]bool, env []string) (frameworks, services []string) {
	hasDep := func(subs ...string) bool {
		for d := range deps {
			for _, s := range subs {
				if d == s || strings.HasPrefix(d, s+"/") || strings.Contains(d, s) {
					return true
				}
			}
		}
		return false
	}
	envHas := func(sub string) bool {
		for _, e := range env {
			if strings.Contains(e, sub) {
				return true
			}
		}
		return false
	}
	fw := map[string][]string{
		"Nuxt": {"nuxt"}, "Next.js": {"next"}, "React": {"react"}, "Vue": {"vue"}, "Svelte": {"svelte"}, "Angular": {"@angular/core"},
		"Express": {"express"}, "NestJS": {"@nestjs/core"}, "Laravel": {"laravel/framework"}, "Symfony": {"symfony/"},
		"Django": {"django"}, "FastAPI": {"fastapi"}, "Flask": {"flask"}, "Gin": {"github.com/gin-gonic/gin"},
		"Echo": {"github.com/labstack/echo"}, "Fiber": {"github.com/gofiber/fiber"}, "Rails": {"rails"}, "Tailwind CSS": {"tailwindcss"},
		"Playwright": {"playwright", "@playwright/test"}, "Vitest": {"vitest"}, "Jest": {"jest"}, "Prisma": {"prisma", "@prisma/client"},
	}
	for name, subs := range fw {
		exact := false
		for _, s := range subs {
			if deps[s] {
				exact = true
			}
		}
		if exact || (strings.Contains(subs[0], "/") && hasDep(subs...)) {
			frameworks = append(frameworks, name)
		}
	}
	svc := map[string]struct {
		deps []string
		env  string
	}{
		"Stripe": {[]string{"stripe"}, "STRIPE"}, "PayPal": {[]string{"paypal"}, "PAYPAL"}, "Sentry": {[]string{"sentry"}, "SENTRY"},
		"Redis": {[]string{"redis", "ioredis"}, "REDIS"}, "PostgreSQL": {[]string{"pg", "pgx", "postgres", "psycopg"}, "POSTGRES"},
		"MySQL": {[]string{"mysql"}, "MYSQL"}, "MongoDB": {[]string{"mongo"}, "MONGO"}, "Elasticsearch": {[]string{"elasticsearch"}, "ELASTIC"},
		"AWS": {[]string{"aws-sdk", "@aws-sdk", "boto3", "aws/aws-sdk"}, "AWS_"}, "Firebase": {[]string{"firebase"}, "FIREBASE"},
		"Supabase": {[]string{"supabase"}, "SUPABASE"}, "Graylog": {[]string{"graylog"}, "GRAYLOG"}, "Datadog": {[]string{"datadog", "dd-trace"}, "DATADOG"},
		"OpenAI": {[]string{"openai"}, "OPENAI"}, "Anthropic": {[]string{"anthropic"}, "ANTHROPIC"}, "Discord": {[]string{"discord"}, "DISCORD"},
		"Slack": {[]string{"slack"}, "SLACK"}, "Telegram": {[]string{"telegram"}, "TELEGRAM"}, "Kafka": {[]string{"kafka"}, "KAFKA"},
		"RabbitMQ": {[]string{"amqp", "rabbitmq"}, "RABBIT"}, "Cloudflare": {[]string{"cloudflare", "wrangler"}, "CLOUDFLARE"},
	}
	for name, v := range svc {
		if hasDep(v.deps...) || envHas(v.env) {
			services = append(services, name)
		}
	}
	sort.Strings(frameworks)
	sort.Strings(services)
	return frameworks, services
}

// ---- agent docs ----

var frontmatterRe = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*\n?`)

func agentDocs(root string) []AgentDoc {
	var out []AgentDoc
	add := func(rel, kind string) {
		if len(out) >= maxAgentDocs {
			return
		}
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return
		}
		d := AgentDoc{Path: rel, Kind: kind}
		content := string(raw)
		if m := frontmatterRe.FindStringSubmatch(content); m != nil {
			for _, line := range strings.Split(m[1], "\n") {
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				switch strings.TrimSpace(k) {
				case "name":
					d.Name = v
				case "description":
					d.Description = v
				}
			}
			content = content[len(m[0]):]
		}
		content = strings.TrimSpace(content)
		if len(content) > maxDocBytes {
			content, d.Truncated = cutUTF8(content, maxDocBytes), true
		}
		d.Content = content
		out = append(out, d)
	}
	for _, f := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", ".cursorrules", ".windsurfrules", ".github/copilot-instructions.md", ".clinerules"} {
		add(f, "instructions")
	}
	glob := func(pattern, kind string) {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		sort.Strings(matches)
		for _, m := range matches {
			if rel, err := filepath.Rel(root, m); err == nil {
				add(rel, kind)
			}
		}
	}
	glob(".claude/agents/*.md", "subagent")
	glob(".claude/skills/*/SKILL.md", "skill")
	glob(".cursor/rules/*.md*", "rules")
	glob(".agents/*.md", "subagent")
	return out
}

// ---- helpers ----

func envNames(path string) []string {
	if path == "" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(readHead(path, 16000), "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "export "))
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if k, _, ok := strings.Cut(l, "="); ok && regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(k) {
			out = append(out, k) // the value is never read into the summary
		}
	}
	return out
}

func firstExisting(root string, names ...string) string {
	for _, n := range names {
		p := filepath.Join(root, n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func readHead(path string, n int) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, n)
	k, _ := f.Read(buf)
	return cutUTF8(string(buf[:k]), n)
}

func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !isRuneStart(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	if len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Text renders the summary for the model. Values from .env files are never
// included; agent docs are quoted as data.
func (s *Summary) Text() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("Project: %s\n", s.Name)
	if s.Description != "" {
		w("Mô tả trong manifest: %s\n", s.Description)
	}
	list := func(label string, v []string) {
		if len(v) > 0 {
			w("%s: %s\n", label, strings.Join(v, ", "))
		}
	}
	list("Manifest", s.Manifests)
	list("Framework", s.Frameworks)
	list("Dịch vụ / SDK", s.Services)
	list("Hạ tầng", s.Infra)
	list("Thư mục gốc", s.TopDirs)
	list("Biến môi trường (chỉ tên)", s.EnvVars)
	list("Dependency đáng chú ý", s.Dependencies)
	if len(s.Languages) > 0 {
		type kv struct {
			k string
			v int
		}
		var ls []kv
		for k, v := range s.Languages {
			ls = append(ls, kv{k, v})
		}
		sort.Slice(ls, func(i, j int) bool { return ls[i].v > ls[j].v })
		parts := make([]string, 0, len(ls))
		for _, x := range ls {
			parts = append(parts, fmt.Sprintf("%s %d", x.k, x.v))
		}
		w("Ngôn ngữ (số file): %s\n", strings.Join(parts, ", "))
	}
	w("Tổng số file: %d\n", s.FileCount)
	if s.Readme != "" {
		w("\n### README (đầu file)\n%s\n", s.Readme)
	}
	for _, d := range s.AgentDocs {
		w("\n### File agent có sẵn: %s (%s)", d.Path, d.Kind)
		if d.Name != "" {
			w(" name=%s", d.Name)
		}
		if d.Description != "" {
			w(" description=%s", d.Description)
		}
		w("\n%s\n", d.Content)
		if d.Truncated {
			w("[…cắt bớt]\n")
		}
	}
	return b.String()
}
