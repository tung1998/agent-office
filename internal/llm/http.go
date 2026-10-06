package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// apiError keeps the provider's message but never the request (which holds the key).
type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Msg) }

func doJSON(ctx context.Context, hc *http.Client, method, url string, headers map[string]string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &apiError{Status: resp.StatusCode, Msg: errorMessage(raw)}
	}
	return json.Unmarshal(raw, out)
}

// errorMessage extracts {"error":{"message":..}} (both vendors) or a short body.
func errorMessage(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// ---- Anthropic ----

type anthropic struct {
	base string
	key  string
	http *http.Client
}

func (a *anthropic) headers() map[string]string {
	return map[string]string{"x-api-key": a.key, "anthropic-version": "2023-06-01"}
}

func (a *anthropic) Check(ctx context.Context) (CheckResult, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doJSON(ctx, a.http, http.MethodGet, strings.TrimRight(a.base, "/")+"/v1/models?limit=100", a.headers(), nil, &out); err != nil {
		return CheckResult{}, err
	}
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, m.ID)
	}
	return CheckResult{Models: models, Detail: fmt.Sprintf("%d model", len(models))}, nil
}

func (a *anthropic) Complete(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": maxTokens(req.MaxTokens),
		"messages":   []map[string]string{{"role": "user", "content": req.Prompt}},
	}
	if req.System != "" {
		body["system"] = req.System
	}
	var out struct {
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := doJSON(ctx, a.http, http.MethodPost, strings.TrimRight(a.base, "/")+"/v1/messages", a.headers(), body, &out); err != nil {
		return Result{}, err
	}
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return Result{Text: text.String(), Model: out.Model, InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens,
		DurationMS: time.Since(start).Milliseconds()}, nil
}

// ---- OpenAI and compatible ----

type openAI struct {
	base     string
	key      string
	http     *http.Client
	official bool // api.openai.com wants max_completion_tokens; compatible servers expect max_tokens
}

func (o *openAI) headers() map[string]string {
	if o.key == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + o.key}
}

func (o *openAI) Check(ctx context.Context) (CheckResult, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doJSON(ctx, o.http, http.MethodGet, strings.TrimRight(o.base, "/")+"/models", o.headers(), nil, &out); err != nil {
		return CheckResult{}, err
	}
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		models = append(models, m.ID)
	}
	sort.Strings(models)
	return CheckResult{Models: models, Detail: fmt.Sprintf("%d model", len(models))}, nil
}

func (o *openAI) Complete(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	msgs := []map[string]string{}
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": req.Prompt})
	body := map[string]any{"model": req.Model, "messages": msgs}
	if o.official {
		body["max_completion_tokens"] = maxTokens(req.MaxTokens)
	} else {
		body["max_tokens"] = maxTokens(req.MaxTokens)
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := doJSON(ctx, o.http, http.MethodPost, strings.TrimRight(o.base, "/")+"/chat/completions", o.headers(), body, &out); err != nil {
		return Result{}, err
	}
	text := ""
	if len(out.Choices) > 0 {
		text = out.Choices[0].Message.Content
	}
	return Result{Text: text, Model: out.Model, InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens,
		DurationMS: time.Since(start).Milliseconds()}, nil
}

func maxTokens(n int) int {
	if n <= 0 {
		return 1024
	}
	return n
}
