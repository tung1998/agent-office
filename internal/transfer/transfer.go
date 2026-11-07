// Package transfer exports the office configuration (AI connections without
// secrets, the workflow library, projects with their agents and workflows)
// and imports it back, on this or another machine. Agents refer to
// connections by name and workflows to agents by key so the bundle is
// portable; users, sessions and keys are never exported.
package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/repos"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/team"
	"github.com/tung1998/agent-office/internal/workflow"
)

// Version of the bundle format (1: projects carried an org model, ADR-099).
const Version = 2

// Bundle is the whole exported configuration.
type Bundle struct {
	Version    int            `json:"version"`
	ExportedAt *time.Time     `json:"exported_at,omitempty"`
	Providers  []ProviderSpec `json:"providers"`
	Workflows  []LibraryEntry `json:"workflows"`
	Projects   []ProjectSpec  `json:"projects"`
	// Templates: org model templates of a version 1 bundle (not imported).
	Templates []json.RawMessage `json:"templates,omitempty"`
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

// LibraryEntry is a workflow of the library written or changed here (the
// shipped ones as they are need no copy).
type LibraryEntry struct {
	Key    string `json:"key"`
	Source string `json:"source"`
}

// ProjectWorkflow is a workflow installed in a project; its roles are bound
// by agent key.
type ProjectWorkflow struct {
	Key     string            `json:"key"`
	From    string            `json:"from,omitempty"` // the library workflow it was copied from
	Source  string            `json:"source"`
	Roles   map[string]string `json:"roles,omitempty"` // role → agent key
	Enabled bool              `json:"enabled"`
}

// ProjectSpec is a project, its agents and its workflows.
type ProjectSpec struct {
	Name        string            `json:"name"`
	Path        string            `json:"path,omitempty"` // empty: machine-wide helper
	GitRemote   string            `json:"git_remote,omitempty"`
	Description string            `json:"description,omitempty"`
	Agents      *team.Snapshot    `json:"agents,omitempty"`
	Workflows   []ProjectWorkflow `json:"workflows,omitempty"`
	// Model: a version 1 bundle's org model; its agents are taken.
	Model *team.Snapshot `json:"model,omitempty"`
}

// Service exports and imports.
type Service struct {
	store       storage.Store
	providers   *provider.Service
	team        *team.Service
	lib         workflow.Library
	cloneRoot   string // where a new project's folder goes (ADR-170)
	collections Collections
	burnCheck   func(storage.BurnSession) error // ADR-196; nil: a pack's Burn is left out
}

// New builds a Service.
func New(store storage.Store, providers *provider.Service, tm *team.Service, lib workflow.Library) *Service {
	return &Service{store: store, providers: providers, team: tm, lib: lib}
}

// Export builds the bundle.
func (s *Service) Export(ctx context.Context) (Bundle, error) {
	b := Bundle{Version: Version, Providers: []ProviderSpec{}, Workflows: []LibraryEntry{}, Projects: []ProjectSpec{}}
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

	if s.lib.Dir != "" {
		items, err := s.lib.List()
		if err != nil {
			return b, err
		}
		for _, it := range items {
			if it.Error != "" || it.Builtin {
				continue
			}
			full, err := s.lib.Get(it.Def.Key) // the list leaves the body out
			if err != nil {
				continue
			}
			b.Workflows = append(b.Workflows, LibraryEntry{Key: it.Def.Key, Source: full.Source})
		}
		sort.Slice(b.Workflows, func(i, j int) bool { return b.Workflows[i].Key < b.Workflows[j].Key })
	}

	repos, err := s.store.Repos().List(ctx)
	if err != nil {
		return b, err
	}
	for _, r := range repos {
		p, err := s.projectSpec(ctx, r, names)
		if err != nil {
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

// projectSpec is r, its agents (connections by name) and its workflows.
func (s *Service) projectSpec(ctx context.Context, r storage.Repo, names map[string]string) (ProjectSpec, error) {
	p := ProjectSpec{Name: r.Name, Path: r.Path, GitRemote: repos.Remote(r.Path, r.GitRemote), Description: r.Description}
	agents, err := s.store.Agents().List(ctx, r.ID)
	if err != nil {
		return p, err
	}
	if len(agents) > 0 {
		snap := portable(team.Export(r, agents), names)
		p.Agents = &snap
	}
	keyOf := map[string]string{}
	for _, a := range agents {
		keyOf[a.ID] = a.Key
	}
	wfs, err := s.store.Workflows().List(ctx, r.ID)
	if err != nil {
		return p, err
	}
	for _, w := range wfs {
		pw := ProjectWorkflow{Key: w.Key, From: w.SourceKey, Source: w.Source, Enabled: w.Enabled}
		for role, id := range w.Bindings {
			if k := keyOf[id]; k != "" {
				if pw.Roles == nil {
					pw.Roles = map[string]string{}
				}
				pw.Roles[role] = k
			}
		}
		p.Workflows = append(p.Workflows, pw)
	}
	return p, nil
}

// connectionNames maps this office's connection ids to their names.
func (s *Service) connectionNames(ctx context.Context) (map[string]string, error) {
	provs, err := s.store.Providers().List(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, p := range provs {
		names[p.ID] = p.Name
	}
	return names, nil
}

// portable swaps connection ids for names.
func portable(t team.Snapshot, names map[string]string) team.Snapshot {
	agents := make([]team.AgentSpec, len(t.Agents))
	for i, a := range t.Agents {
		a.Provider, a.ProviderID = names[a.ProviderID], ""
		a.FallbackNames = nil
		for _, entry := range a.Fallbacks { // "name|model" keeps the model
			if id, model := storage.SplitFallback(entry); names[id] != "" {
				a.FallbackNames = append(a.FallbackNames, storage.FallbackEntry(names[id], model))
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
	Kind   string `json:"kind"` // provider | workflow | project
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
// by connection name, workflow key, and project path (or name for helpers).
func (s *Service) Import(ctx context.Context, b Bundle, dryRun bool) (Result, error) {
	res := Result{DryRun: dryRun, Changes: []Change{}}
	if b.Version != Version && b.Version != 1 {
		return res, fmt.Errorf("không hỗ trợ bundle version %d", b.Version)
	}
	add := func(kind, name, op, detail string) {
		res.Changes = append(res.Changes, Change{Kind: kind, Name: name, Op: op, Detail: detail})
	}

	// connections first: agents reference them by name
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
		if ok && ps.APIKeyEnv == "" && existing.APIKeyEnv != "" {
			// the bundle never carries a working env-var key it doesn't know about
			// (e.g. built on another machine, or from a provider using a stored
			// secret there): don't let it blank out the existing one.
			in.APIKeyEnv = existing.APIKeyEnv
		}
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
			} else {
				// agents referencing this would-be-created provider must see it as a
				// change, not fall back to the default-provider placeholder ("").
				byName[ps.Name] = storage.Provider{Name: ps.Name, ID: "dryrun:" + ps.Name}
			}
			continue
		}
		same := existing.Kind == in.Kind && existing.BaseURL == ps.BaseURL && existing.APIKeyEnv == in.APIKeyEnv &&
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
	resolve := func(t team.Snapshot) team.Snapshot {
		agents := make([]team.AgentSpec, len(t.Agents))
		for i, a := range t.Agents {
			if p, ok := byName[a.Provider]; ok {
				a.ProviderID = p.ID
			} else {
				a.ProviderID = "" // unknown connection: use the default one
			}
			a.Provider, a.Fallbacks = "", nil
			for _, n := range a.FallbackNames { // unknown ones are left out
				if name, model := storage.SplitFallback(n); byName[name].ID != "" {
					a.Fallbacks = append(a.Fallbacks, storage.FallbackEntry(byName[name].ID, model))
				}
			}
			a.FallbackNames = nil
			agents[i] = a
		}
		t.Agents = agents
		return t
	}

	if len(b.Templates) > 0 {
		add("template", fmt.Sprintf("%d mô hình", len(b.Templates)), "skip", "không còn mô hình tổ chức: agent của từng project vẫn được nhập")
	}
	for _, le := range b.Workflows {
		def, err := workflow.Parse(le.Source)
		if err != nil || def.Key != le.Key {
			add("workflow", le.Key, "skip", "quy trình không hợp lệ")
			continue
		}
		if workflow.BuiltinSource(le.Key) != "" {
			add("workflow", le.Key, "skip", "mẫu có sẵn chỉ đọc; quy trình trong project được nhập riêng")
			continue
		}
		cur, err := s.lib.Get(le.Key)
		switch {
		case err == nil && strings.TrimSpace(cur.Source) == strings.TrimSpace(le.Source):
			add("workflow", le.Key, "unchanged", "")
			continue
		case err == nil:
			add("workflow", le.Key, "update", def.Name)
		default:
			add("workflow", le.Key, "create", def.Name)
		}
		if !dryRun {
			if _, err := s.lib.Save(le.Key, le.Source); err != nil {
				return res, err
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
		var agents *team.Snapshot
		if src := cmp(ps.Agents, ps.Model); src != nil {
			t := resolve(*src)
			if err := team.Validate(t.Agents); err != nil {
				add("project", label, "skip", err.Error())
				continue
			}
			agents = &t
		}
		if bad := badWorkflow(ps.Workflows); bad != "" {
			add("project", label, "skip", bad)
			continue
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
			if agents != nil {
				cur, err := s.team.Load(ctx, repo.ID)
				if err != nil {
					return res, err
				}
				changed = changed || !sameAgents(cur, *agents)
			}
			if wfChanged, err := s.workflowsDiffer(ctx, repo.ID, ps.Workflows); err != nil {
				return res, err
			} else if wfChanged {
				changed = true
			}
			if !changed {
				add("project", label, "unchanged", "")
				continue
			}
			add("project", label, "update", "")
			if dryRun {
				continue
			}
			if err := s.store.Repos().UpdateInfoRemote(ctx, repo.ID, ps.Name, ps.Description, ps.GitRemote, repo.UpdatedAt); err != nil {
				return res, err
			}
		}
		if agents != nil {
			if err := s.team.Replace(ctx, repo.ID, *agents, "import"); err != nil {
				return res, err
			}
		}
		if err := s.putWorkflows(ctx, repo.ID, ps.Workflows); err != nil {
			return res, err
		}
	}
	return res, nil
}

// workflowsDiffer: the project's workflows are not those of the bundle.
func (s *Service) workflowsDiffer(ctx context.Context, projectID string, in []ProjectWorkflow) (bool, error) {
	for _, w := range in {
		cur, err := s.store.Workflows().GetByKey(ctx, projectID, w.Key)
		if errors.Is(err, storage.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		roles, err := s.roles(ctx, projectID, w.Roles)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(cur.Source) != strings.TrimSpace(w.Source) || cur.Enabled != w.Enabled || !maps.Equal(nonNil(cur.Bindings), roles) {
			return true, nil
		}
	}
	return false, nil
}

// putWorkflows installs or updates the project's workflows of the bundle.
// It resolves every workflow (parse, roles, current record) before writing
// any of them, so a resolve failure partway through the bundle leaves the
// project's existing workflows untouched instead of half-applying the import.
// Entries sharing a Key (same bundle listing a key twice) are collapsed into
// one plan slot, last one wins — matching the pre-fix behavior where the
// second pass's GetByKey would already see the first pass's write.
func (s *Service) putWorkflows(ctx context.Context, projectID string, in []ProjectWorkflow) error {
	type pending struct {
		next     storage.Workflow
		existing bool
	}
	plan := make([]pending, 0, len(in))
	byKey := map[string]int{}
	for _, w := range in {
		def, err := workflow.Parse(w.Source)
		if err != nil {
			continue
		}
		roles, err := s.roles(ctx, projectID, w.Roles)
		if err != nil {
			return err
		}
		next := storage.Workflow{ProjectID: projectID, Key: def.Key, Name: def.Name, Description: def.Description,
			Source: strings.TrimSpace(w.Source) + "\n", Bindings: roles, Enabled: w.Enabled}
		if w.From != "" {
			next.SourceKey, next.SourceHash = w.From, workflow.Hash(w.Source)
		}
		if idx, ok := byKey[def.Key]; ok {
			next.ID, next.CreatedAt = plan[idx].next.ID, plan[idx].next.CreatedAt
			plan[idx] = pending{next: next, existing: plan[idx].existing}
			continue
		}
		cur, err := s.store.Workflows().GetByKey(ctx, projectID, def.Key)
		if errors.Is(err, storage.ErrNotFound) {
			byKey[def.Key] = len(plan)
			plan = append(plan, pending{next: next})
			continue
		}
		if err != nil {
			return err
		}
		next.ID, next.CreatedAt = cur.ID, cur.CreatedAt
		byKey[def.Key] = len(plan)
		plan = append(plan, pending{next: next, existing: true})
	}
	for _, p := range plan {
		if p.existing {
			if err := s.store.Workflows().Update(ctx, p.next); err != nil {
				return err
			}
			continue
		}
		if _, err := s.store.Workflows().Create(ctx, p.next); err != nil {
			return err
		}
	}
	return nil
}

// roles turns role → agent key into role → agent id (unknown keys dropped).
func (s *Service) roles(ctx context.Context, projectID string, byKey map[string]string) (map[string]string, error) {
	out := map[string]string{}
	if len(byKey) == 0 {
		return out, nil
	}
	agents, err := s.store.Agents().List(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for role, key := range byKey {
		for _, a := range agents {
			if a.Key == key {
				out[role] = a.ID
			}
		}
	}
	return out, nil
}

// badWorkflow names the first workflow that does not parse, or whose source key
// does not match its bundle key ("" = none).
func badWorkflow(list []ProjectWorkflow) string {
	for _, w := range list {
		def, err := workflow.Parse(w.Source)
		if err != nil {
			return "quy trình " + w.Key + ": " + err.Error()
		}
		if def.Key != w.Key {
			return "quy trình " + w.Key + ": key trong source là " + def.Key
		}
	}
	return ""
}

func cmp(a, b *team.Snapshot) *team.Snapshot {
	if a != nil {
		return a
	}
	return b
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

// sameAgents compares what matters of two sets of agents.
func sameAgents(a, b team.Snapshot) bool {
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
