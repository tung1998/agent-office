package composio_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/composio"
)

func sign(secret, id string, ts int64, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(id + "." + strconv.FormatInt(ts, 10) + "." + body))
	return "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func TestVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := `{"type":"composio.trigger.message"}`
	h := http.Header{}
	h.Set("webhook-id", "msg_1")
	h.Set("webhook-timestamp", strconv.FormatInt(now.Unix(), 10))
	h.Set("webhook-signature", "v1,bogus "+sign("sec", "msg_1", now.Unix(), body))
	if err := composio.Verify("sec", h, []byte(body), now); err != nil {
		t.Fatalf("good delivery refused: %v", err)
	}
	if composio.Verify("other", h, []byte(body), now) == nil {
		t.Error("wrong secret taken")
	}
	if composio.Verify("sec", h, []byte(body+" "), now) == nil {
		t.Error("changed body taken")
	}
	if composio.Verify("sec", h, []byte(body), now.Add(10*time.Minute)) == nil {
		t.Error("old delivery taken")
	}
	// a whsec_ secret signs with its base64 bytes (Standard Webhooks)
	raw := []byte("0123456789abcdef")
	whsec := "whsec_" + base64.StdEncoding.EncodeToString(raw)
	h.Set("webhook-signature", sign(string(raw), "msg_1", now.Unix(), body))
	if err := composio.Verify(whsec, h, []byte(body), now); err != nil {
		t.Fatalf("whsec secret: %v", err)
	}
}

func TestClient(t *testing.T) {
	var patched map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "ak_good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/triggers_types":
			if r.URL.Query().Get("cursor") == "" {
				_, _ = w.Write([]byte(`{"items":[{"slug":"gmail_new_gmail_message","name":"New email","description":"d","toolkit":{"slug":"gmail"}}],"next_cursor":"p2"}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":[{"slug":"GMAIL_LABEL_ADDED","name":"Label","toolkit":{"slug":"gmail"}}],"next_cursor":null}`))
		case r.Method == "GET" && r.URL.Path == "/webhook_subscriptions":
			_, _ = w.Write([]byte(`{"items":[{"id":"whs_1","webhook_url":"https://old","enabled_events":["composio.connected_account.expired"],"secret":"s1"}]}`))
		case r.Method == "PATCH" && r.URL.Path == "/webhook_subscriptions/whs_1":
			_ = json.NewDecoder(r.Body).Decode(&patched)
			_, _ = w.Write([]byte(`{"id":"whs_1","webhook_url":"https://new","secret":"s1"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	c := composio.Client{Key: "ak_good", Base: srv.URL}
	list, err := c.TriggerTypes(ctx, []string{"Gmail"}, 10)
	if err != nil || len(list) != 2 || list[0].Slug != "GMAIL_NEW_GMAIL_MESSAGE" || list[0].Toolkit != "gmail" {
		t.Fatalf("triggers = %v %v", list, err)
	}
	if l, _ := c.TriggerTypes(ctx, nil, 1); len(l) != 1 {
		t.Errorf("max not kept: %d", len(l))
	}
	sub, err := c.Subscribe(ctx, "https://new")
	if err != nil || sub.ID != "whs_1" || sub.Secret != "s1" {
		t.Fatalf("subscribe = %+v %v", sub, err)
	}
	if ev, _ := patched["enabled_events"].([]any); len(ev) != 2 || patched["version"] != "V3" {
		t.Errorf("patch = %v", patched)
	}
	if _, err := (composio.Client{Key: "bad", Base: srv.URL}).TriggerTypes(ctx, nil, 1); !errors.Is(err, composio.ErrUnauthorized) {
		t.Errorf("bad key = %v", err)
	}
}
