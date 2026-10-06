// Package orgmodel manages org model templates and their per-project instances.
//
// Hierarchy: project → org model (instance copied from a template) → agents.
// Every change is validated as a whole model so solo, team and council all
// stay valid configurations of the same core, and every change is preceded
// by a snapshot (revision) so it can be rolled back.
package orgmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/storage"
)

var (
	ErrNotTemplate  = errors.New("orgmodel: not a template")
	ErrHasInstance  = errors.New("orgmodel: repo already has a model")
	ErrBuiltinReset = errors.New("orgmodel: only built-in templates can be reset")
)

// KeepRevisions is how many snapshots are kept per model.
const KeepRevisions = 50

// WithActor records who is making changes, for the revision history.
func WithActor(ctx context.Context, who string) context.Context { return actor.With(ctx, who) }

func actorFrom(ctx context.Context) string { return actor.From(ctx) }

// Service is the use-case layer for org models.
type Service struct{ store storage.Store }

// NewService builds a Service.
func NewService(store storage.Store) *Service { return &Service{store: store} }

// SeedBuiltins inserts missing built-in templates. Existing ones are left
// untouched so admin edits survive restarts.
func (s *Service) SeedBuiltins(ctx context.Context) (int, error) {
	builtins, err := Builtins()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range builtins {
		if _, err := s.store.OrgModels().GetTemplateByKey(ctx, t.Key); err == nil {
			continue
		} else if !errors.Is(err, storage.ErrNotFound) {
			return n, err
		}
		if _, err := s.create(ctx, t, "", "", true); err != nil {
			return n, fmt.Errorf("seed %s: %w", t.Key, err)
		}
		n++
	}
	return n, nil
}

// ResetBuiltin restores a built-in template to its shipped definition (in place,
// so its history is kept).
func (s *Service) ResetBuiltin(ctx context.Context, key string) (storage.OrgModel, error) {
	builtins, err := Builtins()
	if err != nil {
		return storage.OrgModel{}, err
	}
	for _, t := range builtins {
		if t.Key != key {
			continue
		}
		existing, err := s.store.OrgModels().GetTemplateByKey(ctx, key)
		if errors.Is(err, storage.ErrNotFound) {
			return s.create(ctx, t, "", "", true)
		}
		if err != nil {
			return storage.OrgModel{}, err
		}
		return s.replace(ctx, existing, t, "", "template.reset")
	}
	return storage.OrgModel{}, ErrBuiltinReset
}

// CreateTemplate adds a custom template to the library.
func (s *Service) CreateTemplate(ctx context.Context, t Template) (storage.OrgModel, error) {
	return s.create(ctx, t, "", "", false)
}

// CloneTemplate copies a template (or a project's model) into a new library template.
func (s *Service) CloneTemplate(ctx context.Context, sourceID, key, name string) (storage.OrgModel, error) {
	t, err := s.Load(ctx, sourceID)
	if err != nil {
		return storage.OrgModel{}, err
	}
	t.Key, t.Name = key, name
	return s.create(ctx, t, "", "", false)
}

// ApplyToRepo copies a library template into the project. replace overwrites
// an existing model (in place: same id, history kept).
func (s *Service) ApplyToRepo(ctx context.Context, repoID, templateID string, replace bool) (storage.OrgModel, error) {
	src, err := s.store.OrgModels().Get(ctx, templateID)
	if err != nil {
		return storage.OrgModel{}, err
	}
	if !src.IsTemplate() {
		return storage.OrgModel{}, ErrNotTemplate
	}
	t, err := s.Load(ctx, templateID)
	if err != nil {
		return storage.OrgModel{}, err
	}
	if !replace {
		if _, err := s.store.OrgModels().GetForRepo(ctx, repoID); err == nil {
			return storage.OrgModel{}, ErrHasInstance
		}
	}
	return s.ApplyTemplate(ctx, repoID, t, templateID)
}

// ApplyTemplate installs an arbitrary (possibly tailored) template as the
// project's model, replacing any existing one in place.
func (s *Service) ApplyTemplate(ctx context.Context, repoID string, t Template, sourceID string) (storage.OrgModel, error) {
	if _, err := s.store.Repos().Get(ctx, repoID); err != nil {
		return storage.OrgModel{}, err
	}
	existing, err := s.store.OrgModels().GetForRepo(ctx, repoID)
	if errors.Is(err, storage.ErrNotFound) {
		return s.create(ctx, t, repoID, sourceID, false)
	}
	if err != nil {
		return storage.OrgModel{}, err
	}
	return s.replace(ctx, existing, t, sourceID, "model.replace")
}

