package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-045: an agent proposes a settings change with propose_change; a person
// approves it on a card; it goes through the same handler as the dashboard.
func TestConfigChangeProposeApprove(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "other"}, nil)
	other := body["project"].(map[string]any)["id"].(string)
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{"name": "Hook", "source": "webhook", "action": "chat", "prompt": "x"}, nil)
	aid := body["automation"].(map[string]any)["id"].(string)

	acts := e.acts // wired to the API's registry by api.New
	sc := actions.Scope{ProjectID: pid, Agent: "Lead", Level: perm.Read}
	propose := func(resource, op, id string, patch map[string]any) (storage.Action, error) {
		raw, _ := json.Marshal(patch)
		return acts.Propose(ctx, sc, "config_change", "", "đổi tên", storage.ActionArgs{Change: &storage.ConfigChange{Resource: resource, Op: op, ID: id, Patch: raw}})
	}

	a, err := propose("automation", "update", aid, map[string]any{"name": "Hook mới"})
	if err != nil || a.Status != "pending" || a.Args.Change.Before == nil {
		t.Fatalf("propose = %+v %v", a, err)
	}
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/actions/"+a.ID+"/approve", map[string]any{}, nil); resp.StatusCode != 200 {
		t.Fatalf("approve = %d %v", resp.StatusCode, b)
	}
	if got, _ := e.st.Automations().Get(ctx, aid); got.Name != "Hook mới" {
		t.Fatalf("name = %q", got.Name)
	}
	rows, _ := e.st.Audit().List(ctx, storage.AuditFilter{Resource: "automation", ResourceID: aid})
	if len(rows) == 0 || rows[0].Action != "automation.update" || rows[0].ActorName != "Lead" || rows[0].ApprovedBy != "admin@x.io" {
		t.Fatalf("audit = %+v", rows[:1])
	}

	// stale: changed since the proposal
	a, _ = propose("automation", "update", aid, map[string]any{"name": "A"})
	do(t, admin, "PATCH", e.srv.URL+"/api/automations/"+aid, map[string]any{"name": "B", "source": "webhook", "action": "chat", "prompt": "x", "enabled": true}, nil)
	_, b := do(t, admin, "POST", e.srv.URL+"/api/actions/"+a.ID+"/approve", map[string]any{}, nil)
	if got, _ := e.st.Automations().Get(ctx, aid); got.Name != "B" {
		t.Fatalf("a stale change was applied: %q (%v)", got.Name, b)
	}

	// not stale: only runtime fields (last run, failures) moved since the proposal
	a, _ = propose("automation", "update", aid, map[string]any{"name": "C"})
	cur, _ := e.st.Automations().Get(ctx, aid)
	cur.Failures = 2
	now := time.Now().UTC()
	cur.LastRunAt = &now
	_ = e.st.Automations().Update(ctx, cur)
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/actions/"+a.ID+"/approve", map[string]any{}, nil); resp.StatusCode != 200 {
		t.Fatalf("approve after a run = %d %v", resp.StatusCode, b)
	}
	if got, _ := e.st.Automations().Get(ctx, aid); got.Name != "C" {
		t.Fatalf("a run made the proposal stale: %q", got.Name)
	}

	for name, bad := range map[string]func() error{
		"unknown field": func() error { _, err := propose("automation", "update", aid, map[string]any{"bogus": 1}); return err },
		"unknown kind":  func() error { _, err := propose("nope", "update", aid, map[string]any{"name": "x"}); return err },
		"other project": func() error {
			sc.ProjectID = other
			defer func() { sc.ProjectID = pid }()
			_, err := propose("automation", "update", aid, map[string]any{"name": "x"})
			return err
		},
		"office setting from a project": func() error {
			_, err := propose("provider", "create", "", map[string]any{"name": "p", "kind": "anthropic"})
			return err
		},
		"key from the AI": func() error {
			_, err := propose("provider", "create", "", map[string]any{"name": "p", "kind": "anthropic", "api_key": "sk-1"})
			return err
		},
	} {
		if bad() == nil {
			t.Errorf("%s accepted", name)
		}
	}

	// a provider: the person pastes the key on the card, the AI never sees it
	sc.Office = true // the office assistant (or the CLI) proposes office settings
	a, err = propose("provider", "create", "", map[string]any{"name": "Claude API", "kind": "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/actions/"+a.ID+"/approve", map[string]any{"api_key": "sk-ant-card-key-0001"}, nil); resp.StatusCode != 200 {
		t.Fatalf("approve provider = %d %v", resp.StatusCode, b)
	}
	ps, _ := e.st.Providers().List(ctx)
	found := false
	for _, p := range ps {
		if p.Name == "Claude API" && len(p.APIKeyEnc) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("provider with key not created: %+v", ps)
	}
}

