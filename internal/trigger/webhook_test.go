package trigger_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

func TestWebhookAuthDedupeDebounce(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	r := trigger.New(st, &fakeExec{})
	secret, hash := trigger.NewSecret()
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "jira", Source: "webhook", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{Auth: "bearer", SecretHash: hash},
		Limits: storage.AutomationLimits{DebounceSeconds: 30, DebounceKey: "issue.key", DebounceMaxSeconds: 60}})
	srv := httptest.NewServer(r.Webhook())
	defer srv.Close()
	post := func(token, body, idem string) (int, map[string]string) {
		req, _ := http.NewRequest("POST", srv.URL+"/hooks/"+a.ID, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if idem != "" {
			req.Header.Set("Idempotency-Key", idem)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]string
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := post("wrong", `{}`, ""); code != 404 {
		t.Fatalf("bad token = %d", code)
	}
	code, first := post(secret, `{"issue":{"key":"SP-1"}}`, "e1")
	if code != 202 || first["status"] != "debounced" {
		t.Fatalf("first = %d %v", code, first)
	}
	if code, dup := post(secret, `{"issue":{"key":"SP-1"}}`, "e1"); code != 200 || dup["status"] != "duplicate" || dup["job_id"] != first["job_id"] {
		t.Fatalf("dup = %d %v", code, dup)
	}
	// same issue again: same job, pushed back but never past the max wait
	if _, again := post(secret, `{"issue":{"key":"SP-1","v":2}}`, "e2"); again["job_id"] != first["job_id"] {
		t.Fatalf("debounce made a new job: %v", again)
	}
	j, _ := st.Jobs().Get(ctx, first["job_id"])
	if !strings.Contains(j.Payload, `"v":2`) || j.NextAttemptAt.After(*j.DebounceUntil) {
		t.Fatalf("job = %+v", j)
	}
	// status endpoint with the same token
	req, _ := http.NewRequest("GET", srv.URL+"/hooks/"+a.ID+"/jobs/"+j.ID, nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
}
