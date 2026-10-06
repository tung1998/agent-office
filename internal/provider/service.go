// Package provider manages AI connections: stores keys encrypted, tests them,
// and builds llm clients for the rest of the system.
package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/usage"
)

var (
	ErrInvalidKind = errors.New("loại kết nối không hợp lệ")
	ErrNameTaken   = errors.New("tên kết nối đã tồn tại")
	ErrNeedsKey    = errors.New("kết nối API cần API key hoặc tên biến môi trường chứa key")
	ErrBadTier     = errors.New("hạng model chỉ gồm strong, balanced, fast")
	// ErrKeyRequiredForNewURL: a stored key never follows a changed endpoint.
	ErrKeyRequiredForNewURL = errors.New("đổi địa chỉ API thì phải nhập lại API key (key đã lưu không được gửi tới địa chỉ mới)")
)

// Service is the provider use-case layer.
type Service struct {
	store      storage.Store
	box        *secrets.Box
	opts       llm.Options
	now        func() time.Time
	usage      *usage.Service // nil: calls are not recorded or budgeted
	resolveBin func(string) string
}

// SetBinResolver sets how CLI providers without an explicit path find their
// binary (the CLI manager's lookup). Without it, PATH is used.
func (s *Service) SetBinResolver(fn func(bin string) string) { s.resolveBin = fn }

// SetUsage turns on recording and budget checks for Call.
func (s *Service) SetUsage(u *usage.Service) { s.usage = u }

// Call sends one prompt through p: it checks the daily budget, calls the
// model, and records tokens, cost and duration. Every model call in the
// office goes through here.
func (s *Service) Call(ctx context.Context, p storage.Provider, req llm.Request, m usage.Meta) (llm.Result, error) {
	if s.usage != nil {
		if err := s.usage.Check(ctx, m.ProjectID); err != nil {
			_, _ = s.usage.Record(ctx, m, p, req.Model, llm.Result{}, err)
			return llm.Result{}, err
		}
	}
	client, err := s.Client(p)
	if err != nil {
		return llm.Result{}, err
	}
	res, err := client.Complete(ctx, req)
	if s.usage != nil {
		if _, recErr := s.usage.Record(ctx, m, p, req.Model, res, err); recErr != nil && err == nil {
			err = recErr
		}
	}
	return res, err
}

// NewService builds a Service. opts lets tests inject an HTTP client.
func NewService(store storage.Store, box *secrets.Box, opts llm.Options) *Service {
	return &Service{store: store, box: box, opts: opts, now: time.Now}
}

// Input creates or updates a provider. APIKey nil keeps the stored key, "" clears it.
type Input struct {
	Name       string
	Kind       storage.ProviderKind
	Preset     *string // catalog entry; nil keeps the current one
	BaseURL    string
	APIKey     *string
	APIKeyEnv  string
	TierModels map[string]string
	Enabled    *bool
}

func (s *Service) apply(p *storage.Provider, in Input) error {
	// A stored key must not follow a new endpoint: whoever can edit the URL could
	// otherwise point it at their own host and press "Kiểm tra" to receive the key.
	if p.ID != "" && (p.APIKeyEnc != "" || p.APIKeyEnv != "") && in.APIKey == nil && normURL(in.BaseURL) != normURL(p.BaseURL) {
		return ErrKeyRequiredForNewURL
	}
	p.Name = strings.TrimSpace(in.Name)
	if p.Name == "" {
		return errors.New("thiếu tên kết nối")
	}
	if !in.Kind.Valid() {
		return ErrInvalidKind
	}
	p.Kind = in.Kind
	if in.Preset != nil {
		p.Preset = *in.Preset
		if _, ok := PresetByID(p.Preset); !ok || p.Kind != storage.ProviderOpenAICompatible {
			p.Preset = ""
		}
	}
	p.BaseURL = strings.TrimSpace(in.BaseURL)
	p.APIKeyEnv = strings.TrimSpace(in.APIKeyEnv)
	if in.APIKey != nil {
		key := strings.TrimSpace(*in.APIKey)
		enc, err := s.box.Seal(key)
		if err != nil {
			return err
		}
		p.APIKeyEnc = enc
		p.APIKeyHint = ""
		if key != "" {
			p.APIKeyHint = secrets.Hint(key)
		}
	}
	if in.TierModels != nil {
		for k := range in.TierModels {
			if !storage.ValidTier(k) {
				return ErrBadTier
			}
		}
		p.TierModels = in.TierModels
	}
	if len(p.TierModels) == 0 {
		p.TierModels = llm.DefaultTierModels(p.Kind)
	}
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	needsKey := p.Kind == storage.ProviderAnthropic || p.Kind == storage.ProviderOpenAI
	if needsKey && p.APIKeyEnc == "" && p.APIKeyEnv == "" {
		return ErrNeedsKey
	}
	return nil
}

