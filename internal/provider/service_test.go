package provider_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func setup(t *testing.T) (*provider.Service, storage.Store, *httptest.Server) {
	dir := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(dir, "o.db"))
	t.Cleanup(func() { st.Close() })
	st.Migrate(context.Background())
	box, err := secrets.Load(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-ant-good-key-1234" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"message":"invalid x-api-key"}}`))
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"claude-sonnet-5"}]}`))
			return
		}
		w.Write([]byte(`{"model":"claude-sonnet-5","content":[{"type":"text","text":"xin chào"}],"usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return provider.NewService(st, box, llm.Options{}), st, srv
}

func ptr(s string) *string { return &s }

func TestCreateEncryptsAndTests(t *testing.T) {
	svc, st, srv := setup(t)
	ctx := context.Background()

	if _, err := svc.Create(ctx, provider.Input{Name: "x", Kind: storage.ProviderAnthropic}); !errors.Is(err, provider.ErrNeedsKey) {
		t.Fatalf("missing key err = %v", err)
	}
	p, err := svc.Create(ctx, provider.Input{Name: "Claude", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: ptr("sk-ant-good-key-1234")})
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsDefault || p.APIKeyHint != "…1234" || strings.Contains(p.APIKeyEnc, "good-key") || p.TierModels["strong"] == "" {
		t.Fatalf("created = %+v", p)
	}
	res, err := svc.Test(ctx, p.ID, "chào", "")
	if err != nil || !res.OK || res.Response == nil || res.Response.Text != "xin chào" {
		t.Fatalf("test = %+v, %v", res, err)
	}
	got, _ := st.Providers().Get(ctx, p.ID)
	if got.Status != "ok" || len(got.Models) != 1 {
		t.Fatalf("status not saved: %+v", got)
	}

	// update without key keeps it; wrong key marks error
	if _, err := svc.Update(ctx, p.ID, provider.Input{Name: "Claude API", Kind: storage.ProviderAnthropic, BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if res, _ := svc.Test(ctx, p.ID, "", ""); !res.OK {
		t.Fatalf("key lost on update: %+v", res)
	}
	svc.Update(ctx, p.ID, provider.Input{Name: "Claude API", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: ptr("sk-ant-bad-key-99999")})
	res, _ = svc.Test(ctx, p.ID, "", "")
	if res.OK || !strings.Contains(res.Detail, "invalid x-api-key") {
		t.Fatalf("bad key result = %+v", res)
	}
	if got, _ := st.Providers().Get(ctx, p.ID); got.Status != "error" {
		t.Fatalf("status = %s", got.Status)
	}

	// env var key
	t.Setenv("TEST_ANTHROPIC_KEY", "sk-ant-good-key-1234")
	e, err := svc.Create(ctx, provider.Input{Name: "Env", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKeyEnv: "TEST_ANTHROPIC_KEY"})
	if err != nil || e.IsDefault {
		t.Fatalf("env provider = %+v, %v", e, err)
	}
	if res, _ := svc.Test(ctx, e.ID, "", ""); !res.OK {
		t.Fatalf("env key test = %+v", res)
	}
	if _, err := svc.Create(ctx, provider.Input{Name: "Env", Kind: storage.ProviderClaudeCLI}); !errors.Is(err, provider.ErrNameTaken) {
		t.Fatalf("dup name err = %v", err)
	}
}

func TestResolveModel(t *testing.T) {
	svc, _, srv := setup(t)
	ctx := context.Background()
	def, _ := svc.Create(ctx, provider.Input{Name: "Claude", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: ptr("sk-ant-good-key-1234")})
	other, _ := svc.Create(ctx, provider.Input{Name: "Local", Kind: storage.ProviderOpenAICompatible, BaseURL: "http://x/v1",
		TierModels: map[string]string{"fast": "llama-small"}})

	p, m, err := svc.ResolveModel(ctx, storage.Agent{ModelTier: "strong"})
	if err != nil || p.ID != def.ID || m != "claude-opus-5-5" {
		t.Fatalf("default tier = %s %s %v", p.Name, m, err)
	}
	p, m, _ = svc.ResolveModel(ctx, storage.Agent{ProviderID: other.ID, ModelTier: "fast"})
	if p.ID != other.ID || m != "llama-small" {
		t.Fatalf("agent provider tier = %s %s", p.Name, m)
	}
	_, m, _ = svc.ResolveModel(ctx, storage.Agent{ModelTier: "fast", LLMModel: "claude-sonnet-5"})
	if m != "claude-sonnet-5" {
		t.Fatalf("explicit model = %s", m)
	}
}

func TestTestFillsTiersFromModels(t *testing.T) {
	_, st, _ := setup(t)
	dir := t.TempDir()
	box, _ := secrets.Load(filepath.Join(dir, "k"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"gpt-5-mini"},{"id":"text-embedding-3-small"}]}`))
	}))
	defer srv.Close()
	svc := provider.NewService(st, box, llm.Options{})
	p, err := svc.Create(context.Background(), provider.Input{Name: "GPT", Kind: storage.ProviderOpenAI, BaseURL: srv.URL, APIKey: ptr("sk-openai-test-1234")})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := svc.Test(context.Background(), p.ID, "", ""); !res.OK {
		t.Fatalf("test = %+v", res)
	}
	got, _ := st.Providers().Get(context.Background(), p.ID)
	if got.TierModels["strong"] != "gpt-5" || got.TierModels["fast"] != "gpt-5-mini" {
		t.Fatalf("tiers = %v", got.TierModels)
	}
}

// Re-pointing a connection at another host must not carry the stored key
// along: otherwise editing the URL is enough to exfiltrate it.
func TestBaseURLChangeDropsStoredKey(t *testing.T) {
	svc, st, srv := setup(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, provider.Input{Name: "Claude", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: ptr("sk-ant-good-key-1234")})

	if _, err := svc.Update(ctx, p.ID, provider.Input{Name: "Claude", Kind: storage.ProviderAnthropic, BaseURL: "https://evil.example"}); !errors.Is(err, provider.ErrKeyRequiredForNewURL) {
		t.Fatalf("url change without key err = %v", err)
	}
	got, _ := st.Providers().Get(ctx, p.ID)
	if got.BaseURL != srv.URL || got.APIKeyEnc == "" {
		t.Fatalf("rejected update must not change anything: %+v", got)
	}
	// same URL keeps the key; new URL with a new key is fine
	if _, err := svc.Update(ctx, p.ID, provider.Input{Name: "Claude 2", Kind: storage.ProviderAnthropic, BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(ctx, p.ID, provider.Input{Name: "Claude 2", Kind: storage.ProviderAnthropic, BaseURL: srv.URL + "/", APIKey: ptr("sk-ant-good-key-1234")}); err != nil {
		t.Fatal(err)
	}
	// a keyless compatible endpoint can move freely
	c, _ := svc.Create(ctx, provider.Input{Name: "Local", Kind: storage.ProviderOpenAICompatible, BaseURL: "http://a/v1"})
	if _, err := svc.Update(ctx, c.ID, provider.Input{Name: "Local", Kind: storage.ProviderOpenAICompatible, BaseURL: "http://b/v1"}); err != nil {
		t.Fatalf("keyless move = %v", err)
	}
}

func TestCLIProviderUsesBinResolver(t *testing.T) {
	svc, _, _ := setup(t)
	ctx := context.Background()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte("#!/bin/sh\necho '9.9.9 (Claude Code)'\n"), 0o755)
	p, _ := svc.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI})
	svc.SetBinResolver(func(name string) string {
		if name == "claude" {
			return bin
		}
		return ""
	})
	if res, _ := svc.Test(ctx, p.ID, "", ""); !res.OK || res.Version != "9.9.9 (Claude Code)" {
		t.Fatalf("test via resolver = %+v", res)
	}
}
