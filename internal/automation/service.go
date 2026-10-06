package automation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Service is what the API uses.
type Service struct {
	Home      string
	Library   Library
	Installer Installer
	Registry  Registry
	Health    *MCPHealth // nil: no MCP health checks
	// Projects returns registered office projects: path → id.
	Projects func(ctx context.Context) map[string]string
	// Stash keeps the MCP servers office turned off (nil: no switch).
	Stash MCPStash
	mu    sync.Mutex // one switch at a time
}

func (s *Service) env(ctx context.Context) Env {
	e := Env{Home: s.Home, Projects: map[string]string{}}
	if s.Projects != nil {
		e.Projects = s.Projects(ctx)
	}
	if s.Stash != nil {
		e.Disabled, _ = s.Stash.Load(ctx)
	}
	return e
}

// Scan inventories the machine.
func (s *Service) Scan(ctx context.Context) Inventory { return Scan(s.env(ctx)) }

// Ref points at an installed item; it is resolved against a fresh scan so the
// client cannot make the office touch arbitrary paths.
type Ref struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Path        string `json:"path"`
	ProjectPath string `json:"project_path"`
}

func (s *Service) resolve(ctx context.Context, r Ref) (Item, error) {
	for _, it := range s.Scan(ctx).Items {
		if it.Kind == r.Kind && it.Name == r.Name && it.Location.Type == r.Type &&
			it.Location.Path == r.Path && it.Location.ProjectPath == r.ProjectPath {
			return it, nil
		}
	}
	return Item{}, ErrNotFound
}

