package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// burn: getWorkflowRun listed a conversation's runs with a LIMIT before
// filtering by parent_run_id; a busy conversation with more runs than the
// limit could silently drop real children. The parent here is the oldest
// run in the conversation, so a recency-capped scan pushes its child out.
func TestWorkflowRunChildrenNotLostToListLimit(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	c := e.client(t)
	login(t, e, c, "admin@x.io", "admin-password")
	p, _ := e.st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	conv, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Purpose: "workflow_run"})

	parent, err := e.st.WorkflowRuns().Create(ctx, storage.WorkflowRun{
		ProjectID: p.ID, ConversationID: conv.ID, WorkflowID: "wf_parent",
		Status: storage.RunRunning, StartedAt: time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := e.st.WorkflowRuns().Create(ctx, storage.WorkflowRun{
		ProjectID: p.ID, ConversationID: conv.ID, WorkflowID: "wf_child", ParentRunID: parent.ID,
		Status: storage.RunRunning, StartedAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Flood the conversation with more recent, unrelated runs so the child
	// (older than all of them) would be pushed past a LIMIT-100 recency scan.
	for i := 0; i < 100; i++ {
		if _, err := e.st.WorkflowRuns().Create(ctx, storage.WorkflowRun{
			ProjectID: p.ID, ConversationID: conv.ID, WorkflowID: "wf_noise",
			Status: storage.RunRunning, StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	_, body := do(t, c, "GET", e.srv.URL+"/api/workflow-runs/"+parent.ID, nil, nil)
	children, _ := body["children"].([]any)
	if len(children) != 1 || children[0].(map[string]any)["id"] != child.ID {
		t.Fatalf("children = %v, want [%s]", children, child.ID)
	}
}
