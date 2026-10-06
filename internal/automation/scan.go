// Package automation manages the Claude Code building blocks a team shares:
// skills (.claude/skills/<name>/SKILL.md), subagents (.claude/agents/*.md) and
// MCP servers. It scans where they are installed (machine-wide, per project,
// plugins and other tools), installs them machine-wide or into a project, and
// removes them (to a recoverable trash).
package automation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Location says where an item lives.
type Location struct {
	Type        string `json:"type"` // user | project | local | plugin | cursor | claude_desktop | codex
	Label       string `json:"label"`
	Path        string `json:"path"`                   // file or folder of the item
	ProjectPath string `json:"project_path,omitempty"` // for project / local
	ProjectID   string `json:"project_id,omitempty"`   // when the folder is an office project
	Editable    bool   `json:"editable"`               // office may remove it
}

// Item is one installed skill, agent or MCP server.
type Item struct {
	Kind        string         `json:"kind"` // skill | agent | mcp
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Location    Location       `json:"location"`
	Meta        map[string]any `json:"meta,omitempty"`     // agent: tools, model; mcp: transport, command/url
	Config      map[string]any `json:"config,omitempty"`   // mcp config with secret values masked
	Disabled    bool           `json:"disabled,omitempty"` // mcp: turned off, Claude Code does not load it
}

// MachineProject is a folder Claude Code has opened on this machine.
type MachineProject struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Exists    bool   `json:"exists"`
	ProjectID string `json:"project_id,omitempty"` // registered in office
	Skills    int    `json:"skills"`
	Agents    int    `json:"agents"`
	MCP       int    `json:"mcp"`
	ClaudeMD  bool   `json:"claude_md"`
	AgentsMD  bool   `json:"agents_md"`
}

// Inventory is the result of a scan.
type Inventory struct {
	Items    []Item           `json:"items"`
	Projects []MachineProject `json:"projects"`
}

// Env tells the scanner where things are (tests point Home elsewhere).
type Env struct {
	Home string
	// Registered office projects: path → id.
	Projects map[string]string
	// Disabled: the MCP servers office turned off (mcptoggle.go).
	Disabled map[string]DisabledMCP
}

// markDisabled flags the items named in off.
func markDisabled(items []Item, off map[string]bool) []Item {
	for i := range items {
		items[i].Disabled = off[items[i].Name]
	}
	return items
}

