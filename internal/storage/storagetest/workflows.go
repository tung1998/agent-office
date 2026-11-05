package storagetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

func testWorkflows(t *testing.T, s storage.Store) {
	ctx := context.Background()
	p, err := s.Repos().Create(ctx, storage.Repo{Name: "p", Path: "/tmp/wf-p"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Workflows().Create(ctx, storage.Workflow{ProjectID: p.ID, Key: "council", Name: "Hội đồng", Source: "---\n---\nx", Enabled: true,
		Bindings: map[string]string{"a": "agt_1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Workflows().Create(ctx, storage.Workflow{ProjectID: p.ID, Key: "council", Name: "x", Source: "x"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("duplicate key err = %v", err)
	}
	w.Enabled, w.Bindings = false, nil
	if err := s.Workflows().Update(ctx, w); err != nil {
		t.Fatal(err)
	}
	got, err := s.Workflows().GetByKey(ctx, p.ID, "council")
	if err != nil || got.Enabled || got.Bindings == nil || len(got.Bindings) != 0 {
		t.Fatalf("GetByKey = %+v, %v", got, err)
	}
	if list, err := s.Workflows().List(ctx, p.ID); err != nil || len(list) != 1 {
		t.Fatalf("List = %d, %v", len(list), err)
	}

	r, err := s.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: p.ID, ConversationID: "cnv_1", WorkflowKey: "council", WorkflowName: "Hội đồng",
		Status: storage.RunRunning, Roles: []storage.RunRole{{Role: "a", AgentID: "agt_1"}}})
	if err != nil {
		t.Fatal(err)
	}
	r.Turns, r.Log = 2, []storage.RunLog{{At: time.Now().UTC(), Text: "giao a"}}
	r.Roles[0].SessionID = "ses"
	if err := s.WorkflowRuns().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	back, err := s.WorkflowRuns().Get(ctx, r.ID)
	if err != nil || back.Turns != 2 || len(back.Log) != 1 || back.Roles[0].SessionID != "ses" || back.Gates == nil {
		t.Fatalf("Get = %+v, %v", back, err)
	}
	// a graph of steps keeps where it is (ADR-256): read back whole
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	r.Checkpoint = &storage.RunCheckpoint{Input: map[string]string{"text": "x"}, Steps: map[string]storage.StepState{"a": {Output: `{"k":1}`, Status: "0", JSON: map[string]any{"k": 1.0}}},
		Visits: map[string]int{"a": 1, "w": 1}, Pending: []string{"b"}, Running: map[string]string{"w": "wait"},
		Marks: map[string]storage.StepMark{"w": {WaitUntil: &until}, "c": {ActionID: "act_1", ChildRunID: "wfr_2"}}, N: 2, Last: "a", Resumes: 1,
		Tier: "fast", Tree: "burn-1", Trusted: true, DepthLeft: 2, CallerJob: "job_1"}
	if err := s.WorkflowRuns().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	back, err = s.WorkflowRuns().Get(ctx, r.ID)
	if err != nil || back.Checkpoint == nil {
		t.Fatalf("checkpoint = %+v, %v", back.Checkpoint, err)
	}
	if !reflect.DeepEqual(back.Checkpoint, r.Checkpoint) {
		t.Fatalf("checkpoint = %+v, want %+v", back.Checkpoint, r.Checkpoint)
	}
	if c := back.Checkpoint.Clone(); c == back.Checkpoint || !reflect.DeepEqual(c, back.Checkpoint) {
		t.Fatalf("Clone = %+v", c)
	}
	if n, err := s.WorkflowRuns().FailRunning(ctx, "restart", time.Now()); err != nil || n != 1 {
		t.Fatalf("FailRunning = %d, %v", n, err)
	}
	list, err := s.WorkflowRuns().List(ctx, "", "cnv_1", 10)
	if err != nil || len(list) != 1 || list[0].Status != storage.RunFailed || list[0].FinishedAt == nil {
		t.Fatalf("List runs = %+v, %v", list, err)
	}
	if err := s.Workflows().Delete(ctx, w.ID); err != nil {
		t.Fatal(err)
	}

	testListByWorkflow(t, s, p.ID)
}

// testListByWorkflow checks that ListByWorkflow finds a workflow's runs even
// when they are pushed past the default limit by another workflow's runs.
func testListByWorkflow(t *testing.T, s storage.Store, projectID string) {
	ctx := context.Background()
	older := time.Now().UTC().Add(-24 * time.Hour)
	for i := 0; i < 3; i++ {
		if _, err := s.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: projectID, ConversationID: "cnv_b", WorkflowID: "wf_b", WorkflowKey: "b",
			Status: storage.RunDone, StartedAt: older.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 510; i++ {
		if _, err := s.WorkflowRuns().Create(ctx, storage.WorkflowRun{ProjectID: projectID, ConversationID: "cnv_a", WorkflowID: "wf_a", WorkflowKey: "a",
			Status: storage.RunDone}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.WorkflowRuns().ListByWorkflow(ctx, projectID, "", "wf_b", 100)
	if err != nil || len(runs) != 3 {
		t.Fatalf("ListByWorkflow(wf_b) = %d runs, %v", len(runs), err)
	}
	for _, r := range runs {
		if r.WorkflowID != "wf_b" {
			t.Fatalf("ListByWorkflow(wf_b) returned run of workflow %q", r.WorkflowID)
		}
	}
}
