package api_test

import (
	"context"
	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// The chat list says where each chat started (web, a bot, an automation) and
// filters by it: a Discord chat opened from a job is in the list too.
func TestChatListBySource(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	for _, c := range []storage.Conversation{
		{ProjectID: pid, Title: "web", CreatedBy: "human:admin@x.io"},
		{ProjectID: pid, Title: "discord", CreatedBy: "discord:binh", Purpose: "channel"},
		{ProjectID: pid, Title: "telegram", CreatedBy: "telegram:an", Purpose: "channel"},
		{ProjectID: pid, Title: "auto", CreatedBy: "auto:Báo cáo"},
	} {
		if _, err := e.st.Chat().CreateConversation(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	titles := func(q string) map[string]string {
		_, b := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/conversations"+q, nil, nil)
		out := map[string]string{}
		for _, x := range b["conversations"].([]any) {
			c := x.(map[string]any)
			out[c["title"].(string)] = c["source"].(string)
		}
		return out
	}
	if got := titles("?source=all"); len(got) != 4 || got["web"] != "web" || got["discord"] != "discord" || got["telegram"] != "telegram" || got["auto"] != "auto" {
		t.Fatalf("all = %v", got)
	}
	if got := titles("?source=discord"); len(got) != 1 || got["discord"] != "discord" {
		t.Fatalf("discord = %v", got)
	}
	if got := titles("?source=web"); len(got) != 1 || got["web"] != "web" {
		t.Fatalf("web = %v", got)
	}
	if got := titles(""); len(got) != 2 || got["web"] == "" || got["auto"] == "" { // as before: the project's own chats
		t.Fatalf("default = %v", got)
	}
}

// A chat that helps write a skill (its answers fill the editor): its own
// purpose, and in the project's chat list so it can be found again.
func TestSkillChatPurpose(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "GET", e.srv.URL+"/api/templates", nil, nil)
	solo := ""
	for _, x := range body["templates"].([]any) {
		if m := x.(map[string]any); m["key"] == "solo" {
			solo = m["id"].(string)
		}
	}
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop", "template_id": solo}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	resp, b := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/conversations", map[string]any{"purpose": "skill"}, nil)
	if resp.StatusCode != 201 || b["conversation"].(map[string]any)["purpose"] != "skill" {
		t.Fatalf("create = %d %v", resp.StatusCode, b)
	}
	_, b = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/conversations?source=all", nil, nil)
	if cs := b["conversations"].([]any); len(cs) != 1 || cs[0].(map[string]any)["purpose"] != "skill" {
		t.Fatalf("the skill chat is not in the list: %v", b)
	}
}

// The Jobs page filters by where a job came from and finds them by title.
func TestJobsBySourceAndTitle(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	for _, j := range []storage.Job{
		{ProjectID: pid, Kind: "chat_turn", Origin: "user", Trigger: "ui", Title: "hỏi về đơn", Status: "done"},
		{ProjectID: pid, Kind: "chat_turn", Origin: "automation", Trigger: "discord", Title: "đơn 123 đâu", Status: "done"},
		{ProjectID: pid, Kind: "script", Origin: "automation", Trigger: "schedule", Title: "báo cáo sáng", Status: "done"},
	} {
		e.st.Jobs().Create(ctx, j)
	}
	titles := func(q string) []string {
		_, b := do(t, admin, "GET", e.srv.URL+"/api/jobs?"+q, nil, nil)
		var out []string
		for _, x := range b["jobs"].([]any) {
			out = append(out, x.(map[string]any)["title"].(string))
		}
		return out
	}
	for q, want := range map[string]string{"source=discord": "đơn 123 đâu", "source=web": "hỏi về đơn", "source=auto": "báo cáo sáng", "q=b%C3%A1o": "báo cáo sáng"} {
		if got := titles(q); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want [%s]", q, got, want)
		}
	}
}

// A chat opens on its last messages; older ones come a page at a time.
func TestConversationMessagesPaged(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, CreatedBy: "human:admin@x.io"})
	var ids []string
	for i := range 5 {
		m, _ := e.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Content: string(rune('a' + i))})
		ids = append(ids, m.ID)
	}
	page := func(q string) ([]string, bool) {
		_, b := do(t, admin, "GET", e.srv.URL+"/api/conversations/"+c.ID+q, nil, nil)
		var got []string
		for _, x := range b["messages"].([]any) {
			got = append(got, x.(map[string]any)["content"].(string))
		}
		more, _ := b["has_more"].(bool)
		return got, more
	}
	if got, more := page("?limit=2"); len(got) != 2 || got[0] != "d" || got[1] != "e" || !more {
		t.Fatalf("last page = %v more=%v", got, more)
	}
	if got, more := page("?limit=2&before=" + ids[3]); len(got) != 2 || got[0] != "b" || got[1] != "c" || !more {
		t.Fatalf("older page = %v more=%v", got, more)
	}
	if got, more := page("?limit=2&before=" + ids[1]); len(got) != 1 || got[0] != "a" || more {
		t.Fatalf("first page = %v more=%v", got, more)
	}
	if got, _ := page(""); len(got) != 5 { // without a limit: all, as before
		t.Fatalf("all = %v", got)
	}
}

