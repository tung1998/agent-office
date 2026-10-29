package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func TestAutomationTemplateLibrary(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	member := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	login(t, e, member, "member@x.io", "member-password")

	preset := map[string]any{
		"name": "Daily project review", "description": "Review project status", "category": "Operations",
		"prompt": "Review {{payload}} carefully and return an evidence-based plan.", "source": "schedule",
	}
	if resp, body := do(t, member, "PUT", e.srv.URL+"/api/automation/templates/daily-project-review", preset, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member save = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, admin, "PUT", e.srv.URL+"/api/automation/templates/daily-project-review", preset, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("admin save = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, member, "GET", e.srv.URL+"/api/automation/templates", nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("member list = %d %v", resp.StatusCode, body)
	} else {
		items := body["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["key"] != "daily-project-review" {
			t.Fatalf("template list = %#v", items)
		}
	}
	invalid := map[string]any{"name": "Invalid", "description": "Invalid", "category": "Operations", "prompt": "Prompt", "source": "channel"}
	if resp, body := do(t, admin, "PUT", e.srv.URL+"/api/automation/templates/invalid", invalid, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid source = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, admin, "DELETE", e.srv.URL+"/api/automation/templates/daily-project-review", nil, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("admin delete = %d %v", resp.StatusCode, body)
	}
}

// ADR-074 security fix: only a scheduled automation may override to full
// (administrator) access — a webhook's caller is whoever has the URL.
func TestOverrideFullAccessRejectedUnlessScheduled(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "hook", "source": "webhook", "action": "chat", "prompt": "x",
		"permission_mode": "override", "override_full_access": true,
		"limits": map[string]any{"daily_cost_usd": 5, "disable_after_failures": 3},
	}, nil); resp.StatusCode != 400 {
		t.Fatalf("webhook override = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "sched", "source": "schedule", "action": "chat", "prompt": "x", "config": map[string]any{"every_minutes": 60},
		"permission_mode": "override", "override_full_access": true,
		"limits": map[string]any{"daily_cost_usd": 5, "disable_after_failures": 3},
	}, nil); resp.StatusCode != 201 {
		t.Fatalf("schedule override = %d %v", resp.StatusCode, body)
	}
}

// ADR-074 security fix: an override that turns full access OFF must never be
// forced to set a budget — it replaces the agent's own full access entirely
// (even when the underlying agent itself has FullAccess=true), so it never
// needs the cost-cap/auto-disable guardrail that only a full-access run needs.
func TestOverrideWithoutFullAccessNotRequiredToSetBudget(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	agents := body["project"].(map[string]any)["agents"].([]any)
	agentID := agents[0].(map[string]any)["id"].(string)

	// the agent itself has full access (set directly in storage, as if an
	// admin had turned it on earlier) — only the override's own fields decide
	// whether this automation gets it, never a fallback to the agent's own.
	ag, err := e.st.Agents().Get(context.Background(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	ag.Permissions.FullAccess, ag.Permissions.FullAccessBy = true, "admin@x.io"
	if err := e.st.Agents().Update(context.Background(), ag); err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "sched", "source": "schedule", "action": "chat", "prompt": "x", "config": map[string]any{"every_minutes": 60},
		"agent_id": agentID, "permission_mode": "override", "override_full_access": false,
		"limits": map[string]any{"daily_cost_usd": 0, "disable_after_failures": 0},
	}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("override off with no budget wrongly rejected: %d %v", resp.StatusCode, body)
	}
}

// ADR-074 security fix: an override's extra read dirs are checked the same
// way an agent's are — a credentials folder like ~/.ssh is always rejected.
func TestOverrideExtraDirsRejectsSensitiveDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ssh := home + "/.ssh"
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "sched", "source": "schedule", "action": "chat", "prompt": "x", "config": map[string]any{"every_minutes": 60},
		"permission_mode": "override", "override_extra_dirs": []string{ssh},
		"limits": map[string]any{"daily_cost_usd": 5, "disable_after_failures": 3},
	}, nil); resp.StatusCode != 400 {
		t.Fatalf("override extra dir = ~/.ssh: %d %v", resp.StatusCode, body)
	}
}

func TestAutomationsAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "Jira", "source": "webhook", "action": "chat", "prompt": "Bug mới: {{payload.issue.key}}",
	}, nil)
	secret, _ := body["secret"].(string)
	if resp.StatusCode != 201 || secret == "" {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	aid := body["automation"].(map[string]any)["id"].(string)
	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "x", "source": "schedule", "action": "chat", "config": map[string]any{"cron": "99 * * * *"},
	}, nil); resp.StatusCode != 400 {
		t.Fatalf("bad cron = %d %v", resp.StatusCode, body)
	}
	// ADR-219: a schedule's only_if is read back, so an edit sending the config back keeps it
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "Điều phối", "source": "schedule", "action": "chat", "prompt": "p",
		"config": map[string]any{"every_minutes": 5, "only_if": map[string]any{"board": "changed"}},
	}, nil)
	if cfg, _ := body["automation"].(map[string]any)["config"].(map[string]any); resp.StatusCode != 201 || cfg["only_if"].(map[string]any)["board"] != "changed" {
		t.Fatalf("only_if = %d %v", resp.StatusCode, body)
	}
	// an event automation reads its event back, so turning it off with the config it read works
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "Lặp", "source": "event", "action": "chat", "prompt": "p",
		"config": map[string]any{"event": "automation.done", "event_from": "self"},
	}, nil)
	ev, _ := body["automation"].(map[string]any)
	if cfg, _ := ev["config"].(map[string]any); resp.StatusCode != 201 || cfg["event"] != "automation.done" || cfg["event_from"] != "self" {
		t.Fatalf("event read back = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, admin, "PATCH", e.srv.URL+"/api/automations/"+ev["id"].(string), map[string]any{
		"name": "Lặp", "enabled": false, "source": "event", "action": "chat", "prompt": "p", "config": ev["config"],
	}, nil); resp.StatusCode != 200 {
		t.Fatalf("turn off event = %d %v", resp.StatusCode, body)
	}
	if _, body := do(t, admin, "GET", e.srv.URL+"/api/automations/preview-schedule?cron=0+8+*+*+1-5&tz=Asia/Ho_Chi_Minh", nil, nil); len(body["next"].([]any)) != 5 {
		t.Fatalf("preview = %v", body)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/projects/"+pid+"/automations", nil)
	res, _ := admin.Do(req)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.Contains(string(raw), "secret_hash") || !strings.Contains(string(raw), "/hooks/"+aid) {
		t.Fatalf("list = %s", raw)
	}

	// the webhook takes the secret, and the run shows up as a job
	hook, _ := http.NewRequest("POST", e.srv.URL+"/hooks/"+aid, strings.NewReader(`{"issue":{"key":"SP-1"}}`))
	hook.Header.Set("Authorization", "Bearer "+secret)
	hres, err := http.DefaultClient.Do(hook)
	if err != nil || hres.StatusCode != 202 {
		t.Fatalf("hook = %v %v", hres, err)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/jobs?project="+pid+"&origin=automation", nil, nil)
	jobs := body["jobs"].([]any)
	if len(jobs) != 1 || jobs[0].(map[string]any)["automation_name"] != "Jira" {
		t.Fatalf("jobs = %v", body)
	}
	if _, body := do(t, admin, "GET", e.srv.URL+"/api/jobs/stats?project="+pid+"&by=kind", nil, nil); body["totals"] == nil {
		t.Fatalf("stats = %v", body)
	}

	// members cannot create or run
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/automations/"+aid+"/run", map[string]any{}, nil); resp.StatusCode != 403 {
		t.Fatalf("member run = %d", resp.StatusCode)
	}
}

func TestScriptAutomationAPI(t *testing.T) { // ADR-041
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "x", "source": "schedule", "action": "script", "config": map[string]any{"every_minutes": 5}, "script": map[string]any{"lang": "ruby", "body": "x"},
	}, nil); resp.StatusCode != 400 {
		t.Fatalf("unknown language = %d %v", resp.StatusCode, body)
	}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "đếm", "source": "schedule", "action": "script", "config": map[string]any{"every_minutes": 60},
		"script": map[string]any{"lang": "bash", "body": "echo hi; exit 4"}, "escalate": map[string]any{"when": "never"},
	}, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	a := body["automation"].(map[string]any)
	if a["script"].(map[string]any)["body"] != "echo hi; exit 4" {
		t.Fatalf("automation = %v", a)
	}
	_, run := do(t, admin, "POST", e.srv.URL+"/api/automations/"+a["id"].(string)+"/run", map[string]any{}, nil)
	jid := run["job"].(map[string]any)["id"].(string)
	var job map[string]any
	for i := 0; i < 50; i++ {
		_, got := do(t, admin, "GET", e.srv.URL+"/api/jobs/"+jid, nil, nil)
		job = got["job"].(map[string]any)
		if job["status"] != "pending" && job["status"] != "running" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if job["kind"] != "script" || job["status"] != "failed" || job["exit_code"] != float64(4) || !strings.Contains(job["output"].(string), "hi") {
		t.Fatalf("job = %v", job)
	}
}

