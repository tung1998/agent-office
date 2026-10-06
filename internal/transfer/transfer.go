// Package transfer exports the office configuration (AI connections without
// secrets, templates, projects and their models) and imports it back, on this
// or another machine. Agents refer to connections by name so the bundle is
// portable; users, sessions and keys are never exported.
package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
)

// Version of the bundle format.
const Version = 1

// Bundle is the whole exported configuration.
type Bundle struct {
	Version    int             `json:"version"`
	ExportedAt *time.Time      `json:"exported_at,omitempty"`
	Providers  []ProviderSpec  `json:"providers"`
	Templates  []TemplateEntry `json:"templates"`
	Projects   []ProjectSpec   `json:"projects"`
}

// ProviderSpec is a connection without its secret.
type ProviderSpec struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Preset     string            `json:"preset,omitempty"`
	BaseURL    string            `json:"base_url,omitempty"`
	APIKeyEnv  string            `json:"api_key_env,omitempty"`
	TierModels map[string]string `json:"tier_models,omitempty"`
	IsDefault  bool              `json:"is_default,omitempty"`
	Enabled    bool              `json:"enabled"`
	// HadStoredKey tells the importer a key must be entered again.
	HadStoredKey bool `json:"had_stored_key,omitempty"`
}

// TemplateEntry is a library template.
type TemplateEntry struct {
	Builtin  bool              `json:"builtin,omitempty"`
	Template orgmodel.Template `json:"template"`
}

// ProjectSpec is a project and its model.
type ProjectSpec struct {
	Name           string             `json:"name"`
	Path           string             `json:"path,omitempty"` // empty: machine-wide helper
	GitRemote      string             `json:"git_remote,omitempty"`
	Description    string             `json:"description,omitempty"`
	SourceTemplate string             `json:"source_template,omitempty"` // library key it came from
	Model          *orgmodel.Template `json:"model,omitempty"`
}

// Service exports and imports.
type Service struct {
	store     storage.Store
	providers *provider.Service
	org       *orgmodel.Service
}

// New builds a Service.
func New(store storage.Store, providers *provider.Service, org *orgmodel.Service) *Service {
	return &Service{store: store, providers: providers, org: org}
}

// Export builds the bundle.
func (s *Service) Export(ctx context.Context) (Bundle, error) {
	b := Bundle{Version: Version, Providers: []ProviderSpec{}, Templates: []TemplateEntry{}, Projects: []ProjectSpec{}}
	provs, err := s.store.Providers().List(ctx)
	if err != nil {
		return b, err
	}
	names := map[string]string{}
	for _, p := range provs {
		names[p.ID] = p.Name
		b.Providers = append(b.Providers, ProviderSpec{
			Name: p.Name, Kind: string(p.Kind), Preset: p.Preset, BaseURL: p.BaseURL, APIKeyEnv: p.APIKeyEnv, TierModels: p.TierModels,
			IsDefault: p.IsDefault, Enabled: p.Enabled, HadStoredKey: p.APIKeyEnc != "",
		})
	}
	sort.Slice(b.Providers, func(i, j int) bool { return b.Providers[i].Name < b.Providers[j].Name })

	tpls, err := s.store.OrgModels().ListTemplates(ctx)
	if err != nil {
		return b, err
	}
	keyByID := map[string]string{}
	for _, m := range tpls {
		keyByID[m.ID] = m.Key
		t, err := s.org.Load(ctx, m.ID)
		if err != nil {
			return b, err
		}
		b.Templates = append(b.Templates, TemplateEntry{Builtin: m.Builtin, Template: portable(t, names)})
	}
	sort.Slice(b.Templates, func(i, j int) bool { return b.Templates[i].Template.Key < b.Templates[j].Template.Key })

	repos, err := s.store.Repos().List(ctx)
	if err != nil {
		return b, err
	}
	for _, r := range repos {
		p := ProjectSpec{Name: r.Name, Path: r.Path, GitRemote: r.GitRemote, Description: r.Description}
		if m, err := s.store.OrgModels().GetForRepo(ctx, r.ID); err == nil {
			t, err := s.org.Load(ctx, m.ID)
			if err != nil {
				return b, err
			}
			pt := portable(t, names)
			p.Model, p.SourceTemplate = &pt, keyByID[m.SourceTemplateID]
		} else if !errors.Is(err, storage.ErrNotFound) {
			return b, err
		}
		b.Projects = append(b.Projects, p)
	}
	sort.Slice(b.Projects, func(i, j int) bool {
		if b.Projects[i].Name != b.Projects[j].Name {
			return b.Projects[i].Name < b.Projects[j].Name
		}
		return b.Projects[i].Path < b.Projects[j].Path
	})
	return b, nil
}

