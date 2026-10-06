// Package setup is the AI setup assistant for a project: it sends the scan
// summary and the template library to a model, gets back a description, the
// best-fitting org model and tailored agent changes, and turns the accepted
// changes into a validated org model.
package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

// ErrNoProvider means no AI connection is available: the UI sends the user to set one up.
var ErrNoProvider = errors.New("chưa có kết nối AI hoạt động")

// AgentChange is one tailored edit on top of the chosen template.
type AgentChange struct {
	Action       string   `json:"action"` // update | add | remove
	Key          string   `json:"key"`
	Name         string   `json:"name,omitempty"`
	Tier         string   `json:"tier,omitempty"`
	Role         string   `json:"role,omitempty"`
	Description  string   `json:"description,omitempty"`
	ReportsTo    []string `json:"reports_to,omitempty"`
	ModelTier    string   `json:"model_tier,omitempty"`
	Instructions string   `json:"instructions,omitempty"` // add: full prompt; update: project context appended
	ReadOnly     *bool    `json:"read_only,omitempty"`
	Source       string   `json:"source,omitempty"` // existing agent file it came from
	Reason       string   `json:"reason"`
}

// Proposal is the model's recommendation.
type Proposal struct {
	Description string        `json:"description"`
	TemplateKey string        `json:"template_key"`
	Reason      string        `json:"reason"`
	Confidence  float64       `json:"confidence"`
	Changes     []AgentChange `json:"agent_changes"`
	Notes       []string      `json:"notes"`
}

// Result is a proposal plus the org model it produces and how it was made.
type Result struct {
	Proposal Proposal          `json:"proposal"`
	Template orgmodel.Template `json:"template"`
	Problems []string          `json:"problems"`
	Provider string            `json:"provider"`
	Model    string            `json:"model"`
	Usage    llm.Result        `json:"usage"`
}

// Assistant runs the setup flow.
type Assistant struct {
	store     storage.Store
	providers *provider.Service
	org       *orgmodel.Service
}

// New builds an Assistant.
func New(store storage.Store, providers *provider.Service, org *orgmodel.Service) *Assistant {
	return &Assistant{store: store, providers: providers, org: org}
}

// Propose asks the default AI connection (strong tier) for a setup.
// projectText is the scan summary (empty for a machine-wide helper); goal is
// optional free text from the user.
func (a *Assistant) Propose(ctx context.Context, projectID, projectName, projectText, goal string) (Result, error) {
	p, model, err := a.providers.ResolveModel(ctx, storage.Agent{ModelTier: storage.TierStrong})
	if errors.Is(err, storage.ErrNotFound) || (err == nil && (!p.Enabled || p.Status == "error")) {
		return Result{}, ErrNoProvider
	}
	if err != nil {
		return Result{}, err
	}
	if _, err := a.providers.Client(p); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrNoProvider, err)
	}
	templates, err := a.library(ctx)
	if err != nil {
		return Result{}, err
	}
	out, err := a.providers.Call(ctx, p, llm.Request{
		Model: model, System: systemPrompt, MaxTokens: 6000,
		Prompt: userPrompt(projectName, projectText, goal, templates),
	}, usage.Meta{Kind: "setup_propose", ProjectID: projectID})
	if err != nil {
		return Result{}, fmt.Errorf("gọi AI lỗi: %w", err)
	}
	var prop Proposal
	if err := json.Unmarshal([]byte(extractJSON(out.Text)), &prop); err != nil {
		return Result{}, fmt.Errorf("AI trả về không đúng định dạng JSON: %w", err)
	}
	res := Result{Proposal: prop, Provider: p.Name, Model: out.Model, Usage: out}
	if res.Model == "" {
		res.Model = model
	}
	res.Usage.Text = ""
	t, problems, err := a.Build(ctx, prop.TemplateKey, prop.Changes)
	if err != nil {
		return res, err
	}
	if problems == nil {
		problems = []string{}
	}
	if res.Proposal.Changes == nil {
		res.Proposal.Changes = []AgentChange{}
	}
	if res.Proposal.Notes == nil {
		res.Proposal.Notes = []string{}
	}
	res.Template, res.Problems = t, problems
	return res, nil
}

// Build applies changes to a library template and validates the result.
// Structural problems are returned (not as an error) so the user can untick
// the offending change.
func (a *Assistant) Build(ctx context.Context, templateKey string, changes []AgentChange) (orgmodel.Template, []string, error) {
	m, err := a.store.OrgModels().GetTemplateByKey(ctx, templateKey)
	if errors.Is(err, storage.ErrNotFound) {
		return orgmodel.Template{}, nil, fmt.Errorf("mô hình %q không có trong thư viện", templateKey)
	}
	if err != nil {
		return orgmodel.Template{}, nil, err
	}
	t, err := a.org.Load(ctx, m.ID)
	if err != nil {
		return t, nil, err
	}
	t = Apply(t, changes)
	var problems []string
	if err := orgmodel.Validate(t); err != nil {
		var ve *orgmodel.ValidationError
		if !errors.As(err, &ve) {
			return t, nil, err
		}
		problems = ve.Problems
	}
	return t, problems, nil
}

