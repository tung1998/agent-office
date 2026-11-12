package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/storage"
)

// GET /api/jobs/stats must not count or sum another person's private
// assistant-chat jobs (ADR-046), matching what the job groups endpoint
// already hides.
func TestJobStatsHidesOthersAssistantChatJobs(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	id, err := assistant.Ensure(ctx, e.st, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	mine, err := e.st.Jobs().Create(ctx, storage.Job{ProjectID: id, Kind: "chat_turn", Origin: "user", Status: "running", CreatedBy: "human:admin@x.io", StartedAt: &now})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := e.st.Jobs().Create(ctx, storage.Job{ProjectID: id, Kind: "chat_turn", Origin: "user", Status: "running", CreatedBy: "human:member@x.io", StartedAt: &now})
	if err != nil {
		t.Fatal(err)
	}
	cost := 5.0
	e.st.Runs().Create(ctx, storage.Run{Kind: "chat_turn", ProjectID: id, JobID: theirs.ID, Status: "ok", CostUSD: &cost})
	e.st.Jobs().Finish(ctx, theirs.ID, "done", "", "", now)

	_, body := do(t, admin, "GET", e.srv.URL+"/api/jobs/stats", nil, nil)
	totals := body["totals"].(map[string]any)
	if totals["running"].(float64) != 1 {
		t.Fatalf("running = %v, want 1 (admin's own job only, not member's %q)", totals["running"], mine.ID)
	}
	if totals["cost_24h"].(float64) != 0 {
		t.Fatalf("cost_24h = %v, want 0 (member's assistant-chat cost must not leak)", totals["cost_24h"])
	}
	for _, row := range body["rows"].([]any) {
		r := row.(map[string]any)
		if r["jobs"].(float64) > 1 {
			t.Fatalf("rows leaked member's job: %v", body["rows"])
		}
	}
}
