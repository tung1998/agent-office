package api_test

import (
	"context"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// "Sự cố": what needs a person across projects, each with where to fix it.
func TestIncidents(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	m, _ := e.st.Monitors().Create(ctx, storage.Monitor{ProjectID: pid, Name: "API", Type: "http", Target: "http://x", Enabled: true})
	m.Status, m.LastMessage = "down", "HTTP 500"
	e.st.Monitors().SaveStatus(ctx, m)
	e.st.Automations().Create(ctx, storage.Automation{ProjectID: pid, Name: "Báo cáo", Source: "schedule", Action: "script", DisabledCode: "failures", DisabledReason: "script lỗi"})
	ch, _ := e.st.Channels().Create(ctx, storage.Channel{ProjectID: pid, Kind: "discord", Name: "Bot", Enabled: true, TokenEnc: "x", Allow: []string{"*"}})
	e.st.Channels().SetStatus(ctx, ch.ID, "bot", "discord: token không hợp lệ", nil)
	now := time.Now().UTC()
	e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "task", Origin: "user", Status: "failed", Error: "x", Title: "Sửa lỗi", FinishedAt: &now})

	_, b := do(t, admin, "GET", e.srv.URL+"/api/incidents", nil, nil)
	kinds := map[string]bool{}
	for _, x := range b["incidents"].([]any) {
		it := x.(map[string]any)
		kinds[it["kind"].(string)] = true
		if it["project_id"] != pid || it["link"] == "" || it["title"] == "" {
			t.Errorf("incident = %v", it)
		}
	}
	for _, k := range []string{"monitor", "automation", "bot", "jobs"} {
		if !kinds[k] {
			t.Errorf("no %s incident in %v", k, b["incidents"])
		}
	}
	if b["count"] != float64(len(b["incidents"].([]any))) {
		t.Errorf("count = %v", b["count"])
	}
}

// An incident can be let go (it comes back only when it happens again) and a
// turned-off automation turned back on from the list.
func TestIncidentActions(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	a, _ := e.st.Automations().Create(ctx, storage.Automation{ProjectID: pid, Name: "Báo cáo", Source: "webhook", Action: "script", DisabledCode: "failures", DisabledReason: "script lỗi"})
	now := time.Now().UTC()
	e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "task", Origin: "user", Status: "failed", Error: "x", Title: "Sửa lỗi", FinishedAt: &now})
	list := func() map[string]map[string]any {
		_, b := do(t, admin, "GET", e.srv.URL+"/api/incidents", nil, nil)
		out := map[string]map[string]any{}
		for _, x := range b["incidents"].([]any) {
			it := x.(map[string]any)
			out[it["kind"].(string)] = it
		}
		return out
	}
	got := list()
	if got["automation"]["id"] != a.ID || got["jobs"]["id"] == "" || got["jobs"]["key"] == "" {
		t.Fatalf("incidents = %v", got)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/incidents/dismiss", map[string]any{"key": got["jobs"]["key"]}, nil); resp.StatusCode != 204 {
		t.Fatalf("dismiss = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/incidents/retry", map[string]any{"kind": "automation", "id": a.ID}, nil); resp.StatusCode != 200 {
		t.Fatalf("retry = %d", resp.StatusCode)
	}
	got = list()
	if _, ok := got["jobs"]; ok {
		t.Fatalf("a dismissed incident is back: %v", got["jobs"])
	}
	if _, ok := got["automation"]; ok {
		t.Fatalf("the automation is still off: %v", got["automation"])
	}
	// it happens again: back in the list
	later := time.Now().UTC().Add(time.Second)
	e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "task", Origin: "user", Status: "failed", Error: "y", Title: "Sửa lỗi 2", FinishedAt: &later, CreatedAt: later})
	if _, ok := list()["jobs"]; !ok {
		t.Fatal("a new failure after the dismissal is not shown")
	}
}

// The overview's recent chats: across projects, the latest first, bots' too.
func TestRecentConversations(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "cũ"})
	time.Sleep(5 * time.Millisecond)
	e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "từ discord", Purpose: "channel", CreatedBy: "discord:an"})
	e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "xây tự động hóa", Purpose: "automation"})
	_, b := do(t, admin, "GET", e.srv.URL+"/api/conversations/recent?limit=5", nil, nil)
	list, _ := b["conversations"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["title"] != "từ discord" || list[0].(map[string]any)["source"] != "discord" {
		t.Fatalf("recent = %v", b)
	}
	if b["projects"].(map[string]any)[pid] != "shop" {
		t.Fatalf("projects = %v", b["projects"])
	}
}