// Create adds a provider. The first provider becomes the default.
func (s *Service) Create(ctx context.Context, in Input) (storage.Provider, error) {
	p := storage.Provider{Enabled: true}
	if err := s.apply(&p, in); err != nil {
		return p, err
	}
	existing, err := s.store.Providers().List(ctx)
	if err != nil {
		return p, err
	}
	p.IsDefault = len(existing) == 0
	p, err = s.store.Providers().Create(ctx, p)
	if errors.Is(err, storage.ErrConflict) {
		return p, ErrNameTaken
	}
	return p, err
}

// Update changes a provider.
func (s *Service) Update(ctx context.Context, id string, in Input) (storage.Provider, error) {
	p, err := s.store.Providers().Get(ctx, id)
	if err != nil {
		return p, err
	}
	if err := s.apply(&p, in); err != nil {
		return p, err
	}
	if err := s.store.Providers().Update(ctx, p); errors.Is(err, storage.ErrConflict) {
		return p, ErrNameTaken
	} else if err != nil {
		return p, err
	}
	return s.store.Providers().Get(ctx, id)
}

// APIKey returns the usable key: from the env var when configured, else decrypted.
func (s *Service) APIKey(p storage.Provider) (string, error) {
	if p.APIKeyEnv != "" {
		if v := os.Getenv(p.APIKeyEnv); v != "" {
			return v, nil
		}
		if p.APIKeyEnc == "" {
			return "", fmt.Errorf("biến môi trường %s chưa được đặt", p.APIKeyEnv)
		}
	}
	return s.box.Open(p.APIKeyEnc)
}

// CLIBin returns the binary a CLI provider runs (explicit path, or the resolver's
// lookup, or the bare command name).
func (s *Service) CLIBin(p storage.Provider) string {
	if !p.Kind.IsCLI() {
		return ""
	}
	if p.BaseURL != "" {
		return p.BaseURL
	}
	bin := map[storage.ProviderKind]string{storage.ProviderClaudeCLI: "claude", storage.ProviderCodexCLI: "codex"}[p.Kind]
	if s.resolveBin != nil {
		if path := s.resolveBin(bin); path != "" {
			return path
		}
	}
	return bin
}

// Client builds an llm client for p.
func (s *Service) Client(p storage.Provider) (llm.Client, error) {
	if p.Kind.IsCLI() && p.BaseURL == "" && s.resolveBin != nil {
		bin := map[storage.ProviderKind]string{storage.ProviderClaudeCLI: "claude", storage.ProviderCodexCLI: "codex"}[p.Kind]
		if path := s.resolveBin(bin); path != "" {
			p.BaseURL = path
		}
	}
	key, err := s.APIKey(p)
	if err != nil {
		return nil, err
	}
	return llm.New(p, key, s.opts)
}

// TestResult is what the admin page shows after "Kiểm tra".
type TestResult struct {
	OK       bool             `json:"ok"`
	Detail   string           `json:"detail"`
	Models   []string         `json:"models"`
	Version  string           `json:"version,omitempty"`
	Response *llm.Result      `json:"response,omitempty"`
	Provider storage.Provider `json:"-"`
	// NeedsLogin marks a CLI provider whose account is signed out; CLITool is
	// the tool the dashboard can sign in without a terminal (claude | codex).
	NeedsLogin bool   `json:"needs_login,omitempty"`
	CLITool    string `json:"cli_tool,omitempty"`
}

// cliToolOf names the CLI the dashboard can install and sign in for p.
func cliToolOf(p storage.Provider) string {
	switch p.Kind {
	case storage.ProviderClaudeCLI:
		return "claude"
	case storage.ProviderCodexCLI:
		return "codex"
	}
	return ""
}

