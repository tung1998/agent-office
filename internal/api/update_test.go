package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/selfupdate"
	"github.com/tung1998/agent-office/internal/storage"
)

// The update page counts every kind of work a restart cuts off, not only the
// answers being written: a running workflow refuses an update now (409,
// naming it), an update when idle is accepted; the auto switch is kept; all
// of it is the admins' only (ADR-255).
func TestUpdateWaitsForWorkAndAutoSwitch(t *testing.T) {
	testUpdater = selfupdate.New(selfupdate.Source{Root: t.TempDir()}, t.TempDir(), nil, nil)
	defer func() { testUpdater = nil }()
	e := setup(t)
	ctx := context.Background()
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	for _, m := range []string{"GET", "POST", "DELETE"} {
		if resp, _ := do(t, member, m, e.srv.URL+"/api/system/update", map[string]any{}, nil); resp.StatusCode != 403 {
			t.Fatalf("member %s = %d", m, resp.StatusCode)
		}
	}
	if resp, _ := do(t, member, "PUT", e.srv.URL+"/api/system/update/auto", map[string]any{"enabled": true}, nil); resp.StatusCode != 403 {
		t.Fatalf("member auto = %d", resp.StatusCode)
	}

	c := e.client(t)
	login(t, e, c, "admin@x.io", "admin-password")
	resp, body := do(t, c, "GET", e.srv.URL+"/api/system/update", nil, nil)
	busy, _ := body["busy"].(map[string]any)
	if resp.StatusCode != 200 || busy == nil || body["auto"] == nil {
		t.Fatalf("status = %d %v", resp.StatusCode, body)
	}

	p, _ := e.st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	if _, err := e.st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: "cnv_w", WorkflowKey: "daily-upgrade", WorkflowName: "Nâng cấp hằng ngày", Status: storage.RunRunning}); err != nil {
		t.Fatal(err)
	}
	resp, body = do(t, c, "POST", e.srv.URL+"/api/system/update", map[string]any{}, nil)
	if resp.StatusCode != 409 || body["code"] != "busy" || !strings.Contains(body["error"].(string), "Nâng cấp hằng ngày") {
		t.Fatalf("update now with a workflow running = %d %v", resp.StatusCode, body)
	}
	if resp, _ = do(t, c, "DELETE", e.srv.URL+"/api/system/update", nil, nil); resp.StatusCode != 409 {
		t.Fatalf("cancel with nothing in progress = %d", resp.StatusCode)
	}
	if resp, body = do(t, c, "POST", e.srv.URL+"/api/system/update", map[string]any{"when_idle": true, "max_wait_minutes": 5000}, nil); resp.StatusCode != 400 {
		t.Fatalf("a wait past 24h = %d %v", resp.StatusCode, body)
	}
	if resp, body = do(t, c, "POST", e.srv.URL+"/api/system/update", map[string]any{"when_idle": true}, nil); resp.StatusCode != 202 {
		t.Fatalf("update when idle = %d %v", resp.StatusCode, body)
	}
	if st := testUpdater.State(); !st.WhenIdle || st.By != "admin@x.io" {
		t.Fatalf("state = %+v", st)
	}

	if resp, _ = do(t, c, "PUT", e.srv.URL+"/api/system/update/auto", map[string]any{"enabled": true}, nil); resp.StatusCode != 200 {
		t.Fatalf("auto = %d", resp.StatusCode)
	}
	_, body = do(t, c, "GET", e.srv.URL+"/api/system/update", nil, nil)
	if a, _ := body["auto"].(map[string]any); a["enabled"] != true {
		t.Fatalf("auto after save = %v", body["auto"])
	}
}
