package api_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

// "Luôn cho phép": the card shows the pattern and its pack; a risky command
// has none, and approving it "always" is refused (it stays pending).
func TestApproveAlwaysAPI(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	sw, _ := e.st.Actions().Create(ctx, storage.Action{ProjectID: pid, Kind: "run_command", Target: "git switch main", ProposedBy: "Lead", Status: "pending"})
	rm, _ := e.st.Actions().Create(ctx, storage.Action{ProjectID: pid, Kind: "run_command", Target: "rm -rf dist", ProposedBy: "Lead", Status: "pending"})

	if _, b := do(t, admin, "GET", e.srv.URL+"/api/actions/"+sw.ID+"/always", nil, nil); b["pattern"] != "git switch *" || b["pack"] != "git (đã duyệt)" || b["new_pack"] != true {
		t.Fatalf("preview = %v", b)
	}
	if _, b := do(t, admin, "GET", e.srv.URL+"/api/actions/"+rm.ID+"/always", nil, nil); b["pattern"] != "" {
		t.Fatalf("risky preview = %v", b)
	}
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/actions/"+rm.ID+"/approve", map[string]any{"always": true}, nil); resp.StatusCode != 400 {
		t.Fatalf("risky always = %d %v", resp.StatusCode, b)
	}
	if a, _ := e.st.Actions().Get(ctx, rm.ID); a.Status != "pending" {
		t.Fatalf("risky ran: %+v", a)
	}
	// "Bỏ qua": rejected (its agent is not run again about it)
	if resp, b := do(t, admin, "POST", e.srv.URL+"/api/actions/"+rm.ID+"/reject", map[string]any{"skip": true}, nil); resp.StatusCode != 200 {
		t.Fatalf("skip = %d %v", resp.StatusCode, b)
	}
	if a, _ := e.st.Actions().Get(ctx, rm.ID); a.Status != "rejected" {
		t.Fatalf("skipped: %+v", a)
	}
}