// A settings change approved from a bot's chat (by someone the bot lets
// approve, with no office account) is carried out on the office's behalf; the
// log names who approved it in the chat.
func TestConfigChangeApprovedFromChat(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/automations", map[string]any{"name": "Hook", "source": "webhook", "action": "chat", "prompt": "x"}, nil)
	aid := body["automation"].(map[string]any)["id"].(string)
	raw, _ := json.Marshal(map[string]any{"name": "Hook từ Discord"})
	a, err := e.acts.Propose(ctx, actions.Scope{ProjectID: pid, Agent: "Lead", Level: perm.Read}, "config_change", "", "đổi tên",
		storage.ActionArgs{Change: &storage.ConfigChange{Resource: "automation", Op: "update", ID: aid, Patch: raw}})
	if err != nil {
		t.Fatal(err)
	}
	done, err := e.acts.Decide(ctx, a.ID, true, "discord:tung")
	if err != nil || done.Status != "done" {
		t.Fatalf("decide = %+v %v", done, err)
	}
	if got, _ := e.st.Automations().Get(ctx, aid); got.Name != "Hook từ Discord" {
		t.Fatalf("name = %q", got.Name)
	}
	if _, err := e.acts.Decide(ctx, mustPropose(t, e, pid, aid), true, "nobody@x.io"); err == nil {
		// an unknown office account is still refused
		if got, _ := e.st.Automations().Get(ctx, aid); got.Name == "X" {
			t.Fatal("an unknown account applied a change")
		}
	}
	// a bot set up to approve directly approves in its own name
	if done, err := e.acts.Decide(ctx, mustPropose(t, e, pid, aid), true, "bot:Dev"); err != nil || done.Status != "done" {
		t.Fatalf("decide by the bot = %+v %v", done, err)
	}
}

