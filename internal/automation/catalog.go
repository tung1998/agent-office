package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Input is a value the user fills before installing (an env var or header).
type Input struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Kind     string `json:"kind"` // env | header | arg
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
	Default  string `json:"default,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// MCPTemplate is an installable MCP server definition. Config values may
// contain {{KEY}} placeholders filled from Inputs.
type MCPTemplate struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"` // default server name
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Category    string         `json:"category,omitempty"`
	Homepage    string         `json:"homepage,omitempty"`
	Config      map[string]any `json:"config"`
	Inputs      []Input        `json:"inputs"`
	Source      string         `json:"source"`         // catalog | registry | library
	Auth        string         `json:"auth,omitempty"` // oauth: the server asks to sign in on first use
}

var (
	placeholderRe = regexp.MustCompile(`\{\{[A-Za-z0-9_]+\}\}`)
	regVarRe      = regexp.MustCompile(`\{[A-Za-z0-9_]+\}`)
)

func stdio(cmd string, args ...string) map[string]any {
	a := make([]any, len(args))
	for i, s := range args {
		a[i] = s
	}
	return map[string]any{"type": "stdio", "command": cmd, "args": a}
}

func httpMCP(u string) map[string]any { return map[string]any{"type": "http", "url": u} }

// Catalog is a short curated list of widely used servers.
func Catalog() []MCPTemplate {
	gh := httpMCP("https://api.githubcopilot.com/mcp/")
	gh["headers"] = map[string]any{"Authorization": "Bearer {{GITHUB_TOKEN}}"}
	fsCfg := stdio("npx", "-y", "@modelcontextprotocol/server-filesystem", "{{DIR}}")
	list := []MCPTemplate{
		{ID: "context7", Name: "context7", Title: "Context7", Category: "Tài liệu", Description: "Tài liệu thư viện/framework mới nhất cho agent.", Homepage: "https://github.com/upstash/context7", Config: stdio("npx", "-y", "@upstash/context7-mcp")},
		{ID: "playwright", Name: "playwright", Title: "Playwright", Category: "Trình duyệt", Description: "Điều khiển trình duyệt: mở trang, click, chụp ảnh, kiểm thử UI.", Homepage: "https://github.com/microsoft/playwright-mcp", Config: stdio("npx", "@playwright/mcp@latest")},
		{ID: "chrome-devtools", Name: "chrome-devtools", Title: "Chrome DevTools", Category: "Trình duyệt", Description: "Đọc console, network, hiệu năng của Chrome.", Homepage: "https://github.com/ChromeDevTools/chrome-devtools-mcp", Config: stdio("npx", "-y", "chrome-devtools-mcp@latest")},
		{ID: "github", Name: "github", Title: "GitHub", Category: "Code", Description: "Issue, PR, repo trên GitHub.", Homepage: "https://github.com/github/github-mcp-server", Config: gh,
			Inputs: []Input{{Key: "GITHUB_TOKEN", Label: "GitHub token", Kind: "header", Secret: true, Required: true, Hint: "Personal access token"}}},
		{ID: "sentry", Name: "sentry", Title: "Sentry", Category: "Giám sát", Description: "Lỗi và issue từ Sentry.", Homepage: "https://docs.sentry.io/product/sentry-mcp/", Config: httpMCP("https://mcp.sentry.dev/mcp"), Auth: "oauth"},
		{ID: "atlassian", Name: "atlassian", Title: "Atlassian (Jira, Confluence)", Category: "Quản lý việc", Description: "Đọc/ghi Jira issue và trang Confluence.", Homepage: "https://www.atlassian.com/platform/remote-mcp-server", Config: httpMCP("https://mcp.atlassian.com/v1/mcp"), Auth: "oauth"},
		{ID: "linear", Name: "linear", Title: "Linear", Category: "Quản lý việc", Description: "Issue và project trên Linear.", Homepage: "https://linear.app/docs/mcp", Config: httpMCP("https://mcp.linear.app/mcp"), Auth: "oauth"},
		{ID: "notion", Name: "notion", Title: "Notion", Category: "Tài liệu", Description: "Trang và database Notion.", Homepage: "https://developers.notion.com/docs/mcp", Config: httpMCP("https://mcp.notion.com/mcp"), Auth: "oauth"},
		{ID: "supabase", Name: "supabase", Title: "Supabase", Category: "Dữ liệu", Description: "Database, bảng, SQL trên Supabase.", Homepage: "https://supabase.com/docs/guides/getting-started/mcp", Config: httpMCP("https://mcp.supabase.com/mcp"), Auth: "oauth"},
		{ID: "filesystem", Name: "filesystem", Title: "Filesystem", Category: "Hệ thống", Description: "Đọc/ghi file trong một thư mục được phép.", Homepage: "https://github.com/modelcontextprotocol/servers", Config: fsCfg,
			Inputs: []Input{{Key: "DIR", Label: "Thư mục cho phép", Kind: "arg", Required: true}}},
		{ID: "fetch", Name: "fetch", Title: "Fetch", Category: "Hệ thống", Description: "Tải trang web và chuyển sang Markdown (cần uv).", Homepage: "https://github.com/modelcontextprotocol/servers", Config: stdio("uvx", "mcp-server-fetch")},
	}
	for i := range list {
		list[i].Source = "catalog"
		if list[i].Inputs == nil {
			list[i].Inputs = []Input{}
		}
	}
	return list
}