// Load returns a stored model as a Template.
func (s *Service) Load(ctx context.Context, id string) (Template, error) {
	m, err := s.store.OrgModels().Get(ctx, id)
	if err != nil {
		return Template{}, err
	}
	agents, err := s.store.Agents().List(ctx, id)
	if err != nil {
		return Template{}, err
	}
	return Export(m, agents), nil
}

// UpdateModel changes name, description, kind or governance.
func (s *Service) UpdateModel(ctx context.Context, id string, fn func(*storage.OrgModel)) (storage.OrgModel, error) {
	m, err := s.store.OrgModels().Get(ctx, id)
	if err != nil {
		return m, err
	}
	fn(&m)
	agents, err := s.store.Agents().List(ctx, id)
	if err != nil {
		return m, err
	}
	if err := Validate(Export(m, agents)); err != nil {
		return m, err
	}
	err = s.store.InTx(ctx, func(tx storage.Store) error {
		if err := snapshot(ctx, tx, id, "model.update"); err != nil {
			return err
		}
		return tx.OrgModels().Update(ctx, m)
	})
	return m, err
}

// SaveAgent creates (a.ID empty) or updates an agent after validating the whole model.
func (s *Service) SaveAgent(ctx context.Context, a storage.Agent) (storage.Agent, error) {
	m, err := s.store.OrgModels().Get(ctx, a.OrgModelID)
	if err != nil {
		return a, err
	}
	agents, err := s.store.Agents().List(ctx, a.OrgModelID)
	if err != nil {
		return a, err
	}
	next := make([]storage.Agent, 0, len(agents)+1)
	oldKey := ""
	for _, x := range agents {
		if x.ID == a.ID {
			oldKey = x.Key
			continue
		}
		next = append(next, x)
	}
	if a.ID != "" && oldKey == "" {
		return a, storage.ErrNotFound
	}
	// Renaming a key keeps the reporting lines pointing at it.
	if oldKey != "" && oldKey != a.Key {
		for i := range next {
			for j, r := range next[i].ReportsTo {
				if r == oldKey {
					next[i].ReportsTo[j] = a.Key
				}
			}
		}
		for j, v := range m.Governance.Veto {
			if v == oldKey {
				m.Governance.Veto[j] = a.Key
			}
		}
	}
	if a.Sort == 0 && a.ID == "" {
		a.Sort = len(agents)
	}
	next = append(next, a)
	if err := Validate(Export(m, next)); err != nil {
		return a, err
	}
	action := "agent.update:" + a.Key
	if a.ID == "" {
		action = "agent.create:" + a.Key
	}
	err = s.store.InTx(ctx, func(tx storage.Store) error {
		if err := snapshot(ctx, tx, m.ID, action); err != nil {
			return err
		}
		if a.ID == "" {
			created, err := tx.Agents().Create(ctx, a)
			a = created
			return err
		}
		if err := tx.Agents().Update(ctx, a); err != nil {
			return err
		}
		if oldKey != a.Key {
			for _, x := range next[:len(next)-1] {
				if err := tx.Agents().Update(ctx, x); err != nil {
					return err
				}
			}
			return tx.OrgModels().Update(ctx, m)
		}
		return nil
	})
	return a, err
}

// DeleteAgent removes an agent if the model stays valid without it.
func (s *Service) DeleteAgent(ctx context.Context, id string) error {
	a, err := s.store.Agents().Get(ctx, id)
	if err != nil {
		return err
	}
	m, err := s.store.OrgModels().Get(ctx, a.OrgModelID)
	if err != nil {
		return err
	}
	agents, err := s.store.Agents().List(ctx, a.OrgModelID)
	if err != nil {
		return err
	}
	rest := agents[:0]
	for _, x := range agents {
		if x.ID != id {
			rest = append(rest, x)
		}
	}
	if err := Validate(Export(m, rest)); err != nil {
		return err
	}
	return s.store.InTx(ctx, func(tx storage.Store) error {
		if err := snapshot(ctx, tx, m.ID, "agent.delete:"+a.Key); err != nil {
			return err
		}
		return tx.Agents().Delete(ctx, id)
	})
}

// ReplaceModel overwrites a model's content (template or project model) in place.
func (s *Service) ReplaceModel(ctx context.Context, id string, t Template, action string) (storage.OrgModel, error) {
	m, err := s.store.OrgModels().Get(ctx, id)
	if err != nil {
		return m, err
	}
	return s.replace(ctx, m, t, m.SourceTemplateID, action)
}

