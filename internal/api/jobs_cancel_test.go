package api_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/storage"
)

// A running chat_turn job whose Turn is no longer in memory (already
// finished, or lost on a restart) must still end up cancelled in storage,
// not reported as cancelled while staying "running" forever.
func TestCancelJobRunningWithoutTurn(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "x"})
	now := time.Now().UTC()
	j, err := e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", ConversationID: c.ID, Title: "x", Status: "running", StartedAt: &now})
	if err != nil {
		t.Fatal(err)
	}

	httpResp, resp := do(t, admin, "POST", e.srv.URL+"/api/jobs/"+j.ID+"/cancel", nil, nil)
	if httpResp.StatusCode != 200 {
		t.Fatalf("status = %d, body = %v", httpResp.StatusCode, resp)
	}
	got := resp["job"].(map[string]any)
	if got["status"] != "cancelled" {
		t.Fatalf("response job status = %v, want cancelled", got["status"])
	}
	saved, err := e.st.Jobs().Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "cancelled" {
		t.Fatalf("stored job status = %q, want cancelled (previously stayed \"running\" while the API reported success)", saved.Status)
	}
}

// Cancelling a job that already finished must not look like a success.
func TestCancelJobAlreadyDone(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	c, _ := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: pid, Title: "x"})
	now := time.Now().UTC()
	j, _ := e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", ConversationID: c.ID, Title: "x", Status: "running", StartedAt: &now})
	if _, err := e.st.Jobs().Finish(ctx, j.ID, "done", "", "", now); err != nil {
		t.Fatal(err)
	}

	httpResp, _ := do(t, admin, "POST", e.srv.URL+"/api/jobs/"+j.ID+"/cancel", nil, nil)
	if httpResp.StatusCode != 409 {
		t.Fatalf("status = %d, want 409", httpResp.StatusCode)
	}
	saved, err := e.st.Jobs().Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "done" {
		t.Fatalf("stored job status = %q, want untouched done", saved.Status)
	}
}

// FinishIfRunning is the CAS primitive cancelJob relies on: it must refuse
// (ErrConflict) once the job has moved past "running", so a cancel racing
// the job's own completion cannot overwrite the real outcome.
func TestFinishIfRunningConflict(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	now := time.Now().UTC()
	j, _ := e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", Title: "x", Status: "running", StartedAt: &now})
	if _, err := e.st.Jobs().Finish(ctx, j.ID, "done", "", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Jobs().FinishIfRunning(ctx, j.ID, "cancelled", "cancelled", "", now); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	saved, err := e.st.Jobs().Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "done" {
		t.Fatalf("stored job status = %q, want untouched done", saved.Status)
	}
}

// FinishIfPending is the CAS primitive cancelJob's pending branch relies on:
// once Claim has moved a job to "running", FinishIfPending must refuse
// (ErrConflict) instead of overwriting it with "cancelled" while the run
// keeps executing for real.
func TestFinishIfPendingConflict(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	j, _ := e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", Title: "x", Status: "pending"})

	if _, err := e.st.Jobs().Claim(ctx, time.Now().UTC(), 10, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Jobs().FinishIfPending(ctx, j.ID, "cancelled", "cancelled", "", time.Now().UTC()); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	saved, err := e.st.Jobs().Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "running" {
		t.Fatalf("stored job status = %q, want untouched running", saved.Status)
	}

	j2, _ := e.st.Jobs().Create(ctx, storage.Job{ProjectID: pid, Kind: "chat_turn", Origin: "user", Title: "y", Status: "pending"})
	if _, err := e.st.Jobs().FinishIfPending(ctx, j2.ID, "cancelled", "cancelled", "", time.Now().UTC()); err != nil {
		t.Fatalf("FinishIfPending on a still-pending job: %v", err)
	}
}