// portable swaps connection ids for names.
func portable(t orgmodel.Template, names map[string]string) orgmodel.Template {
	agents := make([]orgmodel.AgentSpec, len(t.Agents))
	for i, a := range t.Agents {
		a.Provider, a.ProviderID = names[a.ProviderID], ""
		a.FallbackNames = nil
		for _, id := range a.Fallbacks {
			if n := names[id]; n != "" {
				a.FallbackNames = append(a.FallbackNames, n)
			}
		}
		a.Fallbacks = nil
		agents[i] = a
	}
	t.Agents = agents
	return t
}

// Change is one line of an import plan.
type Change struct {
	Kind   string `json:"kind"` // provider | template | project
	Name   string `json:"name"`
	Op     string `json:"op"` // create | update | unchanged | skip
	Detail string `json:"detail,omitempty"`
}

// Result of an import (or a dry run).
type Result struct {
	DryRun  bool     `json:"dry_run"`
	Changes []Change `json:"changes"`
}

// Import applies the bundle. With dryRun nothing is written. Items are matched
// by connection name, template key, and project path (or name for helpers).
func (s *Service) Import(ctx context.Context, b Bundle, dryRun bool) (Result, error) {
	res := Result{DryRun: dryRun, Changes: []Change{}}
	if b.Version != Version {
		return res, fmt.Errorf("không hỗ trợ bundle version %d", b.Version)
	}
	add := func(kind, name, op, detail string) {
		res.Changes = append(res.Changes, Change{Kind: kind, Name: name, Op: op, Detail: detail})
	}

	// connections first: templates and projects reference them by name
	current, err := s.store.Providers().List(ctx)
	if err != nil {
		return res, err
	}
	byName := map[string]storage.Provider{}
	for _, p := range current {
		byName[p.Name] = p
	}
	for _, ps := range b.Providers {
		in := provider.Input{Name: ps.Name, Kind: storage.ProviderKind(ps.Kind), Preset: &ps.Preset, BaseURL: ps.BaseURL, APIKeyEnv: ps.APIKeyEnv,
			TierModels: ps.TierModels, Enabled: &ps.Enabled}
		existing, ok := byName[ps.Name]
		if !ok {
			needsKey := in.Kind == storage.ProviderAnthropic || in.Kind == storage.ProviderOpenAI
			if needsKey && ps.APIKeyEnv == "" {
				add("provider", ps.Name, "skip", "cần nhập API key trên trang Kết nối AI (key không được export)")
				continue
			}
			add("provider", ps.Name, "create", "")
			if !dryRun {
				p, err := s.providers.Create(ctx, in)
				if err != nil {
					res.Changes[len(res.Changes)-1] = Change{Kind: "provider", Name: ps.Name, Op: "skip", Detail: err.Error()}
					continue
				}
				byName[p.Name] = p
				if ps.IsDefault {
					_ = s.store.Providers().SetDefault(ctx, p.ID)
				}
			}
			continue
		}
		same := existing.Kind == in.Kind && existing.BaseURL == ps.BaseURL && existing.APIKeyEnv == ps.APIKeyEnv &&
			existing.Enabled == ps.Enabled && reflect.DeepEqual(nonNil(existing.TierModels), nonNil(ps.TierModels)) &&
			(!ps.IsDefault || existing.IsDefault)
		if same {
			add("provider", ps.Name, "unchanged", "")
			continue
		}
		add("provider", ps.Name, "update", "")
		if !dryRun {
			if _, err := s.providers.Update(ctx, existing.ID, in); err != nil {
				res.Changes[len(res.Changes)-1] = Change{Kind: "provider", Name: ps.Name, Op: "skip", Detail: err.Error()}
				continue
			}
			if ps.IsDefault {
				_ = s.store.Providers().SetDefault(ctx, existing.ID)
			}
		}
	}
	resolve := func(t orgmodel.Template) orgmodel.Template {
		agents := make([]orgmodel.AgentSpec, len(t.Agents))
		for i, a := range t.Agents {
			if p, ok := byName[a.Provider]; ok {
				a.ProviderID = p.ID
			} else {
				a.ProviderID = "" // unknown connection: use the default one
			}
			a.Provider, a.Fallbacks = "", nil
			for _, n := range a.FallbackNames { // unknown ones are left out
				if p, ok := byName[n]; ok {
					a.Fallbacks = append(a.Fallbacks, p.ID)
				}
			}
			a.FallbackNames = nil
			agents[i] = a
		}
		t.Agents = agents
		return t
	}

	for _, te := range b.Templates {
		t := resolve(te.Template)
		if err := orgmodel.Validate(t); err != nil {
			add("template", t.Key, "skip", err.Error())
			continue
		}
		existing, err := s.store.OrgModels().GetTemplateByKey(ctx, t.Key)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			add("template", t.Key, "create", t.Name)
			if !dryRun {
				if _, err := s.org.CreateTemplate(ctx, t); err != nil {
					return res, err
				}
			}
		case err != nil:
			return res, err
		default:
			cur, err := s.org.Load(ctx, existing.ID)
			if err != nil {
				return res, err
			}
			if sameTemplate(cur, t) {
				add("template", t.Key, "unchanged", "")
				continue
			}
			add("template", t.Key, "update", "bản hiện tại được lưu vào lịch sử")
			if !dryRun {
				if _, err := s.org.ReplaceModel(ctx, existing.ID, t, "import"); err != nil {
					return res, err
				}
			}
		}
	}

	for _, ps := range b.Projects {
		label := ps.Name
		if ps.Path != "" {
			label = ps.Name + " (" + ps.Path + ")"
		}
		repo, found, err := s.findProject(ctx, ps)
		if err != nil {
			return res, err
		}
		var model *orgmodel.Template
		if ps.Model != nil {
			t := resolve(*ps.Model)
			if err := orgmodel.Validate(t); err != nil {
				add("project", label, "skip", err.Error())
				continue
			}
			model = &t
		}
		if !found {
			add("project", label, "create", "")
			if dryRun {
				continue
			}
			repo, err = s.store.Repos().Create(ctx, storage.Repo{Name: ps.Name, Path: ps.Path, GitRemote: ps.GitRemote, Description: ps.Description})
			if err != nil {
				return res, err
			}
		} else {
			changed := repo.Description != ps.Description || repo.GitRemote != ps.GitRemote || repo.Name != ps.Name
			if model != nil {
				if m, err := s.store.OrgModels().GetForRepo(ctx, repo.ID); err == nil {
					cur, err := s.org.Load(ctx, m.ID)
					if err != nil {
						return res, err
					}
					changed = changed || !sameTemplate(cur, *model)
				} else {
					changed = true
				}
			}
			if !changed {
				add("project", label, "unchanged", "")
				continue
			}
			add("project", label, "update", "")
			if dryRun {
				continue
			}
			repo.Name, repo.Description, repo.GitRemote = ps.Name, ps.Description, ps.GitRemote
			if err := s.store.Repos().Update(ctx, repo); err != nil {
				return res, err
			}
		}
		if model != nil {
			src := ""
			if ps.SourceTemplate != "" {
				if m, err := s.store.OrgModels().GetTemplateByKey(ctx, ps.SourceTemplate); err == nil {
					src = m.ID
				}
			}
			if _, err := s.org.ApplyTemplate(ctx, repo.ID, *model, src); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

func (s *Service) findProject(ctx context.Context, ps ProjectSpec) (storage.Repo, bool, error) {
	if ps.Path != "" {
		r, err := s.store.Repos().GetByPath(ctx, ps.Path)
		if errors.Is(err, storage.ErrNotFound) {
			return r, false, nil
		}
		return r, err == nil, err
	}
	list, err := s.store.Repos().List(ctx)
	if err != nil {
		return storage.Repo{}, false, err
	}
	for _, r := range list {
		if r.Path == "" && r.Name == ps.Name {
			return r, true, nil
		}
	}
	return storage.Repo{}, false, nil
}

// sameTemplate compares the content that matters (ignores the library key).
func sameTemplate(a, b orgmodel.Template) bool {
	a.Key, b.Key = "", ""
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func nonNil(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if v != "" {
			out[k] = v
		}
	}
	return out
}
