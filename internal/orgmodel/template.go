package orgmodel

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/templates/models"
)

// Template is the portable form of an org model (built-in JSON, export, import).
type Template struct {
	Key         string             `json:"key"`
	Name        string             `json:"name"`
	Kind        string             `json:"kind"`
	Description string             `json:"description"`
	Governance  storage.Governance `json:"governance"`
	Agents      []AgentSpec        `json:"agents"`
}

// AgentSpec is an agent inside a Template.
type AgentSpec struct {
	Key           string              `json:"key"`
	Name          string              `json:"name"`
	Tier          string              `json:"tier"`
	Role          string              `json:"role,omitempty"`
	Description   string              `json:"description,omitempty"`
	ReportsTo     []string            `json:"reports_to,omitempty"`
	ModelTier     string              `json:"model_tier"`
	LLMModel      string              `json:"llm_model,omitempty"`
	Effort        string              `json:"effort,omitempty"`                // thinking level ("" = the CLI's own)
	ProviderID    string              `json:"provider_id,omitempty"`           // this office's connection id
	Provider      string              `json:"provider,omitempty"`              // connection name, used by export/import between offices
	Fallbacks     []string            `json:"fallback_provider_ids,omitempty"` // this office's connection ids, tried next in order
	FallbackNames []string            `json:"fallback_providers,omitempty"`    // their names, used by export/import between offices
	Instructions  string              `json:"instructions,omitempty"`
	Permissions   storage.Permissions `json:"permissions"`
}

// Builtins returns the embedded templates in display order.
func Builtins() ([]Template, error) {
	order := map[string]int{"solo": 0, "team": 1, "council": 2}
	entries, err := fs.ReadDir(models.FS, ".")
	if err != nil {
		return nil, err
	}
	var out []Template
	for _, e := range entries {
		raw, err := fs.ReadFile(models.FS, e.Name())
		if err != nil {
			return nil, err
		}
		var t Template
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("orgmodel: %s: %w", e.Name(), err)
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].Key] < order[out[j].Key] })
	return out, nil
}

func (s AgentSpec) toAgent(orgID string, sort int) storage.Agent {
	// ADR-074 security: FullAccess/ExtraDirs never come from a template, an
	// import, a clone, or a model restore — only internal/api/org.go's
	// applyAgent (admin only) may turn them on.
	perms := s.Permissions
	perms.FullAccess, perms.FullAccessBy, perms.ExtraDirs = false, "", nil
	return storage.Agent{
		OrgModelID: orgID, Key: s.Key, Name: s.Name, Tier: s.Tier, Role: s.Role, Description: s.Description,
		ReportsTo: s.ReportsTo, ModelTier: s.ModelTier, LLMModel: s.LLMModel, Instructions: s.Instructions,
		Permissions: perms, Sort: sort, ProviderID: s.ProviderID, FallbackProviderIDs: s.Fallbacks, Effort: effortOf(s.Effort),
	}
}

// effortOf keeps a known thinking level ("" otherwise: an import from elsewhere).
func effortOf(e string) string {
	if storage.ValidEffort(e) {
		return e
	}
	return ""
}

// AgentSpecOf is an agent as a template spec (as snapshots store it).
func AgentSpecOf(a storage.Agent) AgentSpec { return specFromAgent(a) }

func specFromAgent(a storage.Agent) AgentSpec {
	return AgentSpec{
		Key: a.Key, Name: a.Name, Tier: a.Tier, Role: a.Role, Description: a.Description, ReportsTo: a.ReportsTo,
		ModelTier: a.ModelTier, LLMModel: a.LLMModel, Instructions: a.Instructions, Permissions: a.Permissions,
		ProviderID: a.ProviderID, Fallbacks: a.FallbackProviderIDs, Effort: a.Effort,
	}
}

// Export turns a stored model and its agents into a Template.
func Export(m storage.OrgModel, agents []storage.Agent) Template {
	t := Template{Key: m.Key, Name: m.Name, Kind: m.Kind, Description: m.Description, Governance: m.Governance}
	for _, a := range agents {
		t.Agents = append(t.Agents, specFromAgent(a))
	}
	return t
}