// Apply edits a template copy. Unknown keys in update/remove are ignored.
func Apply(t orgmodel.Template, changes []AgentChange) orgmodel.Template {
	agents := append([]orgmodel.AgentSpec(nil), t.Agents...)
	idx := func(key string) int {
		for i, a := range agents {
			if a.Key == key {
				return i
			}
		}
		return -1
	}
	for _, c := range changes {
		key := strings.TrimSpace(c.Key)
		switch c.Action {
		case "update":
			i := idx(key)
			if i < 0 {
				continue
			}
			a := &agents[i]
			setIf(&a.Name, c.Name)
			setIf(&a.Role, c.Role)
			setIf(&a.Description, c.Description)
			if storage.ValidTier(c.ModelTier) {
				a.ModelTier = c.ModelTier
			}
			if len(c.ReportsTo) > 0 && a.Tier != storage.TierLead {
				a.ReportsTo = c.ReportsTo
			}
			if strings.TrimSpace(c.Instructions) != "" {
				a.Instructions = strings.TrimSpace(a.Instructions + "\n\nBối cảnh project:\n" + strings.TrimSpace(c.Instructions))
			}
			if c.ReadOnly != nil {
				a.Permissions.ReadOnly = *c.ReadOnly
			}
		case "add":
			if key == "" || idx(key) >= 0 {
				continue
			}
			spec := orgmodel.AgentSpec{
				Key: key, Name: orDefault(c.Name, key), Tier: orDefault(c.Tier, storage.TierWorker), Role: c.Role,
				Description: c.Description, ReportsTo: c.ReportsTo, ModelTier: c.ModelTier, Instructions: strings.TrimSpace(c.Instructions),
				Permissions: storage.Permissions{ReadOnly: true},
			}
			if !storage.ValidTier(spec.ModelTier) {
				spec.ModelTier = storage.TierFast
			}
			if c.ReadOnly != nil {
				spec.Permissions.ReadOnly = *c.ReadOnly
			}
			if !spec.Permissions.ReadOnly {
				spec.Permissions.RequiresApproval = true // writes always need a human
			}
			if spec.Tier == storage.TierLead {
				spec.ReportsTo = nil
			}
			agents = append(agents, spec)
		case "remove":
			i := idx(key)
			if i < 0 {
				continue
			}
			agents = append(agents[:i], agents[i+1:]...)
			for j := range agents {
				agents[j].ReportsTo = without(agents[j].ReportsTo, key)
			}
			t.Governance.Veto = without(t.Governance.Veto, key)
		}
	}
	t.Agents = agents
	return t
}

// Accept applies the accepted changes and installs the result as the project's model.
func (a *Assistant) Accept(ctx context.Context, repoID, templateKey string, changes []AgentChange) (storage.OrgModel, error) {
	t, problems, err := a.Build(ctx, templateKey, changes)
	if err != nil {
		return storage.OrgModel{}, err
	}
	if len(problems) > 0 {
		return storage.OrgModel{}, &orgmodel.ValidationError{Problems: problems}
	}
	src, err := a.store.OrgModels().GetTemplateByKey(ctx, templateKey)
	if err != nil {
		return storage.OrgModel{}, err
	}
	return a.org.ApplyTemplate(ctx, repoID, t, src.ID)
}

type libraryEntry struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Description string          `json:"description"`
	Governance  json.RawMessage `json:"governance"`
	Agents      []libraryAgent  `json:"agents"`
}

type libraryAgent struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Tier      string   `json:"tier"`
	Role      string   `json:"role"`
	ReportsTo []string `json:"reports_to,omitempty"`
}

func (a *Assistant) library(ctx context.Context) ([]libraryEntry, error) {
	list, err := a.store.OrgModels().ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	var out []libraryEntry
	for _, m := range list {
		agents, err := a.store.Agents().List(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		gov, _ := json.Marshal(m.Governance)
		e := libraryEntry{Key: m.Key, Name: m.Name, Kind: m.Kind, Description: m.Description, Governance: gov}
		for _, ag := range agents {
			e.Agents = append(e.Agents, libraryAgent{Key: ag.Key, Name: ag.Name, Tier: ag.Tier, Role: ag.Role, ReportsTo: ag.ReportsTo})
		}
		out = append(out, e)
	}
	return out, nil
}

// extractJSON takes the ```json block, or the outermost {...}.
func extractJSON(s string) string {
	if i := strings.Index(s, "```json"); i >= 0 {
		rest := s[i+7:]
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}

func setIf(dst *string, v string) {
	if strings.TrimSpace(v) != "" {
		*dst = strings.TrimSpace(v)
	}
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return strings.TrimSpace(v)
}

func without(list []string, v string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
