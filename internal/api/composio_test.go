package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/api"
)

// The Composio connection (ADR-282): the key is checked and kept sealed,
// trigger types come from Composio, and subscribing points Composio at the
// webhook with a new token, after which deliveries there must be signed.
func TestComposioAPI(t *testing.T) {
	var hookURL string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "ak_good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/triggers_types":
			_, _ = w.Write([]byte(`{"items":[{"slug":"GMAIL_NEW_GMAIL_MESSAGE","name":"New email","toolkit":{"slug":"gmail"}}]}`))
		case r.Method == "GET" && r.URL.Path == "/webhook_subscriptions":
			_, _ = w.Write([]byte(`{"items":[]}`))
		case r.Method == "POST" && r.URL.Path == "/webhook_subscriptions":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			hookURL, _ = in["webhook_url"].(string)
			_, _ = w.Write([]byte(`{"id":"whs_1","secret":"sign-secret"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	defer api.SetComposioBase(fake.URL)()

	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "GET", e.srv.URL+"/api/composio", nil, nil); resp.StatusCode != 403 {
		t.Fatalf("member reads composio = %d", resp.StatusCode)
	}
	if resp, body := do(t, admin, "PUT", e.srv.URL+"/api/composio", map[string]string{"api_key": "bad"}, nil); resp.StatusCode != 400 {
		t.Fatalf("bad key = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, admin, "PUT", e.srv.URL+"/api/composio", map[string]string{"api_key": "ak_good"}, nil); resp.StatusCode != 200 || body["connected"] != true {
		t.Fatalf("connect = %d %v", resp.StatusCode, body)
	}
	_, body := do(t, admin, "GET", e.srv.URL+"/api/composio", nil, nil)
	if body["connected"] != true || strings.Contains(body["key_hint"].(string), "ak_good") {
		t.Fatalf("get = %v", body)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/composio/triggers?toolkit=gmail", nil, nil)
	if list, _ := body["triggers"].([]any); len(list) != 1 {
		t.Fatalf("triggers = %v", body)
	}

	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "Mail", "source": "webhook", "action": "chat", "prompt": "{{payload.data}}",
		"config": map[string]any{"auth": "query", "auth_name": "token", "app_events": []string{"gmail_new_gmail_message", "bad slug!"}},
	}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	a := body["automation"].(map[string]any)
	if ev, _ := a["config"].(map[string]any)["app_events"].([]any); len(ev) != 1 || ev[0] != "GMAIL_NEW_GMAIL_MESSAGE" {
		t.Fatalf("app_events = %v", a["config"])
	}
	aid := a["id"].(string)
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/automations/"+aid+"/composio", map[string]string{"base_url": "http://office.local"}, nil); resp.StatusCode != 400 {
		t.Fatalf("http base = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/automations/"+aid+"/composio", map[string]string{"base_url": "https://office.example.com/"}, nil)
	if resp.StatusCode != 200 || body["subscription_id"] != "whs_1" || body["signed"] != true {
		t.Fatalf("subscribe = %d %v", resp.StatusCode, body)
	}
	u, err := url.Parse(hookURL)
	if err != nil || u.Host != "office.example.com" || u.Path != "/hooks/"+aid || u.Query().Get("token") == "" {
		t.Fatalf("hook url = %q", hookURL)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/composio", nil, nil)
	if body["signed"] != true || body["automation_id"] != aid {
		t.Fatalf("after subscribe = %v", body)
	}
	if resp, _ := do(t, admin, "DELETE", e.srv.URL+"/api/composio", nil, nil); resp.StatusCode != 204 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if _, body = do(t, admin, "GET", e.srv.URL+"/api/composio", nil, nil); body["connected"] != false {
		t.Fatalf("after delete = %v", body)
	}
}