// Revisions lists snapshots of a model, newest first.
func (s *Service) Revisions(ctx context.Context, modelID string, limit int) ([]storage.Revision, error) {
	return s.store.Revisions().List(ctx, modelID, limit)
}

// RevisionTemplate decodes a snapshot.
func RevisionTemplate(r storage.Revision) (Template, error) {
	var t Template
	err := json.Unmarshal(r.Snapshot, &t)
	return t, err
}

// Restore puts a model back to a snapshot. The current state is snapshotted
// first, so a restore can itself be undone.
func (s *Service) Restore(ctx context.Context, revisionID string) (storage.OrgModel, error) {
	rev, err := s.store.Revisions().Get(ctx, revisionID)
	if err != nil {
		return storage.OrgModel{}, err
	}
	t, err := RevisionTemplate(rev)
	if err != nil {
		return storage.OrgModel{}, err
	}
	m, err := s.store.OrgModels().Get(ctx, rev.OrgModelID)
	if err != nil {
		return storage.OrgModel{}, err
	}
	return s.replace(ctx, m, t, m.SourceTemplateID, "restore:"+rev.ID)
}

// replace swaps the content of m for t, keeping m's id (and project link and
// history). The library key of a template is kept to avoid key clashes.
func (s *Service) replace(ctx context.Context, m storage.OrgModel, t Template, sourceID, action string) (storage.OrgModel, error) {
	if m.IsTemplate() {
		t.Key = m.Key
	}
	if err := Validate(t); err != nil {
		return storage.OrgModel{}, err
	}
	err := s.store.InTx(ctx, func(tx storage.Store) error {
		if err := snapshot(ctx, tx, m.ID, action); err != nil {
			return err
		}
		agents, err := tx.Agents().List(ctx, m.ID)
		if err != nil {
			return err
		}
		for _, a := range agents {
			if err := tx.Agents().Delete(ctx, a.ID); err != nil {
				return err
			}
		}
		m.Key, m.Name, m.Description, m.Kind, m.Governance = t.Key, t.Name, t.Description, t.Kind, t.Governance
		m.SourceTemplateID = sourceID
		if err := tx.OrgModels().Update(ctx, m); err != nil {
			return err
		}
		if err := createAgents(ctx, tx, m.ID, t.Agents); err != nil {
			return err
		}
		return nil
	})
	return m, err
}

// snapshot stores the current state of a model before a change.
func snapshot(ctx context.Context, tx storage.Store, modelID, action string) error {
	m, err := tx.OrgModels().Get(ctx, modelID)
	if err != nil {
		return err
	}
	agents, err := tx.Agents().List(ctx, modelID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(Export(m, agents))
	if err != nil {
		return err
	}
	if _, err := tx.Revisions().Create(ctx, storage.Revision{
		OrgModelID: modelID, Action: action, Actor: actorFrom(ctx), AgentCount: len(agents), Snapshot: raw,
	}); err != nil {
		return err
	}
	return tx.Revisions().Prune(ctx, modelID, KeepRevisions)
}

// createAgents inserts a template's agents. A connection that no longer exists
// (deleted, or a snapshot from before) falls back to the default one.
func createAgents(ctx context.Context, tx storage.Store, modelID string, specs []AgentSpec) error {
	for i, spec := range specs {
		if spec.ProviderID != "" {
			if _, err := tx.Providers().Get(ctx, spec.ProviderID); errors.Is(err, storage.ErrNotFound) {
				spec.ProviderID = ""
			} else if err != nil {
				return err
			}
		}
		if _, err := tx.Agents().Create(ctx, spec.toAgent(modelID, i)); err != nil {
			return fmt.Errorf("agent %s: %w", spec.Key, err)
		}
	}
	return nil
}

func (s *Service) create(ctx context.Context, t Template, repoID, sourceID string, builtin bool) (storage.OrgModel, error) {
	if err := Validate(t); err != nil {
		return storage.OrgModel{}, err
	}
	var out storage.OrgModel
	err := s.store.InTx(ctx, func(tx storage.Store) error {
		m, err := tx.OrgModels().Create(ctx, storage.OrgModel{
			RepoID: repoID, SourceTemplateID: sourceID, Key: t.Key, Name: t.Name, Description: t.Description,
			Kind: t.Kind, Governance: t.Governance, Builtin: builtin,
		})
		if err != nil {
			return err
		}
		if err := createAgents(ctx, tx, m.ID, t.Agents); err != nil {
			return err
		}
		out = m
		return nil
	})
	return out, err
}