// Render fills {{KEY}} placeholders. Missing required inputs are an error;
// unused optional env/header entries are dropped.
func Render(t MCPTemplate, values map[string]string) (map[string]any, error) {
	for _, in := range t.Inputs {
		if in.Required && strings.TrimSpace(values[in.Key]) == "" && in.Default == "" {
			return nil, fmt.Errorf("thiếu %s", in.Label)
		}
	}
	get := func(k string) string {
		if v := strings.TrimSpace(values[k]); v != "" {
			return v
		}
		for _, in := range t.Inputs {
			if in.Key == k {
				return in.Default
			}
		}
		return ""
	}
	var walk func(v any) (any, bool)
	walk = func(v any) (any, bool) {
		switch x := v.(type) {
		case string:
			empty := false
			out := placeholderRe.ReplaceAllStringFunc(x, func(m string) string {
				val := get(m[2 : len(m)-2])
				if val == "" {
					empty = true
				}
				return val
			})
			return out, !empty || !placeholderRe.MatchString(x)
		case []any:
			out := []any{}
			for _, e := range x {
				if r, ok := walk(e); ok {
					out = append(out, r)
				}
			}
			return out, true
		case map[string]any:
			out := map[string]any{}
			for k, e := range x {
				if r, ok := walk(e); ok {
					out[k] = r
				}
			}
			return out, true
		}
		return v, true
	}
	out, _ := walk(t.Config)
	return out.(map[string]any), nil
}

// Registry searches the official MCP registry.
type Registry struct {
	BaseURL string // default https://registry.modelcontextprotocol.io
	Client  *http.Client
}

type regServer struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Version     string `json:"version"`
	WebsiteURL  string `json:"websiteUrl"`
	Repository  struct {
		URL string `json:"url"`
	} `json:"repository"`
	Remotes []struct {
		Type    string     `json:"type"`
		URL     string     `json:"url"`
		Headers []regInput `json:"headers"`
	} `json:"remotes"`
	Packages []struct {
		RegistryType         string                     `json:"registryType"`
		Identifier           string                     `json:"identifier"`
		Version              string                     `json:"version"`
		RuntimeHint          string                     `json:"runtimeHint"`
		Transport            struct{ Type, URL string } `json:"transport"`
		EnvironmentVariables []regInput                 `json:"environmentVariables"`
		PackageArguments     []struct {
			Type       string `json:"type"`
			Name       string `json:"name"`
			Value      string `json:"value"`
			Default    string `json:"default"`
			IsRequired bool   `json:"isRequired"`
		} `json:"packageArguments"`
	} `json:"packages"`
}

type regInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Value       string `json:"value"`
	Default     string `json:"default"`
	IsSecret    bool   `json:"isSecret"`
	IsRequired  bool   `json:"isRequired"`
}

