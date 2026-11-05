package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/storage"
)

func failJob(t *testing.T, e *env, j storage.Job, msg string) storage.Job {
	t.Helper()
	ctx := context.Background()
	if j.Kind == "" {
		j.Kind = "chat_turn"
	}
	if j.Origin == "" {
		j.Origin = "user"
	}
	j.Status = "running"
	j, err := e.st.Jobs().Create(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Jobs().Finish(ctx, j.ID, "failed", "agent_error", msg, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return j
}

// GET /api/jobs/failures groups failed jobs and failed model calls by cause
// (ADR-253), within the projects the person sees, filtered by agent and
// category; another person's assistant-chat failures stay hidden (ADR-046).
func TestJobFailuresAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "seen"}, nil)
	seen := body["project"].(map[string]any)
	seenID := seen["id"].(string)
	agentID := seen["agents"].([]any)[0].(map[string]any)["id"].(string)

	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password") // sees "seen" only
	_, body = do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "hidden"}, nil)
	hiddenID := body["project"].(map[string]any)["id"].(string)

	enoent := func(who string) string { return "codex: Error: spawn /Users/" + who + "/codex ENOENT" }
	j1 := failJob(t, e, storage.Job{ProjectID: seenID, AgentID: agentID}, enoent("a"))
	failJob(t, e, storage.Job{ProjectID: seenID, AgentID: agentID}, enoent("b"))
	failJob(t, e, storage.Job{ProjectID: seenID}, "claude: exit status 1")
	failJob(t, e, storage.Job{ProjectID: hiddenID}, enoent("x"))
	// a model call that failed with the job: counted once; one that failed
	// alone (a workflow role, a retried call): counted
	e.st.Runs().Create(ctx, storage.Run{Kind: "chat", ProjectID: seenID, AgentID: agentID, JobID: j1.ID, Status: "error", Error: enoent("a"), ProviderName: "openAI"})
	e.st.Runs().Create(ctx, storage.Run{Kind: "chat", ProjectID: seenID, AgentID: agentID, Status: "error", Error: "codex: You’ve hit your usage limit. try again at 9:18 PM.", ProviderName: "openAI"})
	e.st.Runs().Create(ctx, storage.Run{Kind: "provider_test", ProjectID: seenID, Status: "error", Error: "a test of the connection"})
	// done jobs are not failures
	ok, _ := e.st.Jobs().Create(ctx, storage.Job{ProjectID: seenID, Kind: "chat_turn", Origin: "user", Status: "running"})
	e.st.Jobs().Finish(ctx, ok.ID, "done", "", "", time.Now().UTC())

	groups := func(c interface{}, q string) ([]any, map[string]any) {
		t.Helper()
		resp, b := do(t, member, "GET", e.srv.URL+"/api/jobs/failures"+q, nil, nil)
		if c != nil {
			resp, b = do(t, admin, "GET", e.srv.URL+"/api/jobs/failures"+q, nil, nil)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("failures%s = %d %v", q, resp.StatusCode, b)
		}
		return b["groups"].([]any), b
	}

	gs, b := groups(nil, "")
	if len(gs) != 3 {
		t.Fatalf("member groups = %d %v", len(gs), gs)
	}
	top := gs[0].(map[string]any)
	if top["category"] != "cli_broken" || top["count"].(float64) != 2 || top["jobs"].(float64) != 2 {
		t.Fatalf("top group = %v", top)
	}
	if names := top["agent_names"].([]any); len(names) != 1 || names[0] == agentID {
		t.Fatalf("agent names = %v", names)
	}
	if ids := top["job_ids"].([]any); len(ids) != 2 {
		t.Fatalf("job ids = %v", ids)
	}
	cats := b["categories"].(map[string]any)
	if cats["cli_broken"].(float64) != 2 || cats["quota"].(float64) != 1 || cats["other"].(float64) != 1 || b["total"].(float64) != 4 {
		t.Fatalf("categories = %v total = %v", cats, b["total"])
	}

	if gs, _ := groups(admin, ""); gs[0].(map[string]any)["count"].(float64) != 3 {
		t.Fatalf("admin sees every project: %v", gs[0])
	}
	if gs, _ := groups(nil, "?category=quota"); len(gs) != 1 || gs[0].(map[string]any)["calls"].(float64) != 1 {
		t.Fatalf("category filter = %v", gs)
	}
	if gs, _ := groups(nil, "?agent="+agentID); len(gs) != 2 {
		t.Fatalf("agent filter = %v", gs)
	}
	if gs, _ := groups(nil, "?project="+hiddenID); len(gs) != 0 {
		t.Fatalf("a project the member does not see = %v", gs)
	}

	// another person's assistant chat
	own, err := assistant.Ensure(ctx, e.st, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	theirs := failJob(t, e, storage.Job{ProjectID: own, CreatedBy: "human:member@x.io"}, "their private fault")
	e.st.Runs().Create(ctx, storage.Run{Kind: "chat", ProjectID: own, JobID: theirs.ID, Status: "error", Error: "their private call"})
	failJob(t, e, storage.Job{ProjectID: own, CreatedBy: "human:admin@x.io"}, "my own fault")
	gs, _ = groups(admin, "?project="+own)
	if len(gs) != 1 || gs[0].(map[string]any)["signature"] != "my own fault" {
		t.Fatalf("assistant chat groups = %v", gs)
	}
}

// The "failed in 24h" figure counted with a capped list: it never passed 200.
func TestJobStatsCountsPast200(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "p"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	for range 205 {
		failJob(t, e, storage.Job{ProjectID: pid}, "boom")
	}
	_, body = do(t, admin, "GET", e.srv.URL+"/api/jobs/stats", nil, nil)
	if n := body["totals"].(map[string]any)["failed_24h"].(float64); n != 205 {
		t.Fatalf("failed_24h = %v, want 205", n)
	}
}
