package api_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// A page open on the dashboard hears what changed, whoever changed it (here
// a message written straight to the store, as a bot or an agent does).
func TestEventsStream(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, "GET", e.srv.URL+"/api/events", nil)
	resp, err := admin.Do(req)
	if err != nil || resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events = %v %v", resp, err)
	}
	defer resp.Body.Close()
	go func() {
		time.Sleep(100 * time.Millisecond)
		e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "từ Discord", CreatedBy: "discord:an", Purpose: "channel"})
	}()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data:") && strings.Contains(line, "conversations") {
			return
		}
	}
	t.Fatal("no change heard")
}

// An admin's page is sent the machine's figures without asking; turning the
// "machine" topic on brings the whole picture (processes) to that page.
func TestEventsPushStats(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	rctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, "GET", e.srv.URL+"/api/events", nil)
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	next := func(want string) string { // the data of the next event named want
		name := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				name = v
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok && name == want {
				return v
			}
		}
		t.Fatalf("no %s event", want)
		return ""
	}
	hello := next("hello")
	if stats := next("stats"); !strings.Contains(stats, `"cpu_percent"`) || !strings.Contains(stats, `"agents"`) {
		t.Fatalf("stats = %s", stats)
	}
	sid := strings.TrimSuffix(strings.TrimPrefix(hello, `{"sid":`), "}")
	if res, _ := do(t, admin, "POST", e.srv.URL+"/api/events/topics", map[string]any{"sid": atoi(sid), "topic": "machine", "on": true}, nil); res.StatusCode != 204 {
		t.Fatalf("topic = %d", res.StatusCode)
	}
	if m := next("machine"); !strings.Contains(m, `"groups"`) || !strings.Contains(m, `"mem_total"`) {
		t.Fatalf("machine = %.200s", m)
	}
	if res, _ := do(t, admin, "POST", e.srv.URL+"/api/events/topics", map[string]any{"sid": 99999, "topic": "machine", "on": true}, nil); res.StatusCode != 404 {
		t.Fatalf("someone else's page = %d", res.StatusCode)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// A message written (by a bot, an agent) is pushed with its data, and the
// chat's row with it; a page also gets "Cần xử lý" without asking.
func TestEventsPushChat(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(rctx, "GET", e.srv.URL+"/api/events", nil)
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	got := map[string]string{}
	go func() {
		time.Sleep(200 * time.Millisecond)
		c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "từ Discord", CreatedBy: "discord:an", Purpose: "channel"})
		e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Author: "discord:an", Content: "xin chào office"})
	}()
	name := ""
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			name = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok {
			if _, seen := got[name]; !seen {
				got[name] = v
			}
		}
		if strings.Contains(got["message"], "xin chào office") && strings.Contains(got["conversation"], "từ Discord") && got["incidents"] != "" {
			return
		}
	}
	t.Fatalf("pushed = %v", got)
}