// Search returns install templates for servers matching q (latest versions).
func (r Registry) Search(ctx context.Context, q string, limit int) ([]MCPTemplate, error) {
	base := r.BaseURL
	if base == "" {
		base = "https://registry.modelcontextprotocol.io"
	}
	c := r.Client
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	if limit <= 0 || limit > 50 {
		limit = 30
	}
	u := fmt.Sprintf("%s/v0/servers?search=%s&limit=%d&version=latest", strings.TrimRight(base, "/"), url.QueryEscape(q), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("không gọi được MCP Registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP Registry trả về %s", resp.Status)
	}
	var body struct {
		Servers []struct {
			Server regServer `json:"server"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, err
	}
	out := []MCPTemplate{}
	seen := map[string]bool{}
	for _, s := range body.Servers {
		if seen[s.Server.Name] {
			continue
		}
		seen[s.Server.Name] = true
		if t, ok := fromRegistry(s.Server); ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// fromRegistry maps a registry entry to a template: a remote first (no local
// runtime needed), else an npm/pypi/oci stdio package.
func fromRegistry(s regServer) (MCPTemplate, bool) {
	short := s.Name
	if i := strings.LastIndex(short, "/"); i >= 0 {
		short = short[i+1:]
	}
	short = safeName(short)
	t := MCPTemplate{ID: s.Name, Name: short, Title: firstNonEmpty(s.Title, s.Name), Description: s.Description,
		Homepage: firstNonEmpty(s.WebsiteURL, s.Repository.URL), Source: "registry", Inputs: []Input{}}
	for _, rm := range s.Remotes {
		typ := "http"
		if rm.Type == "sse" {
			typ = "sse"
		}
		cfg := map[string]any{"type": typ, "url": rm.URL}
		if len(rm.Headers) > 0 {
			h := map[string]any{}
			for _, in := range rm.Headers {
				key := safeKey(in.Name)
				h[in.Name] = regValue(in.Value, key)
				t.Inputs = append(t.Inputs, Input{Key: key, Label: in.Name, Kind: "header", Secret: in.IsSecret, Required: in.IsRequired, Default: in.Default, Hint: in.Description})
			}
			cfg["headers"] = h
		}
		t.Config = cfg
		return t, true
	}
	for _, p := range s.Packages {
		if p.Transport.Type != "" && p.Transport.Type != "stdio" {
			continue
		}
		var cfg map[string]any
		switch p.RegistryType {
		case "npm":
			cfg = stdio("npx", "-y", p.Identifier+"@"+firstNonEmpty(p.Version, "latest"))
		case "pypi":
			id := p.Identifier
			if p.Version != "" {
				id += "==" + p.Version
			}
			cfg = stdio("uvx", id)
		case "oci":
			cfg = stdio("docker", "run", "-i", "--rm", p.Identifier)
		default:
			continue
		}
		args := cfg["args"].([]any)
		for _, a := range p.PackageArguments {
			if a.Type == "positional" {
				v := firstNonEmpty(a.Value, a.Default)
				if v == "" {
					key := safeKey(firstNonEmpty(a.Name, fmt.Sprintf("ARG%d", len(args))))
					v = "{{" + key + "}}"
					t.Inputs = append(t.Inputs, Input{Key: key, Label: firstNonEmpty(a.Name, key), Kind: "arg", Required: a.IsRequired})
				}
				args = append(args, v)
			} else if a.Type == "named" && a.Name != "" {
				if v := firstNonEmpty(a.Value, a.Default); v != "" {
					args = append(args, a.Name, v)
				}
			}
		}
		cfg["args"] = args
		if len(p.EnvironmentVariables) > 0 {
			env := map[string]any{}
			for _, e := range p.EnvironmentVariables {
				env[e.Name] = regValue(e.Value, e.Name)
				t.Inputs = append(t.Inputs, Input{Key: e.Name, Label: e.Name, Kind: "env", Secret: e.IsSecret, Required: e.IsRequired, Default: e.Default, Hint: e.Description})
			}
			cfg["env"] = env
		}
		t.Config = cfg
		return t, true
	}
	return t, false
}

// regValue keeps literal values and turns registry "{var}" templates into
// our {{KEY}} placeholder.
func regValue(v, key string) string {
	if v == "" || strings.Contains(v, "{") {
		if v != "" && strings.Contains(v, "{") {
			return regVarRe.ReplaceAllString(v, "{{"+key+"}}")
		}
		return "{{" + key + "}}"
	}
	return v
}

func safeKey(s string) string {
	s = strings.ToUpper(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, s))
	return strings.Trim(s, "_")
}

func safeName(s string) string {
	s = strings.ToLower(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, s))
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.Trim(s, "-.")
}