var frontmatterRe = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*\n?`)

// Frontmatter reads simple `key: value` pairs from a Markdown header.
func Frontmatter(md string) (map[string]string, string) {
	out := map[string]string{}
	m := frontmatterRe.FindStringSubmatch(md)
	if m == nil {
		return out, md
	}
	for _, line := range strings.Split(m[1], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out, md[len(m[0]):]
}

func readText(p string, limit int64) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, _ := f.Read(buf)
	return string(buf[:n])
}

// scanSkills lists <dir>/<name>/SKILL.md.
func scanSkills(dir string, loc Location) []Item {
	entries, _ := os.ReadDir(dir)
	var out []Item
	for _, e := range entries {
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		skill := filepath.Join(dir, e.Name(), "SKILL.md")
		if _, err := os.Stat(skill); err != nil {
			continue
		}
		fm, _ := Frontmatter(readText(skill, 16<<10))
		l := loc
		l.Path = filepath.Join(dir, e.Name())
		out = append(out, Item{Kind: "skill", Name: firstNonEmpty(fm["name"], e.Name()), Description: fm["description"], Location: l})
	}
	return out
}

// scanAgents lists <dir>/*.md subagents.
func scanAgents(dir string, loc Location) []Item {
	files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(files)
	var out []Item
	for _, f := range files {
		fm, _ := Frontmatter(readText(f, 16<<10))
		l := loc
		l.Path = f
		meta := map[string]any{}
		if fm["tools"] != "" {
			meta["tools"] = fm["tools"]
		}
		if fm["model"] != "" {
			meta["model"] = fm["model"]
		}
		out = append(out, Item{Kind: "agent", Name: firstNonEmpty(fm["name"], strings.TrimSuffix(filepath.Base(f), ".md")), Description: fm["description"], Location: l, Meta: meta})
	}
	return out
}

var secretKey = regexp.MustCompile(`(?i)(key|token|secret|password|auth|cookie|credential|bearer)`)

// maskConfig hides env and header values (and secret-looking args) of an MCP config.
func maskConfig(c map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range c {
		switch k {
		case "env", "headers":
			if m, ok := v.(map[string]any); ok {
				masked := map[string]any{}
				for mk := range m {
					masked[mk] = "••••"
				}
				out[k] = masked
				continue
			}
		case "args":
			if arr, ok := v.([]any); ok {
				clean := make([]any, len(arr))
				for i, a := range arr {
					s, _ := a.(string)
					if i > 0 && secretKey.MatchString(toString(arr[i-1])) && !strings.HasPrefix(s, "-") {
						clean[i] = "••••"
					} else if strings.Contains(s, "=") && secretKey.MatchString(strings.SplitN(s, "=", 2)[0]) {
						clean[i] = strings.SplitN(s, "=", 2)[0] + "=••••"
					} else {
						clean[i] = a
					}
				}
				out[k] = clean
				continue
			}
		}
		out[k] = v
	}
	return out
}

func toString(v any) string { s, _ := v.(string); return s }

func mcpItems(servers map[string]any, loc Location) []Item {
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Item
	for _, n := range names {
		c, _ := servers[n].(map[string]any)
		if c == nil {
			continue
		}
		transport := toString(c["type"])
		if transport == "" {
			if c["url"] != nil {
				transport = "http"
			} else {
				transport = "stdio"
			}
		}
		meta := map[string]any{"transport": transport}
		if u := toString(c["url"]); u != "" {
			meta["url"] = u
		}
		if cmd := toString(c["command"]); cmd != "" {
			meta["command"] = cmd
		}
		out = append(out, Item{Kind: "mcp", Name: n, Location: loc, Meta: meta, Config: maskConfig(c)})
	}
	return out
}

func readJSON(p string) map[string]any {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// codexServers reads [mcp_servers.<name>] tables from Codex's TOML (names,
// command, url only; nested tool tables are ignored).
func codexServers(p string) map[string]any {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	out := map[string]any{}
	var cur map[string]any
	header := regexp.MustCompile(`^\[mcp_servers\.([^.\]]+)\]$`)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			cur = nil
			if m := header.FindStringSubmatch(line); m != nil {
				cur = map[string]any{}
				out[strings.Trim(m[1], `"`)] = cur
			}
			continue
		}
		if cur == nil {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`)
			switch k {
			case "command", "url":
				cur[k] = v
			}
		}
	}
	return out
}

// Scan inventories everything.
func Scan(env Env) Inventory {
	inv := Inventory{Items: []Item{}, Projects: []MachineProject{}}
	claudeDir := filepath.Join(env.Home, ".claude")
	user := Location{Type: "user", Label: "Toàn máy", Editable: true}
	inv.Items = append(inv.Items, scanSkills(filepath.Join(claudeDir, "skills"), user)...)
	inv.Items = append(inv.Items, scanAgents(filepath.Join(claudeDir, "agents"), user)...)

	// plugins (read-only): skills and agents shipped by installed plugins
	pluginSkills, _ := filepath.Glob(filepath.Join(claudeDir, "plugins", "cache", "*", "*", "*", "skills"))
	for _, d := range pluginSkills {
		name := pluginName(d)
		inv.Items = append(inv.Items, scanSkills(d, Location{Type: "plugin", Label: "Plugin " + name})...)
	}
	pluginAgents, _ := filepath.Glob(filepath.Join(claudeDir, "plugins", "cache", "*", "*", "*", "agents"))
	for _, d := range pluginAgents {
		inv.Items = append(inv.Items, scanAgents(d, Location{Type: "plugin", Label: "Plugin " + pluginName(d)})...)
	}

	claudeJSON := filepath.Join(env.Home, ".claude.json")
	cfg := readJSON(claudeJSON)
	userServers, _ := cfg["mcpServers"].(map[string]any)
	inv.Items = append(inv.Items, mcpItems(userServers, Location{Type: "user", Label: "Toàn máy", Path: claudeJSON, Editable: true})...)
	inv.Items = append(inv.Items, stashedUser(env.Disabled, claudeJSON, userServers)...)

	// project folders: those Claude Code knows plus office projects
	paths := map[string]bool{}
	if projects, ok := cfg["projects"].(map[string]any); ok {
		for p := range projects {
			paths[p] = true
		}
	}
	for p := range env.Projects {
		paths[p] = true
	}
	// git repos next to them: a folder of repos is seldom opened one by one
	for _, p := range siblingRepos(paths, env.Home) {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		if p == "" {
			continue
		}
		mp := MachineProject{Path: p, Name: filepath.Base(p), ProjectID: env.Projects[p]}
		st, err := os.Stat(p)
		mp.Exists = err == nil && st.IsDir()
		projLoc := Location{Type: "project", Label: "Project " + mp.Name, ProjectPath: p, ProjectID: mp.ProjectID, Editable: true}
		localLoc := Location{Type: "local", Label: "Riêng máy · " + mp.Name, ProjectPath: p, ProjectID: mp.ProjectID, Path: filepath.Join(env.Home, ".claude.json"), Editable: true}
		var items []Item
		if mp.Exists && p != env.Home {
			items = append(items, scanSkills(filepath.Join(p, ".claude", "skills"), projLoc)...)
			items = append(items, scanAgents(filepath.Join(p, ".claude", "agents"), projLoc)...)
			if m := readJSON(filepath.Join(p, ".mcp.json")); m != nil {
				if servers, ok := m["mcpServers"].(map[string]any); ok {
					l := projLoc
					l.Path = filepath.Join(p, ".mcp.json")
					off := disabledNames(readJSON(settingsLocal(p)), "disabledMcpjsonServers")
					items = append(items, markDisabled(mcpItems(servers, l), off)...)
				}
			}
			_, e1 := os.Stat(filepath.Join(p, "CLAUDE.md"))
			_, e2 := os.Stat(filepath.Join(p, "AGENTS.md"))
			mp.ClaudeMD, mp.AgentsMD = e1 == nil, e2 == nil
		}
		if projects, ok := cfg["projects"].(map[string]any); ok {
			if pc, ok := projects[p].(map[string]any); ok {
				if servers, ok := pc["mcpServers"].(map[string]any); ok {
					items = append(items, markDisabled(mcpItems(servers, localLoc), disabledNames(pc, "disabledMcpServers"))...)
				}
			}
		}
		for _, it := range items {
			switch it.Kind {
			case "skill":
				mp.Skills++
			case "agent":
				mp.Agents++
			case "mcp":
				mp.MCP++
			}
		}
		inv.Items = append(inv.Items, items...)
		if p != env.Home {
			inv.Projects = append(inv.Projects, mp)
		}
	}

	// other tools (read-only)
	if m := readJSON(filepath.Join(env.Home, ".cursor", "mcp.json")); m != nil {
		if servers, ok := m["mcpServers"].(map[string]any); ok {
			inv.Items = append(inv.Items, mcpItems(servers, Location{Type: "cursor", Label: "Cursor", Path: filepath.Join(env.Home, ".cursor", "mcp.json")})...)
		}
	}
	desktop := filepath.Join(env.Home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	if m := readJSON(desktop); m != nil {
		if servers, ok := m["mcpServers"].(map[string]any); ok {
			inv.Items = append(inv.Items, mcpItems(servers, Location{Type: "claude_desktop", Label: "Claude Desktop", Path: desktop})...)
		}
	}
	if servers := codexServers(filepath.Join(env.Home, ".codex", "config.toml")); len(servers) > 0 {
		inv.Items = append(inv.Items, mcpItems(servers, Location{Type: "codex", Label: "Codex", Path: filepath.Join(env.Home, ".codex", "config.toml")})...)
	}
	return inv
}

func pluginName(dir string) string {
	// .../plugins/cache/<marketplace>/<plugin>/<version>/skills
	parts := strings.Split(filepath.ToSlash(dir), "/")
	if len(parts) >= 4 {
		return parts[len(parts)-3]
	}
	return filepath.Base(filepath.Dir(dir))
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// siblingRepos: the git repos in the folders that hold the known projects
// (one level, no hidden folders). A worktree (.git is a file) is not a repo
// of its own.
func siblingRepos(known map[string]bool, home string) []string {
	parents := map[string]bool{}
	for p := range known {
		if p != "" && p != home {
			parents[filepath.Dir(p)] = true
		}
	}
	var out []string
	for dir := range parents {
		if dir == "/" || dir == filepath.Dir(home) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if st, err := os.Stat(filepath.Join(p, ".git")); err == nil && st.IsDir() && !known[p] {
				out = append(out, p)
			}
		}
	}
	return out
}