// Jobs come a page at a time: a small page says there is more.
func TestJobsSmallPage(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	for range 3 {
		e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", Status: "done", Title: "x"})
	}
	_, b := do(t, admin, "GET", e.srv.URL+"/api/jobs?limit=2", nil, nil)
	if len(b["jobs"].([]any)) != 2 || b["next_before"] == "" {
		t.Fatalf("page = %d next %q", len(b["jobs"].([]any)), b["next_before"])
	}
}

// A chat of the skill editor is a chat of the project too: in the list (so it
// can be followed), and its job opens the editor again, not a lost thread.
func TestSkillChatListed(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "skill tra đơn", CreatedBy: "human:admin@x.io", Purpose: "skill"})
	e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "builder", CreatedBy: "human:admin@x.io", Purpose: "automation"})
	for _, q := range []string{"", "?source=all", "?source=web"} {
		_, b := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/conversations"+q, nil, nil)
		var found map[string]any
		for _, x := range b["conversations"].([]any) {
			if m := x.(map[string]any); m["id"] == c.ID {
				found = m
			}
			if x.(map[string]any)["title"] == "builder" {
				t.Fatalf("%q: an automation builder's chat stays with its automation", q)
			}
		}
		if found == nil || found["purpose"] != "skill" {
			t.Fatalf("%q: skill chat = %v in %v", q, found, b)
		}
	}
	e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", ConversationID: c.ID, Status: "done"})
	_, b := do(t, admin, "GET", e.srv.URL+"/api/jobs/groups", nil, nil)
	g := b["groups"].([]any)[0].(map[string]any)
	if g["link"] != "/projects/"+pid+"/skills/edit?c="+c.ID {
		t.Fatalf("link = %v", g["link"])
	}
}

// A template is written with the office assistant: its chat is listed and its
// job opens the template editor again; the draft is checked before saving.
func TestTemplateChat(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	aid, _ := assistant.Ensure(ctx, e.st, orgmodel.NewService(e.st), t.TempDir())
	resp, b := do(t, admin, "POST", e.srv.URL+"/api/projects/"+aid+"/conversations", map[string]any{"purpose": "template"}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, b)
	}
	cid := b["conversation"].(map[string]any)["id"].(string)
	_, b = do(t, admin, "GET", e.srv.URL+"/api/projects/"+aid+"/conversations", nil, nil)
	if cs := b["conversations"].([]any); len(cs) != 1 || cs[0].(map[string]any)["purpose"] != "template" {
		t.Fatalf("list = %v", b)
	}
	e.st.Jobs().Create(ctx, storage.Job{ProjectID: aid, Kind: "chat_turn", Origin: "user", CreatedBy: "human:admin@x.io", ConversationID: cid, Status: "done"})
	_, b = do(t, admin, "GET", e.srv.URL+"/api/jobs/groups", nil, nil)
	if g := b["groups"].([]any)[0].(map[string]any); g["link"] != "/templates/new?c="+cid {
		t.Fatalf("link = %v", g["link"])
	}
	good := map[string]any{"key": "review", "name": "Review", "kind": "solo", "agents": []any{
		map[string]any{"key": "lead", "name": "Lead", "tier": "lead", "model_tier": "strong"}}}
	_, b = do(t, admin, "POST", e.srv.URL+"/api/templates/validate", map[string]any{"template": good}, nil)
	if ps, _ := b["problems"].([]any); len(ps) != 0 {
		t.Fatalf("good template: %v", b)
	}
	_, b = do(t, admin, "POST", e.srv.URL+"/api/templates/validate", map[string]any{"template": map[string]any{"key": "X", "kind": "solo"}}, nil)
	if ps, _ := b["problems"].([]any); len(ps) < 2 {
		t.Fatalf("bad template: %v", b)
	}
}

// A Discord chat links to where it is there (its thread, or its first message).
func TestChannelChatLink(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "d", CreatedBy: "discord:binh", Purpose: "channel"})
	e.st.Settings().Set(ctx, "conv_link/"+c.ID, "https://discord.com/channels/g/123")
	_, b := do(t, admin, "GET", e.srv.URL+"/api/conversations/"+c.ID, nil, nil)
	if got := b["conversation"].(map[string]any)["external_url"]; got != "https://discord.com/channels/g/123" {
		t.Fatalf("external_url = %v", got)
	}
}
