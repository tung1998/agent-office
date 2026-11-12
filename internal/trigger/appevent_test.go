package trigger_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/trigger"
)

func TestParseAppEventVersions(t *testing.T) {
	h := http.Header{}
	cases := []struct{ name, body, slug, account string }{
		{"v3", `{"id":"msg_1","type":"composio.trigger.message","metadata":{"trigger_slug":"gmail_new_gmail_message","trigger_id":"ti_1","connected_account_id":"ca_1"},"data":{"subject":"Hi"}}`, "GMAIL_NEW_GMAIL_MESSAGE", "ca_1"},
		{"v2", `{"type":"github_commit_event","data":{"sha":"a1","connection_id":"ca_2","trigger_id":"ti_2"},"log_id":"log_2"}`, "GITHUB_COMMIT_EVENT", "ca_2"},
		{"v1", `{"trigger_name":"slack_receive_message","trigger_id":"ti_3","connection_id":"ca_3","payload":{"text":"yo"},"log_id":"log_3"}`, "SLACK_RECEIVE_MESSAGE", "ca_3"},
	}
	for _, c := range cases {
		ev, ok := trigger.ParseAppEvent(h, []byte(c.body))
		if !ok || ev.Slug != c.slug || ev.Account != c.account || ev.ID == "" || len(ev.Data) == 0 {
			t.Errorf("%s: %+v %v", c.name, ev, ok)
		}
	}
	for _, body := range []string{`{}`, `{"type":"issue.created","data":{"k":1},"log_id":"x"}`, `{"issue":{"key":"SP-1"}}`, `not json`} {
		if ev, ok := trigger.ParseAppEvent(h, []byte(body)); ok {
			t.Errorf("%s taken as an app event: %+v", body, ev)
		}
	}
	h.Set("webhook-id", "wh_9")
	if ev, _ := trigger.ParseAppEvent(h, []byte(cases[0].body)); ev.ID != "wh_9" {
		t.Errorf("delivery id = %q", ev.ID)
	}
}

func TestCleanAppEvents(t *testing.T) {
	got := trigger.CleanAppEvents([]string{" gmail_new_gmail_message ", "GMAIL_NEW_GMAIL_MESSAGE", "", "*", "bad slug!"})
	if want := []string{"GMAIL_NEW_GMAIL_MESSAGE", "*"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// One URL in Composio: the event goes to every webhook of the project that
// lists its slug, once per delivery; others and other projects stay out.
func TestAppEventFansOutToWebhooksOfTheProject(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	r := trigger.New(st, &fakeExec{})
	secret, hash := trigger.NewSecret()
	mk := func(name string, events []string, enabled bool, extra ...func(*storage.Automation)) storage.Automation {
		a := storage.Automation{ProjectID: p.ID, Name: name, Source: "webhook", Action: "chat", Enabled: enabled,
			Config: storage.AutomationConfig{Auth: "query", AuthName: "token", SecretHash: hash, AppEvents: events}}
		for _, f := range extra {
			f(&a)
		}
		a, err := st.Automations().Create(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	entry := mk("composio", []string{"SLACK_RECEIVE_MESSAGE"}, true) // the URL set in Composio
	mail := mk("mail", []string{"gmail_new_gmail_message"}, true)
	mk("mail-off", []string{"GMAIL_NEW_GMAIL_MESSAGE"}, false)
	mk("plain", nil, true)
	mk("pr", []string{"GMAIL_NEW_GMAIL_MESSAGE"}, true, func(a *storage.Automation) { a.Config.PullRequest = true })
	srv := httptest.NewServer(r.Webhook())
	defer srv.Close()
	post := func(body, id string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL+"/hooks/"+entry.ID+"?token="+secret, strings.NewReader(body))
		req.Header.Set("webhook-id", id)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	gmail := `{"id":"msg_1","type":"composio.trigger.message","metadata":{"trigger_slug":"GMAIL_NEW_GMAIL_MESSAGE","connected_account_id":"ca_1"},"data":{"subject":"Invoice"}}`
	code, out := post(gmail, "wh_1")
	jobs, _ := out["jobs"].([]any)
	if code != 202 || len(jobs) != 1 || jobs[0].(map[string]any)["automation_id"] != mail.ID {
		t.Fatalf("gmail = %d %v", code, out)
	}
	j, _ := st.Jobs().Get(ctx, jobs[0].(map[string]any)["job_id"].(string))
	if !strings.Contains(j.Payload, `"event":"GMAIL_NEW_GMAIL_MESSAGE"`) || !strings.Contains(j.Payload, `"subject":"Invoice"`) {
		t.Fatalf("payload = %s", j.Payload)
	}
	// Composio retries the same delivery: no second run
	if _, again := post(gmail, "wh_1"); again["jobs"].([]any)[0].(map[string]any)["status"] != "duplicate" {
		t.Fatalf("retry = %v", again)
	}
	// an event nobody lists: answered, nothing runs
	if code, out := post(strings.Replace(gmail, "GMAIL_NEW_GMAIL_MESSAGE", "NOTION_PAGE_ADDED", 1), "wh_2"); code != 200 || out["status"] != "ignored" {
		t.Fatalf("unlisted = %d %v", code, out)
	}
	// the entry's own event runs the entry
	slack := `{"id":"msg_3","type":"composio.trigger.message","metadata":{"trigger_slug":"SLACK_RECEIVE_MESSAGE"},"data":{"text":"hi"}}`
	if _, out := post(slack, "wh_3"); out["jobs"].([]any)[0].(map[string]any)["automation_id"] != entry.ID {
		t.Fatalf("slack = %v", out)
	}
}

// Composio signs what it sends to the webhook office subscribed (ADR-282):
// a delivery there without a good signature runs nothing.
func TestAppEventNeedsSignatureOnceSubscribed(t *testing.T) {
	ctx := context.Background()
	st, p := openStore(t)
	r := trigger.New(st, &fakeExec{})
	secret, hash := trigger.NewSecret()
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: p.ID, Name: "mail", Source: "webhook", Action: "chat", Enabled: true,
		Config: storage.AutomationConfig{Auth: "query", AuthName: "token", SecretHash: hash, AppEvents: []string{"*"}}})
	r.SetAppSecret(func(_ context.Context, id string) string {
		if id == a.ID {
			return "sign-secret"
		}
		return ""
	})
	srv := httptest.NewServer(r.Webhook())
	defer srv.Close()
	body := `{"id":"msg_1","type":"composio.trigger.message","metadata":{"trigger_slug":"GMAIL_NEW_GMAIL_MESSAGE"},"data":{}}`
	post := func(sig string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/hooks/"+a.ID+"?token="+secret, strings.NewReader(body))
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set("webhook-id", "msg_1")
		req.Header.Set("webhook-timestamp", ts)
		if sig == "" {
			m := hmac.New(sha256.New, []byte("sign-secret"))
			m.Write([]byte("msg_1." + ts + "." + body))
			sig = "v1," + base64.StdEncoding.EncodeToString(m.Sum(nil))
		}
		req.Header.Set("webhook-signature", sig)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post("v1,forged"); code != 401 {
		t.Fatalf("forged = %d", code)
	}
	if code := post(""); code != 202 {
		t.Fatalf("signed = %d", code)
	}
}
