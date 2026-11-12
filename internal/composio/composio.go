// Package composio talks to Composio's REST API (v3.1) for the app events of
// ADR-282: the trigger types to pick from, and the one webhook subscription of
// a Composio project (where its events go, and the secret that signs them).
// It also checks a delivery's signature (Standard Webhooks).
package composio

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBase is Composio's API.
const DefaultBase = "https://backend.composio.dev/api/v3.1"

// Client calls Composio with one API key. It never follows a redirect: the
// key would go to wherever it points.
type Client struct {
	Key  string
	Base string // "" = DefaultBase
	HTTP *http.Client
}

// ErrUnauthorized: Composio refused the key.
var ErrUnauthorized = errors.New("Composio từ chối API key")

func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (c Client) do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	base := c.Base
	if base == "" {
		base = DefaultBase
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("x-api-key", c.Key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http().Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return res.StatusCode, ErrUnauthorized
	case res.StatusCode >= 300:
		return res.StatusCode, fmt.Errorf("Composio trả lỗi %d: %s", res.StatusCode, snippet(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return res.StatusCode, fmt.Errorf("không đọc được trả lời của Composio: %w", err)
		}
	}
	return res.StatusCode, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// TriggerType is one kind of event an app can send.
type TriggerType struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Toolkit     string `json:"toolkit"`
}

// TriggerTypes lists the trigger types, of these toolkits (gmail, slack…;
// none = all), at most max of them.
func (c Client) TriggerTypes(ctx context.Context, toolkits []string, max int) ([]TriggerType, error) {
	out := []TriggerType{}
	cursor := ""
	for len(out) < max {
		q := url.Values{"limit": {"50"}}
		for _, t := range toolkits {
			if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
				q.Add("toolkit_slugs", t)
			}
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Items []struct {
				Slug, Name, Description string
				Toolkit                 struct{ Slug string } `json:"toolkit"`
			} `json:"items"`
			Next *string `json:"next_cursor"`
		}
		if _, err := c.do(ctx, http.MethodGet, "/triggers_types?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			if len(out) >= max {
				break
			}
			out = append(out, TriggerType{Slug: strings.ToUpper(it.Slug), Name: it.Name, Description: it.Description, Toolkit: it.Toolkit.Slug})
		}
		if page.Next == nil || *page.Next == "" || len(page.Items) == 0 {
			break
		}
		cursor = *page.Next
	}
	return out, nil
}

// Subscription is where Composio delivers a project's events.
type Subscription struct {
	ID         string   `json:"id"`
	WebhookURL string   `json:"webhook_url"`
	Version    string   `json:"version"`
	Events     []string `json:"enabled_events"`
	Secret     string   `json:"secret"`
}

// Subscribe points the Composio project's one subscription at webhookURL
// (V3 payloads, trigger messages): it updates the one there is, else makes it.
func (c Client) Subscribe(ctx context.Context, webhookURL string) (Subscription, error) {
	in := map[string]any{"webhook_url": webhookURL, "enabled_events": []string{"composio.trigger.message"}, "version": "V3"}
	var list struct {
		Items []Subscription `json:"items"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/webhook_subscriptions", nil, &list); err != nil {
		return Subscription{}, err
	}
	var s Subscription
	if len(list.Items) > 0 {
		cur := list.Items[0]
		events := cur.Events // keep what else it delivers (connection expiry…)
		if !contains(events, "composio.trigger.message") {
			events = append(events, "composio.trigger.message")
		}
		in["enabled_events"] = events
		_, err := c.do(ctx, http.MethodPatch, "/webhook_subscriptions/"+url.PathEscape(cur.ID), in, &s)
		if err == nil && s.Secret == "" {
			s.Secret = cur.Secret
		}
		return s, err
	}
	_, err := c.do(ctx, http.MethodPost, "/webhook_subscriptions", in, &s)
	return s, err
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// MaxSkew is how old (or early) a signed delivery may be.
const MaxSkew = 5 * time.Minute

// ErrBadSignature: the delivery is not signed with the secret, or too old.
var ErrBadSignature = errors.New("chữ ký webhook Composio không đúng hoặc đã quá hạn")

// Verify checks a delivery against the subscription's secret (Standard
// Webhooks: HMAC-SHA256 of "id.timestamp.body", base64, in "v1,<sig>" among
// space-separated signatures). The secret is taken as given and, for a
// "whsec_" one, as its base64 bytes too.
func Verify(secret string, h http.Header, body []byte, now time.Time) error {
	id, ts, sigs := h.Get("webhook-id"), h.Get("webhook-timestamp"), h.Get("webhook-signature")
	if secret == "" || id == "" || ts == "" || sigs == "" {
		return ErrBadSignature
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	if d := now.Sub(time.Unix(sec, 0)); d > MaxSkew || d < -MaxSkew {
		return ErrBadSignature
	}
	keys := [][]byte{[]byte(secret)}
	if raw, ok := strings.CutPrefix(secret, "whsec_"); ok {
		if k, err := base64.StdEncoding.DecodeString(raw); err == nil {
			keys = append(keys, k)
		}
	}
	msg := id + "." + ts + "." + string(body)
	for _, k := range keys {
		m := hmac.New(sha256.New, k)
		m.Write([]byte(msg))
		want := base64.StdEncoding.EncodeToString(m.Sum(nil))
		for _, s := range strings.Fields(sigs) {
			_, v, ok := strings.Cut(s, ",")
			if !ok {
				v = s
			}
			if subtle.ConstantTimeCompare([]byte(v), []byte(want)) == 1 {
				return nil
			}
		}
	}
	return ErrBadSignature
}