// The Job page: one row per piece of work, named after it.
func TestJobGroupsAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	conv, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "Sửa lỗi thanh toán"})
	for i := 0; i < 3; i++ {
		e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", Trigger: "discord", ConversationID: conv.ID, Status: "done", Title: "lượt"})
	}
	_, b := do(t, admin, "GET", e.srv.URL+"/api/jobs/groups?project="+pid, nil, nil)
	gs, _ := b["groups"].([]any)
	if len(gs) != 1 {
		t.Fatalf("groups = %v", b)
	}
	g := gs[0].(map[string]any)
	if g["title"] != "Sửa lỗi thanh toán" || g["runs"] != float64(3) || g["source"] != "discord" || g["link"] == "" || g["project_name"] != "shop" {
		t.Fatalf("group = %v", g)
	}
}

// A job group opens where its chat lives: the assistant's page, an automation builder's automation.
func TestJobGroupLinks(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	aid, _ := assistant.Ensure(ctx, e.st, orgmodel.NewService(e.st), t.TempDir())
	ac, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: aid, Title: "Báo cáo", CreatedBy: "human:admin@x.io"})
	e.st.Jobs().Create(ctx, storage.Job{ProjectID: aid, Kind: "chat_turn", Origin: "user", CreatedBy: "human:admin@x.io", ConversationID: ac.ID, Status: "done"})
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	bc, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "Tạo automation", Purpose: "automation", AutomationID: "aut_1"})
	e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", ConversationID: bc.ID, Status: "done"})
	_, b := do(t, admin, "GET", e.srv.URL+"/api/jobs/groups", nil, nil)
	links := map[string]string{}
	for _, x := range b["groups"].([]any) {
		g := x.(map[string]any)
		links[g["conversation_id"].(string)] = g["link"].(string)
	}
	if links[ac.ID] != "/assistant?c="+ac.ID || links[bc.ID] != "/projects/"+pid+"/automations/aut_1" {
		t.Fatalf("links = %v", links)
	}
}

// A chat's diff waiting for a person is in "Cần xử lý", next to the commands.
func TestPendingPatchIncident(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	conv, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "Sửa nút"})
	p, _ := e.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: conv.ID, Diff: "x", Files: []string{"a.vue"}, Status: "pending"})
	e.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: conv.ID, Diff: "y", Files: []string{"b.vue"}, Status: "applied"})
	_, b := do(t, admin, "GET", e.srv.URL+"/api/incidents", nil, nil)
	var found map[string]any
	for _, x := range b["incidents"].([]any) {
		if it := x.(map[string]any); it["kind"] == "patch" {
			if found != nil {
				t.Fatalf("two patch incidents: %v", b["incidents"])
			}
			found = it
		}
	}
	if found == nil || found["id"] != p.ID || found["project_id"] != pid || found["link"] == "" {
		t.Fatalf("patch incident = %v in %v", found, b["incidents"])
	}
}

// The assistant's rights are set by an admin and read back with it.
func TestAssistantMode(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	if resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/assistant/mode", map[string]any{"mode": "root"}, nil); resp.StatusCode != 400 {
		t.Fatalf("unknown mode = %d", resp.StatusCode)
	}
	if resp, b := do(t, admin, "PUT", e.srv.URL+"/api/assistant/mode", map[string]any{"mode": "answer"}, nil); resp.StatusCode != 200 || b["mode"] != "answer" {
		t.Fatalf("set = %d %v", resp.StatusCode, b)
	}
}

// A project's daily budget is set on the project; the other settings stay.
func TestProjectBudget(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	do(t, admin, "PUT", e.srv.URL+"/api/usage/settings", map[string]any{"daily_limit_usd": 50}, nil)
	if resp, b := do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/budget", map[string]any{"daily_limit_usd": 12.5}, nil); resp.StatusCode != 200 || b["daily_limit_usd"] != 12.5 {
		t.Fatalf("set = %d %v", resp.StatusCode, b)
	}
	_, b := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/budget", nil, nil)
	if b["daily_limit_usd"] != 12.5 || b["office_limit_usd"] != float64(50) {
		t.Fatalf("get = %v", b)
	}
	do(t, admin, "PUT", e.srv.URL+"/api/projects/"+pid+"/budget", map[string]any{"daily_limit_usd": 0}, nil)
	if _, b := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/budget", nil, nil); b["daily_limit_usd"] != float64(0) {
		t.Fatalf("cleared = %v", b)
	}
}
