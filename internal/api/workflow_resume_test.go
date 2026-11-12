package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

// A run the restart ended shows where it was and that it may go on;
// "Chạy tiếp từ bước dở" goes on with it and it ends done; a run that
// cannot go on is refused (ADR-256).
func TestWorkflowRunResume(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	c := e.client(t)
	login(t, e, c, "admin@x.io", "admin-password")
	p, _ := e.st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	a, err := e.st.Agents().Create(ctx, storage.Agent{ProjectID: p.ID, Key: "lead", Name: "Lead", ModelTier: "fast"})
	if err != nil {
		t.Fatal(err)
	}
	src := "---\nkey: buoc\nname: Bước\nsteps:\n  - { id: a, type: code, lang: bash, script: echo a, next: b }\n  - { id: b, type: code, lang: bash, script: echo b, next: c }\n  - { id: c, type: end, summary: \"{{steps.a.output}}{{steps.b.output}}\" }\n---\n"
	w, err := e.st.Workflows().Create(ctx, storage.Workflow{ProjectID: p.ID, Key: "buoc", Name: "Bước", Source: src, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	own, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Purpose: "workflow_run"})
	caller, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID})
	mk := func(errText string) storage.WorkflowRun {
		r, err := e.st.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: own.ID, CallerConversationID: caller.ID, WorkflowID: w.ID,
			WorkflowKey: "buoc", WorkflowName: "Bước", BodyHash: workflow.RunHash(src), CoordinatorID: a.ID, Status: storage.RunFailed, Error: errText,
			Checkpoint: &storage.RunCheckpoint{Steps: map[string]storage.StepState{"a": {Output: "A"}}, Running: map[string]string{"b": "code"}, Visits: map[string]int{"a": 1, "b": 1}, N: 2}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	other := mk("bước b: lỗi")
	r := mk(workflow.InterruptedDetail + ": cập nhật")

	_, body := do(t, c, "GET", e.srv.URL+"/api/workflow-runs/"+r.ID, nil, nil)
	run, _ := body["run"].(map[string]any)
	cp, _ := run["checkpoint"].(map[string]any)
	running, _ := cp["running"].([]any)
	if cp["resumable"] != true || len(running) != 1 || running[0].(map[string]any)["label"] == "" {
		t.Fatalf("checkpoint = %v", cp)
	}
	if resp, body := do(t, c, "POST", e.srv.URL+"/api/workflow-runs/"+other.ID+"/resume", nil, nil); resp.StatusCode != 400 {
		t.Fatalf("resume a run that failed on its own = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, c, "POST", e.srv.URL+"/api/workflow-runs/"+r.ID+"/resume", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("resume = %d %v", resp.StatusCode, body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, _ := e.st.WorkflowRuns().Get(ctx, r.ID)
		if got.Status == storage.RunDone && got.Result == "Ab" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run = %s %q %q", got.Status, got.Result, got.Error)
		}
		time.Sleep(30 * time.Millisecond)
	}
}
