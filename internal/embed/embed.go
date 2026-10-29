// Package embed finds the agents' notes by meaning without a service of its
// own (ADR-236): each note's embedding, from an OpenAI-compatible
// /embeddings endpoint (OpenAI, Gemini's OpenAI endpoint, Ollama, …), is
// kept in SQLite next to the notes; recall compares the query's with them.
// The notes stay the source (ADR-068): the vectors are rebuilt from them.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls an OpenAI-compatible embeddings endpoint.
type Client struct {
	URL, Key, Model string // URL: the API base, e.g. https://api.openai.com/v1
	HTTP            *http.Client
}

func newClient(url, key, model string) *Client {
	return &Client{URL: strings.TrimRight(url, "/"), Key: key, Model: model, HTTP: &http.Client{Timeout: time.Minute}}
}

// Embed is one vector per text, in their order.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"model": c.Model, "input": texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 { // the body, never the request (it holds the key)
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return nil, fmt.Errorf("embeddings: HTTP %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	for i, v := range vecs {
		if len(v) == 0 {
			return nil, fmt.Errorf("embeddings: no vector for input %d", i)
		}
	}
	return vecs, nil
}