func TestAutomationPermissionAPI(t *testing.T) { // ADR-074
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	agents := body["project"].(map[string]any)["agents"].([]any)
	agentID := agents[0].(map[string]any)["id"].(string)

	base := map[string]any{"name": "a", "source": "schedule", "action": "chat", "agent_id": agentID, "prompt": "x",
		"config": map[string]any{"every_minutes": 60}}

	// full access needs no budget: limits are the admin's own choice
	in := map[string]any{}
	for k, v := range base {
		in[k] = v
	}
	in["permission_mode"], in["override_full_access"] = "override", true
	in["limits"] = map[string]any{"daily_cost_usd": 0, "disable_after_failures": 0}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", in, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("override without budget = %d %v", resp.StatusCode, body)
	}
	a := body["automation"].(map[string]any)
	if a["permission_mode"] != "override" || a["override_admin_by"] != "admin@x.io" {
		t.Fatalf("override fields = %v", a)
	}

	// the default "agent" mode needs no budget of its own
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", base, nil)
	if resp.StatusCode != 201 || body["automation"].(map[string]any)["permission_mode"] != "agent" {
		t.Fatalf("default agent mode = %d %v", resp.StatusCode, body)
	}

	// a member cannot override, even with a budget set
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", in, nil); resp.StatusCode != 403 {
		t.Fatalf("member override = %d", resp.StatusCode)
	}

	// once the agent itself has FullAccess, "agent" mode needs no budget either
	_, agentBody := do(t, admin, "GET", e.srv.URL+"/api/agents/"+agentID, nil, nil)
	ag := agentBody["agent"].(map[string]any)
	ag["permissions"] = map[string]any{"full_access": true}
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/agents/"+agentID, stripID(ag), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("set agent full access = %d %v", resp.StatusCode, body)
	}
	if body["agent"].(map[string]any)["permissions"].(map[string]any)["full_access_by"] != "admin@x.io" {
		t.Fatalf("full_access_by not stamped = %v", body["agent"])
	}
	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", base, nil); resp.StatusCode != 201 {
		t.Fatalf("agent full access without budget = %d %v", resp.StatusCode, body)
	}
}

func TestAutomationBuilderAPI(t *testing.T) { // ADR-042
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)

	// test a script without saving
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations/test-script", map[string]any{
		"script": map[string]any{"lang": "bash", "body": "read p; echo \"got $p\"; exit 2"}, "payload": "hi",
	}, nil)
	if resp.StatusCode != 200 || body["exit_code"] != float64(2) || !strings.Contains(body["output"].(string), "got hi") {
		t.Fatalf("test-script = %d %v", resp.StatusCode, body)
	}
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/projects/"+pid+"/automations/test-script", map[string]any{"script": map[string]any{"lang": "bash", "body": "id"}}, nil); resp.StatusCode != 403 {
		t.Fatalf("member test-script = %d", resp.StatusCode)
	}

	// a building chat, hidden from the project's chats, tied to the automation when it is saved
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/conversations", map[string]any{"purpose": "automation"}, nil)
	cid := body["conversation"].(map[string]any)["id"].(string)
	if _, body := do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/conversations", nil, nil); len(body["conversations"].([]any)) != 0 {
		t.Fatalf("building chat listed: %v", body)
	}
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{
		"name": "đếm", "source": "schedule", "action": "script", "config": map[string]any{"every_minutes": 60},
		"script": map[string]any{"lang": "bash", "body": "echo ok"}, "conversation_id": cid,
	}, nil)
	aid := body["automation"].(map[string]any)["id"].(string)
	resp, body = do(t, admin, "POST", e.srv.URL+"/api/automations/"+aid+"/conversation", map[string]any{}, nil)
	if resp.StatusCode != 200 || body["conversation"].(map[string]any)["id"] != cid {
		t.Fatalf("automation conversation = %d %v", resp.StatusCode, body)
	}
}