func mustPropose(t *testing.T, e *env, pid, aid string) string {
	raw, _ := json.Marshal(map[string]any{"name": "X"})
	a, err := e.acts.Propose(context.Background(), actions.Scope{ProjectID: pid, Agent: "Lead", Level: perm.Read}, "config_change", "", "đổi",
		storage.ActionArgs{Change: &storage.ConfigChange{Resource: "automation", Op: "update", ID: aid, Patch: raw}})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// ADR-188: an administrator's run changes settings at once in the name of
// the admin it acts for, so it can make another agent administrator too;
// without that admin the change is refused.
func TestAdminRunGrantsAdministrator(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/agents", map[string]any{"key": "reviewer", "name": "Reviewer", "model_tier": "fast"}, nil)
	rid := body["agent"].(map[string]any)["id"].(string)

	grant := func(by string) (storage.Action, error) {
		sc := actions.Scope{ProjectID: pid, RunRef: "r-" + by, Agent: "Lead", Level: perm.Operate, Access: perm.Admin(perm.Access{}), Admin: true, AdminBy: by}
		raw, _ := json.Marshal(map[string]any{"permissions": map[string]any{"level": "operate", "full_access": true}})
		return e.acts.Propose(ctx, sc, "config_change", "", "cấp admin", storage.ActionArgs{Change: &storage.ConfigChange{Resource: "agent", Op: "update", ID: rid, Patch: raw}})
	}
	if a, err := grant("nobody@x.io"); err == nil && a.Status == "done" {
		t.Fatal("granted in the name of someone who is not an admin")
	}
	a, err := grant("admin@x.io")
	if err != nil || a.Status != "done" {
		t.Fatalf("grant = %+v %v", a, err)
	}
	got, _ := e.st.Agents().Get(ctx, rid)
	if !got.Permissions.FullAccess || got.Permissions.FullAccessBy != "admin@x.io" {
		t.Fatalf("permissions = %+v", got.Permissions)
	}
}

// ADR-214: an agent an administrator's run makes a lead (Vận hành with
// self.manage) runs as administrator too, in the same admin's name; one
// only given more work does not.
func TestAdminRunLeadIsAdministrator(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	mk := func(key string) string {
		_, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/agents", map[string]any{"key": key, "name": key, "model_tier": "fast"}, nil)
		return body["agent"].(map[string]any)["id"].(string)
	}
	lead, dev := mk("lead"), mk("dev")
	set := func(id string, caps []string) storage.Agent {
		sc := actions.Scope{ProjectID: pid, RunRef: "r-" + id, Agent: "CEO", Level: perm.Operate, Access: perm.Admin(perm.Access{}), Admin: true, AdminBy: "admin@x.io"}
		raw, _ := json.Marshal(map[string]any{"permissions": map[string]any{"level": "operate", "caps": caps}})
		if a, err := e.acts.Propose(ctx, sc, "config_change", "", "đổi vai", storage.ActionArgs{Change: &storage.ConfigChange{Resource: "agent", Op: "update", ID: id, Patch: raw}}); err != nil || a.Status != "done" {
			t.Fatalf("update = %+v %v", a, err)
		}
		got, _ := e.st.Agents().Get(ctx, id)
		return got
	}
	if got := set(lead, []string{"propose", "code.apply", "commands.run", "ops.process", "self.manage"}); !got.Permissions.FullAccess || got.Permissions.FullAccessBy != "admin@x.io" {
		t.Fatalf("lead = %+v", got.Permissions)
	}
	if got := set(dev, []string{"propose", "code.apply", "commands.run", "ops.process"}); got.Permissions.FullAccess {
		t.Fatalf("not a lead, yet administrator: %+v", got.Permissions)
	}
}

// A chat agent reads the agent library through list/get, to start a new
// agent from it instead of writing one from scratch.
func TestAgentLibraryReadable(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	sc := officetools.Scope{ProjectID: body["project"].(map[string]any)["id"].(string), Level: perm.Operate}
	out, isErr := e.office.Call(ctx, sc, "list", json.RawMessage(`{"resource":"agent_library"}`))
	var list []map[string]any
	if isErr || json.Unmarshal([]byte(out), &list) != nil || len(list) == 0 {
		t.Fatalf("list = %.200s %v", out, isErr)
	}
	packFound := false
	for _, entry := range list {
		if entry["id"] == "office/teacher" {
			packFound = entry["requires_pack"] == "@agentoffice/languages"
		}
	}
	if !packFound {
		t.Fatal("agent library list omits teacher's pack requirement")
	}
	id, _ := list[0]["id"].(string)
	if out, isErr := e.office.Call(ctx, sc, "get", json.RawMessage(`{"resource":"agent_library","id":"`+id+`"}`)); isErr || !strings.Contains(out, "instructions") {
		t.Fatalf("get %s = %.200s %v", id, out, isErr)
	}
	if out, _ := e.office.Call(ctx, sc, "describe", json.RawMessage(`{"resource":"agent"}`)); !strings.Contains(out, "agent_library") {
		t.Fatalf("describe agent = %s", out)
	}
}

// ADR-196: an agent steers the work board's Burn through propose_change
// resource burn: its focus, template and run on / off / finish.
func TestConfigChangeBurn(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	sc := actions.Scope{ProjectID: pid, Agent: "CEO", Level: perm.Read}
	propose := func(patch map[string]any) (storage.Action, error) {
		raw, _ := json.Marshal(patch)
		return e.acts.Propose(ctx, sc, "config_change", "", "theo mục tiêu quý", storage.ActionArgs{Change: &storage.ConfigChange{Resource: "burn", Op: "update", Patch: raw}})
	}
	a, err := propose(map[string]any{"focus": "Checkout mobile", "template": "ux", "run": "finish"})
	if err != nil || a.Status != "pending" {
		t.Fatalf("propose = %+v %v", a, err)
	}
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/actions/"+a.ID+"/approve", map[string]any{}, nil); resp.StatusCode != 200 {
		t.Fatalf("approve = %d %v", resp.StatusCode, b)
	}
	b, err := e.st.Burn().Session(ctx, pid)
	if err != nil || b.Focus != "Checkout mobile" || b.Template != "ux" || b.Active() {
		t.Fatalf("burn = %+v %v", b, err)
	}
	if _, err := propose(map[string]any{"bogus": 1}); err == nil {
		t.Fatal("an unknown field was accepted")
	}
	a, _ = propose(map[string]any{"run": "fast"})
	do(t, admin, "POST", e.srv.URL+"/api/actions/"+a.ID+"/approve", map[string]any{}, nil)
	if got, _ := e.st.Actions().Get(ctx, a.ID); got.Status == "done" {
		t.Fatal("run must be on, off or finish")
	}
}
