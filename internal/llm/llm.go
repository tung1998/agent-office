// Package llm talks to model providers: HTTP APIs (Anthropic, OpenAI,
// OpenAI-compatible) and local CLIs (claude, codex, gemini, agy).
//
// This is the thin connection layer used to test a provider and send a single
// prompt. The agent runtime (tool use, sessions, streaming) builds on top later.
package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// Request is one single-turn prompt.
type Request struct {
	Model     string
	System    string
	Prompt    string
	MaxTokens int
}

// Result is the answer plus usage.
type Result struct {
	Text         string  `json:"text"`
	Model        string  `json:"model"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd,omitempty"` // only when the provider reports it (CLI)
	DurationMS   int64   `json:"duration_ms"`
}

// CheckResult describes a reachable provider.
type CheckResult struct {
	Models  []string `json:"models"`            // model ids available, when the provider can list them
	Version string   `json:"version,omitempty"` // CLI version
	Detail  string   `json:"detail"`
}

// Client is a connection to one provider.
type Client interface {
	Check(ctx context.Context) (CheckResult, error)
	Complete(ctx context.Context, req Request) (Result, error)
}

// ErrNoAPIKey means an API provider has no key configured.
var ErrNoAPIKey = errors.New("llm: provider has no API key")

// ErrNeedsLogin means a CLI provider is installed but its account is signed
// out (or the session expired): the office can sign in from the dashboard
// instead of asking the person to open a terminal.
var ErrNeedsLogin = errors.New("chưa đăng nhập")

// Options tweak construction (tests inject an HTTP client).
type Options struct {
	HTTPClient *http.Client
}

// New builds the client for p. apiKey is the decrypted key (empty for CLIs).
func New(p storage.Provider, apiKey string, opts Options) (Client, error) {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	switch p.Kind {
	case storage.ProviderAnthropic:
		if apiKey == "" {
			return nil, ErrNoAPIKey
		}
		return &anthropic{base: orDefault(p.BaseURL, "https://api.anthropic.com"), key: apiKey, http: hc}, nil
	case storage.ProviderOpenAI:
		if apiKey == "" {
			return nil, ErrNoAPIKey
		}
		return &openAI{base: orDefault(p.BaseURL, "https://api.openai.com/v1"), key: apiKey, http: hc, official: true}, nil
	case storage.ProviderOpenAICompatible:
		if p.BaseURL == "" {
			return nil, errors.New("llm: openai_compatible needs base_url (e.g. http://localhost:11434/v1)")
		}
		return &openAI{base: p.BaseURL, key: apiKey, http: hc}, nil
	case storage.ProviderClaudeCLI:
		return &claudeCLI{bin: orDefault(p.BaseURL, "claude"), env: p.CLIEnv()}, nil
	case storage.ProviderCodexCLI:
		return &codexCLI{bin: orDefault(p.BaseURL, "codex"), env: p.CLIEnv()}, nil
	case storage.ProviderGeminiCLI:
		return &geminiCLI{bin: orDefault(p.BaseURL, "gemini"), env: p.CLIEnv()}, nil
	case storage.ProviderAntigravityCLI:
		return &antigravityCLI{bin: orDefault(p.BaseURL, "agy")}, nil
	case storage.ProviderDSH:
		// turns run in dsh; checks and one-off calls go to the same API directly
		if apiKey == "" {
			return nil, ErrNoAPIKey
		}
		api, base := HarnessAPI(p.BaseURL)
		if api == HarnessAnthropic {
			return &anthropic{base: base, key: apiKey, http: hc}, nil
		}
		return &openAI{base: base, key: apiKey, http: hc}, nil
	}
	return nil, fmt.Errorf("llm: unknown provider kind %q", p.Kind)
}

// Wire protocols DeepSeek Harness speaks to a model API.
const (
	HarnessOpenAI    = "openai-completions"
	HarnessAnthropic = "anthropic-messages"
)

// DeepSeekAPI is DeepSeek's own API, a harness connection's default.
const DeepSeekAPI = "https://api.deepseek.com/v1"

// HarnessAPI is the protocol and endpoint of a harness connection's base
// URL: an Anthropic Messages endpoint when the URL names anthropic
// (api.anthropic.com, …/anthropic gateways), else Chat Completions; ""
// is DeepSeek's own API.
func HarnessAPI(baseURL string) (api, base string) {
	base = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return HarnessOpenAI, DeepSeekAPI
	}
	if strings.Contains(strings.ToLower(base), "anthropic") {
		// the Anthropic SDK adds /v1/messages itself
		return HarnessAnthropic, strings.TrimSuffix(base, "/v1")
	}
	return HarnessOpenAI, base
}

// DefaultTierModels suggests tier → model for a new provider. Empty means the
// user picks after the first connection test lists the models.
func DefaultTierModels(kind storage.ProviderKind) map[string]string {
	switch kind {
	case storage.ProviderAnthropic:
		return map[string]string{
			storage.TierStrong:   "claude-opus-5-5",
			storage.TierBalanced: "claude-sonnet-5-5",
			storage.TierFast:     "claude-haiku-5-5",
		}
	case storage.ProviderClaudeCLI:
		// aliases follow the newest model of each family
		return map[string]string{
			storage.TierStrong:   "opus",
			storage.TierBalanced: "sonnet",
			storage.TierFast:     "haiku",
		}
	case storage.ProviderGeminiCLI:
		return map[string]string{
			storage.TierStrong:   "pro",
			storage.TierBalanced: "flash",
			storage.TierFast:     "flash-lite",
		}
	}
	return map[string]string{}
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