// The dashboard sends the automation back as it read it, override_admin_by
// included: saving must work (2026-10-01: every save was 400 "invalid JSON
// body"), and the field is never taken from the client.
func TestAutomationSaveIgnoresOverrideAdminBy(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	in := map[string]any{"name": "sched", "source": "schedule", "action": "chat", "prompt": "x", "config": map[string]any{"every_minutes": 20},
		"permission_mode": "agent", "override_full_access": false, "override_admin_by": "", "override_extra_dirs": []string{}}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", in, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("create = %d %v", resp.StatusCode, body)
	}
	id := body["automation"].(map[string]any)["id"].(string)
	in["permission_mode"], in["override_full_access"], in["override_admin_by"] = "override", true, "someone@else.io"
	resp, body = do(t, admin, "PATCH", e.srv.URL+"/api/automations/"+id, in, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("save = %d %v", resp.StatusCode, body)
	}
	if by := body["automation"].(map[string]any)["override_admin_by"]; by != "admin@x.io" {
		t.Fatalf("override_admin_by = %v, want the admin who saved", by)
	}
}

// failChannelUpdateStore makes Channels().Update fail, to simulate a DB
// error landing after the automation row has already been created.
type failChannelUpdateStore struct{ storage.Store }

func (s failChannelUpdateStore) Channels() storage.ChannelRepo {
	return failChannelUpdate{s.Store.Channels()}
}

type failChannelUpdate struct{ storage.ChannelRepo }

func (failChannelUpdate) Update(ctx context.Context, c storage.Channel) error {
	return fmt.Errorf("db unavailable")
}

// The library list pages with ?q=&offset=&limit=, the office's own skills first.
func TestLibrarySkillPages(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	for _, s := range []struct{ name, fm string }{
		{"alpha", "source: community"}, {"beta-react", "source: community"}, {"gamma-react", "source: community"}, {"own-react", ""},
	} {
		md := "---\nname: " + s.name + "\ndescription: " + s.name + " skill\n" + s.fm + "\n---\nDo it."
		if resp, body := do(t, admin, "PUT", e.srv.URL+"/api/automation/library/skill/"+s.name, map[string]any{"files": map[string]string{"SKILL.md": md}}, nil); resp.StatusCode >= 300 {
			t.Fatalf("save %s = %d %v", s.name, resp.StatusCode, body)
		}
	}
	names := func(body map[string]any) (out []string) {
		for _, it := range body["items"].([]any) {
			out = append(out, it.(map[string]any)["name"].(string))
		}
		return out
	}
	_, body := do(t, admin, "GET", e.srv.URL+"/api/automation/library/skill?q=REACT&limit=2", nil, nil)
	if got := names(body); body["total"] != float64(3) || len(got) != 2 || got[0] != "own-react" || got[1] != "beta-react" {
		t.Fatalf("page 1 = %v", body)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/automation/library/skill?q=react&limit=2&offset=2", nil, nil)
	if got := names(body); len(got) != 1 || got[0] != "gamma-react" {
		t.Fatalf("page 2 = %v", body)
	}
	if _, body = do(t, admin, "GET", e.srv.URL+"/api/automation/library/skill", nil, nil); len(names(body)) != 4 || body["total"] != nil {
		t.Fatalf("whole list = %v", body)
	}
	// tags (inline or a block list), category → work area, risk; counts per filter
	for name, fm := range map[string]string{
		"css-grid": "source: community\ncategory: frontend\nrisk: safe\ntags: [css, ui]",
		"pentest":  "source: community\ncategory: security\nrisk: offensive\ntags:\n- audit\n- ui",
	} {
		md := "---\nname: " + name + "\ndescription: d\n" + fm + "\n---\nDo it."
		if resp, body := do(t, admin, "PUT", e.srv.URL+"/api/automation/library/skill/"+name, map[string]any{"files": map[string]string{"SKILL.md": md}}, nil); resp.StatusCode >= 300 {
			t.Fatalf("save %s = %d %v", name, resp.StatusCode, body)
		}
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/automation/library/skill?tag=UI&limit=10", nil, nil)
	facets, _ := body["facets"].(map[string]any)
	if got := names(body); len(got) != 2 || facets["areas"].(map[string]any)["code"] != float64(1) || facets["areas"].(map[string]any)["ops"] != float64(1) {
		t.Fatalf("tag=ui = %v", body)
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/automation/library/skill?tag=ui&area=ops&limit=10", nil, nil)
	if got := names(body); len(got) != 1 || got[0] != "pentest" {
		t.Fatalf("tag=ui&area=ops = %v", body)
	}
	it := body["items"].([]any)[0].(map[string]any)
	if it["category"] != "security" || it["risk"] != "offensive" || len(it["tags"].([]any)) != 2 {
		t.Fatalf("item = %v", it)
	}
	// the area filter does not narrow its own counts
	if areas := body["facets"].(map[string]any)["areas"].(map[string]any); areas["code"] != float64(1) {
		t.Fatalf("areas = %v", areas)
	}
}
