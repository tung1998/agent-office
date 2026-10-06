package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/storage"
)

func TestAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-test" || r.Header.Get("anthropic-version") == "" {
			w.WriteHeader(401)
			w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"data":[{"id":"claude-sonnet-5"},{"id":"claude-haiku-4-5"}]}`))
		case "/v1/messages":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["model"] != "claude-haiku-4-5" || body["system"] != "be brief" || body["max_tokens"].(float64) != 16 {
				t.Errorf("unexpected body %v", body)
			}
			w.Write([]byte(`{"model":"claude-haiku-4-5","content":[{"type":"text","text":"pong"}],"usage":{"input_tokens":12,"output_tokens":2}}`))
		}
	}))
	defer srv.Close()

	c, err := llm.New(storage.Provider{Kind: storage.ProviderAnthropic, BaseURL: srv.URL}, "sk-test", llm.Options{})
	if err != nil {
		t.Fatal(err)
	}
	chk, err := c.Check(context.Background())
	if err != nil || len(chk.Models) != 2 {
		t.Fatalf("Check = %+v, %v", chk, err)
	}
	res, err := c.Complete(context.Background(), llm.Request{Model: "claude-haiku-4-5", System: "be brief", Prompt: "ping", MaxTokens: 16})
	if err != nil || res.Text != "pong" || res.InputTokens != 12 || res.OutputTokens != 2 {
		t.Fatalf("Complete = %+v, %v", res, err)
	}

	bad, _ := llm.New(storage.Provider{Kind: storage.ProviderAnthropic, BaseURL: srv.URL}, "wrong", llm.Options{})
	_, err = bad.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid x-api-key") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("auth error = %v", err)
	}
	if _, err := llm.New(storage.Provider{Kind: storage.ProviderAnthropic}, "", llm.Options{}); !errors.Is(err, llm.ErrNoAPIKey) {
		t.Fatalf("no key err = %v", err)
	}
}

func TestOpenAIAndCompatible(t *testing.T) {
	var gotMax string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"data":[{"id":"b-model"},{"id":"a-model"}]}`))
		case "/v1/chat/completions":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if _, ok := body["max_completion_tokens"]; ok {
				gotMax = "max_completion_tokens"
			} else if _, ok := body["max_tokens"]; ok {
				gotMax = "max_tokens"
			}
			msgs := body["messages"].([]any)
			if msgs[0].(map[string]any)["role"] != "system" {
				t.Errorf("system message missing: %v", msgs)
			}
			w.Write([]byte(`{"model":"a-model","choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":5,"completion_tokens":1}}`))
		}
	}))
	defer srv.Close()

	official, _ := llm.New(storage.Provider{Kind: storage.ProviderOpenAI, BaseURL: srv.URL + "/v1"}, "sk-x", llm.Options{})
	chk, err := official.Check(context.Background())
	if err != nil || chk.Models[0] != "a-model" {
		t.Fatalf("Check = %+v, %v", chk, err)
	}
	res, err := official.Complete(context.Background(), llm.Request{Model: "a-model", System: "s", Prompt: "p"})
	if err != nil || res.Text != "hi" || res.InputTokens != 5 || gotMax != "max_completion_tokens" {
		t.Fatalf("official Complete = %+v, %v, %s", res, err, gotMax)
	}
	compat, err := llm.New(storage.Provider{Kind: storage.ProviderOpenAICompatible, BaseURL: srv.URL + "/v1"}, "", llm.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compat.Complete(context.Background(), llm.Request{Model: "a-model", System: "s", Prompt: "p"}); err != nil || gotMax != "max_tokens" {
		t.Fatalf("compatible Complete err=%v max=%s", err, gotMax)
	}
	if _, err := llm.New(storage.Provider{Kind: storage.ProviderOpenAICompatible}, "", llm.Options{}); err == nil {
		t.Fatal("compatible without base_url must fail")
	}
}

func fakeBin(t *testing.T, name, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClaudeCLI(t *testing.T) {
	bin := fakeBin(t, "claude", `
if [ "$1" = "--version" ]; then echo "9.9.9 (Claude Code)"; exit 0; fi
prompt=$(cat)
case "$*" in *"--model claude-haiku-4-5"*) ;; *) echo "bad args: $*" >&2; exit 2;; esac
echo "warning: something"
echo "{\"type\":\"result\",\"is_error\":false,\"result\":\"echo:$prompt\",\"total_cost_usd\":0.0012,\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}"
`)
	c, _ := llm.New(storage.Provider{Kind: storage.ProviderClaudeCLI, BaseURL: bin}, "", llm.Options{})
	chk, err := c.Check(context.Background())
	if err != nil || chk.Version != "9.9.9 (Claude Code)" || len(chk.Models) == 0 {
		t.Fatalf("Check = %+v, %v", chk, err)
	}
	res, err := c.Complete(context.Background(), llm.Request{Model: "claude-haiku-4-5", Prompt: "ping"})
	if err != nil || res.Text != "echo:ping" || res.CostUSD != 0.0012 || res.OutputTokens != 3 {
		t.Fatalf("Complete = %+v, %v", res, err)
	}
	missing, _ := llm.New(storage.Provider{Kind: storage.ProviderClaudeCLI, BaseURL: "/nonexistent/claude"}, "", llm.Options{})
	if _, err := missing.Check(context.Background()); err == nil {
		t.Fatal("missing binary must fail")
	}
}

func TestCodexCLI(t *testing.T) {
	bin := fakeBin(t, "codex", `
if [ "$1" = "--version" ]; then echo "codex-cli 1.2.3"; exit 0; fi
cat >/dev/null
echo '{"type":"thread.started"}'
echo '{"type":"item.completed","item":{"type":"agent_message","text":"done"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":9,"output_tokens":4}}'
`)
	c, _ := llm.New(storage.Provider{Kind: storage.ProviderCodexCLI, BaseURL: bin}, "", llm.Options{})
	if chk, err := c.Check(context.Background()); err != nil || chk.Version != "codex-cli 1.2.3" {
		t.Fatalf("Check = %+v, %v", chk, err)
	}
	res, err := c.Complete(context.Background(), llm.Request{Model: "gpt-x", Prompt: "hi"})
	if err != nil || res.Text != "done" || res.InputTokens != 9 {
		t.Fatalf("Complete = %+v, %v", res, err)
	}
}
