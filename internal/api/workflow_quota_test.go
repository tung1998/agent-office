package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A run shows how long its steps waited for quota (time not counted as its
// own), and Chạy ngay on a run with no step waiting for quota says so
// instead of doing nothing (ADR-270).
func TestSkipStepQuotaWait(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	c := e.client(t)
	login(t, e, c, "admin@x.io", "admin-password")
	p, _ := e.st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	a, _ := e.st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "lead", Name: "Lead", ModelTier: "fast"})
	src := "---\nkey: buoc\nname: Bước\nsteps:\n  - { id: a, type: code, lang: bash, script: echo a, next: c }\n  - { id: c, type: end, summary: x }\n---\n"
	w, err := e.st.Workflows().Create(ctx, storage.Workflow{ProjectID: p.ID, Key: "buoc", Name: "Bước", Source: src, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	own, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Purpose: "workflow_run"})
	r, err := e.st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: own.ID, WorkflowID: w.ID,
		WorkflowKey: "buoc", WorkflowName: "Bước", BodyHash: workflow.RunHash(src), CoordinatorID: a.ID, Status: storage.RunDone,
		Checkpoint: &storage.RunCheckpoint{Ended: true, Paused: 90 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	_, body := do(t, c, "GET", e.srv.URL+"/api/workflow-runs/"+r.ID, nil, nil)
	cp, _ := body["run"].(map[string]any)["checkpoint"].(map[string]any)
	if cp["paused_s"] != float64(90) {
		t.Fatalf("checkpoint = %v", cp)
	}
	if resp, body := do(t, c, "POST", e.srv.URL+"/api/workflow-runs/"+r.ID+"/quota-skip", nil, nil); resp.StatusCode != 409 {
		t.Fatalf("quota-skip of a run waiting for nothing = %d %v", resp.StatusCode, body)
	}
	if resp, _ := do(t, c, "POST", e.srv.URL+"/api/workflow-runs/run_nope/quota-skip", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("quota-skip of no run = %d", resp.StatusCode)
	}
}