// Remove uninstalls an item.
func (s *Service) Remove(ctx context.Context, r Ref) (string, error) {
	it, err := s.resolve(ctx, r)
	if err != nil {
		return "", err
	}
	if !it.Location.Editable {
		return "", ErrNotAllowed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if done, err := s.forgetDisabled(ctx, it); done || err != nil {
		return "", err
	}
	it.Disabled = false
	return s.Installer.Remove(ctx, it)
}

// Content returns an installed item's full content (skill files, agent
// markdown, or raw MCP config with secrets masked).
func (s *Service) Content(ctx context.Context, r Ref) (LibraryItem, error) {
	it, err := s.resolve(ctx, r)
	if err != nil {
		return LibraryItem{}, err
	}
	out := LibraryItem{Kind: it.Kind, Name: it.Name, Description: it.Description}
	switch it.Kind {
	case "skill":
		out.Files, err = ReadSkill(it.Location.Path)
	case "agent":
		var raw []byte
		raw, err = os.ReadFile(it.Location.Path)
		out.Files = map[string]string{filepath.Base(it.Location.Path): string(raw)}
	case "mcp":
		out.Template = &MCPTemplate{Name: it.Name, Title: it.Name, Config: it.Config, Inputs: []Input{}}
	}
	return out, err
}

// rawMCP reads an MCP server's unmasked config from its source file.
func (s *Service) rawMCP(it Item) (map[string]any, error) {
	var servers map[string]any
	if it.Disabled && it.Location.Type == "user" && s.Stash != nil { // office keeps it while off
		recs, err := s.Stash.Load(context.Background())
		if err != nil {
			return nil, err
		}
		var c map[string]any
		if rec, ok := recs[DisabledMCP{Type: "user", Name: it.Name}.Key()]; ok && json.Unmarshal(rec.Config, &c) == nil && c != nil {
			return c, nil
		}
		return nil, ErrNotFound
	}
	switch it.Location.Type {
	case "user", "cursor", "claude_desktop", "project":
		m := readJSON(it.Location.Path)
		servers, _ = m["mcpServers"].(map[string]any)
	case "local":
		m := readJSON(it.Location.Path)
		if ps, ok := m["projects"].(map[string]any); ok {
			if pc, ok := ps[it.Location.ProjectPath].(map[string]any); ok {
				servers, _ = pc["mcpServers"].(map[string]any)
			}
		}
	case "codex":
		return codexServer(it.Location.Path, it.Name)
	}
	c, ok := servers[it.Name].(map[string]any)
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

// InstallResult reports an install.
type InstallResult struct {
	Path     string    `json:"path,omitempty"`
	Findings []Finding `json:"findings"`
}

// InstallRequest installs from the library, the catalog/registry (template),
// or copies an installed item to another place.
type InstallRequest struct {
	Kind      string            `json:"kind"`
	Name      string            `json:"name"` // name to install as
	Target    Target            `json:"target"`
	Library   string            `json:"library,omitempty"`  // library entry name
	From      *Ref              `json:"from,omitempty"`     // copy an installed item
	Template  *MCPTemplate      `json:"template,omitempty"` // catalog/registry template
	Values    map[string]string `json:"values,omitempty"`   // template inputs
	Files     map[string]string `json:"files,omitempty"`    // a skill written in office's editor (path → content)
	Overwrite bool              `json:"overwrite"`
	Accept    bool              `json:"accept"` // accept safety warnings
	// Edited and Version: the installed item the editor opened, as it was
	// (the API answers 409 when it changed since, ADR-072)
	Edited  *Ref   `json:"edited,omitempty"`
	Version string `json:"version,omitempty"`
}

// Install runs an InstallRequest.
func (s *Service) Install(ctx context.Context, req InstallRequest) (InstallResult, error) {
	res := InstallResult{Findings: []Finding{}}
	var files map[string]string
	var mcp map[string]any
	switch {
	case req.Library != "":
		it, err := s.Library.Get(req.Kind, req.Library)
		if err != nil {
			return res, err
		}
		if req.Kind == "mcp" {
			if mcp, err = Render(*it.Template, req.Values); err != nil {
				return res, err
			}
		}
		files = it.Files
	case req.From != nil:
		it, err := s.resolve(ctx, *req.From)
		if err != nil {
			return res, err
		}
		switch it.Kind {
		case "skill":
			files, err = ReadSkill(it.Location.Path)
		case "agent":
			var raw []byte
			raw, err = os.ReadFile(it.Location.Path)
			files = map[string]string{"agent.md": string(raw)}
		case "mcp":
			mcp, err = s.rawMCP(it)
		}
		if err != nil {
			return res, err
		}
	case req.Files != nil && req.Kind == "skill":
		files = req.Files
	case req.Template != nil && req.Kind == "mcp":
		var err error
		if mcp, err = Render(*req.Template, req.Values); err != nil {
			return res, err
		}
	default:
		return res, errors.New("thiếu nguồn cài đặt")
	}
	if req.Name == "" {
		req.Name = firstNonEmpty(req.Library, func() string {
			if req.From != nil {
				return req.From.Name
			}
			if req.Template != nil {
				return req.Template.Name
			}
			return ""
		}())
	}

	if req.Kind == "skill" || req.Kind == "agent" {
		res.Findings = CheckContent(files)
		refuse, warn := Verdict(res.Findings)
		if refuse {
			return res, ErrUnsafeSkill
		}
		if warn && !req.Accept {
			return res, ErrNeedsConsent
		}
	}
	var err error
	switch req.Kind {
	case "skill":
		res.Path, err = s.Installer.InstallSkill(req.Target, req.Name, files, req.Overwrite)
	case "agent":
		var content string
		for _, c := range files {
			content = c
		}
		res.Path, err = s.Installer.InstallAgent(req.Target, req.Name, content, req.Overwrite)
	case "mcp":
		err = s.Installer.InstallMCP(ctx, req.Target, req.Name, mcp, req.Overwrite)
	default:
		err = errors.New("loại không hợp lệ")
	}
	return res, err
}

// SaveToLibrary copies an installed item into the library. MCP secrets are
// turned into placeholders so the library never holds credentials.
func (s *Service) SaveToLibrary(ctx context.Context, r Ref, name string) error {
	it, err := s.resolve(ctx, r)
	if err != nil {
		return err
	}
	if name == "" {
		name = it.Name
	}
	switch it.Kind {
	case "skill":
		files, err := ReadSkill(it.Location.Path)
		if err != nil {
			return err
		}
		return s.Library.SaveSkill(name, files)
	case "agent":
		raw, err := os.ReadFile(it.Location.Path)
		if err != nil {
			return err
		}
		return s.Library.SaveAgent(name, string(raw))
	case "mcp":
		raw, err := s.rawMCP(it)
		if err != nil {
			return err
		}
		t := Templatize(name, raw)
		t.Description = it.Description
		return s.Library.SaveMCP(t)
	}
	return errors.New("loại không hợp lệ")
}

// Templatize replaces env and header values with {{KEY}} inputs.
func Templatize(name string, cfg map[string]any) MCPTemplate {
	t := MCPTemplate{Name: name, Title: name, Inputs: []Input{}}
	out := map[string]any{}
	for k, v := range cfg {
		out[k] = v
	}
	for _, field := range []string{"env", "headers"} {
		m, ok := cfg[field].(map[string]any)
		if !ok {
			continue
		}
		nm := map[string]any{}
		for k, v := range m {
			key := safeKey(k)
			val := toString(v)
			// keep references like ${VAR} as they are: they hold no secret
			if strings.HasPrefix(val, "${") {
				nm[k] = val
				continue
			}
			kind := "env"
			if field == "headers" {
				kind = "header"
				if strings.HasPrefix(val, "Bearer ") {
					nm[k] = "Bearer {{" + key + "}}"
					t.Inputs = append(t.Inputs, Input{Key: key, Label: k, Kind: kind, Secret: true, Required: true})
					continue
				}
			}
			nm[k] = "{{" + key + "}}"
			t.Inputs = append(t.Inputs, Input{Key: key, Label: k, Kind: kind, Secret: secretKey.MatchString(k), Required: true})
		}
		out[field] = nm
	}
	t.Config = out
	return t
}