// Test checks connectivity (no tokens) and, when prompt is set, sends it to model
// (default: the provider's balanced tier model). The status is saved.
func (s *Service) Test(ctx context.Context, id, prompt, model string) (TestResult, error) {
	p, err := s.store.Providers().Get(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	fail := func(err error) (TestResult, error) {
		_ = s.store.Providers().SetStatus(ctx, id, "error", err.Error(), nil, s.now())
		res := TestResult{OK: false, Detail: err.Error()}
		if errors.Is(err, llm.ErrNeedsLogin) {
			res.NeedsLogin, res.CLITool = true, cliToolOf(p)
		}
		return res, nil
	}
	client, err := s.Client(p)
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	chk, err := client.Check(ctx)
	if err != nil {
		return fail(err)
	}
	res := TestResult{OK: true, Detail: chk.Detail, Models: chk.Models, Version: chk.Version}
	// Fill tiers the user left empty from the listed models (OpenAI, local servers).
	if len(chk.Models) > 0 && (p.TierModels[storage.TierStrong] == "" || p.TierModels[storage.TierBalanced] == "" || p.TierModels[storage.TierFast] == "") && !p.Kind.IsCLI() {
		p.TierModels = llm.SuggestTiers(p.TierModels, chk.Models)
		if err := s.store.Providers().Update(ctx, p); err != nil {
			return res, err
		}
	}
	if prompt != "" {
		if model == "" {
			model = p.TierModels[storage.TierBalanced]
		}
		if model == "" && len(chk.Models) > 0 {
			model = chk.Models[0]
		}
		out, err := s.Call(ctx, p, llm.Request{Model: model, Prompt: prompt, MaxTokens: 256}, usage.Meta{Kind: "provider_test"})
		var be *usage.BudgetError
		if errors.As(err, &be) {
			return TestResult{OK: false, Detail: err.Error()}, nil
		}
		if err != nil {
			if errors.Is(err, llm.ErrNeedsLogin) {
				return fail(err) // "connected" would be misleading: it is the account, not the tool
			}
			return fail(fmt.Errorf("kết nối được nhưng gửi prompt lỗi: %w", err))
		}
		res.Response = &out
		res.Detail = fmt.Sprintf("%s · trả lời bằng %s trong %d ms", chk.Detail, orStr(out.Model, model), out.DurationMS)
	}
	if err := s.store.Providers().SetStatus(ctx, id, "ok", res.Detail, chk.Models, s.now()); err != nil {
		return res, err
	}
	return res, nil
}

// ResolveModel returns the provider and model an agent runs on: the agent's
// provider (or the default one) and its explicit model (or the provider's tier model).
func (s *Service) ResolveModel(ctx context.Context, a storage.Agent) (storage.Provider, string, error) {
	var p storage.Provider
	var err error
	if a.ProviderID != "" {
		p, err = s.store.Providers().Get(ctx, a.ProviderID)
	} else {
		p, err = s.Default(ctx)
	}
	if err != nil {
		return p, "", err
	}
	model := a.LLMModel
	if model == "" {
		model = p.TierModels[a.ModelTier]
	}
	return p, model, nil
}

// Choice is one connection an agent can run on, with the model it uses there.
type Choice struct {
	Provider storage.Provider
	Model    string
}

// Chain is every connection an agent may run on, in the order to try them:
// its own (ResolveModel) first, then its fallbacks top to bottom. A fallback
// runs the agent's tier model of that connection (an explicit model belongs
// to the first one); deleted, disabled or repeated ones are left out.
func (s *Service) Chain(ctx context.Context, a storage.Agent) ([]Choice, error) {
	p, model, err := s.ResolveModel(ctx, a)
	if err != nil {
		return nil, err
	}
	out := []Choice{{Provider: p, Model: model}}
	seen := map[string]bool{p.ID: true}
	for _, id := range a.FallbackProviderIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		fp, err := s.store.Providers().Get(ctx, id)
		if err != nil || !fp.Enabled {
			continue
		}
		out = append(out, Choice{Provider: fp, Model: fp.TierModels[a.ModelTier]})
	}
	return out, nil
}

// Default returns the default provider.
func (s *Service) Default(ctx context.Context) (storage.Provider, error) {
	list, err := s.store.Providers().List(ctx)
	if err != nil {
		return storage.Provider{}, err
	}
	for _, p := range list {
		if p.IsDefault {
			return p, nil
		}
	}
	return storage.Provider{}, storage.ErrNotFound
}

func normURL(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Box is the secrets box (other settings keep their secrets with it too).
func (s *Service) Box() *secrets.Box { return s.box }
