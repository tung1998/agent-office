package api_test

import (
	"context"
	"testing"

	"github.com/tung1998/agent-office/internal/deciding"
	"github.com/tung1998/agent-office/internal/storage"
)

// A card being decided (office approving it on its own, or someone
// elsewhere) is not in "Cần xử lý": nobody is asked to approve it again.
// Approving it meanwhile answers 409 saying who is deciding it.
func TestDecidingNotAsked(t *testing.T) {
	t.Parallel()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": t.TempDir(), "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	a, _ := e.st.Actions().Create(ctx, storage.Action{ProjectID: pid, Kind: "run_command", Target: "go test ./...", ProposedBy: "Lead", Status: "pending"})

	asked := func() bool {
		_, b := do(t, admin, "GET", e.srv.URL+"/api/incidents", nil, nil)
		list, _ := b["incidents"].([]any)
		for _, x := range list {
			if x.(map[string]any)["key"] == "approval:"+a.ID {
				return true
			}
		}
		return false
	}
	if !asked() {
		t.Fatal("a pending card is not asked")
	}
	if !deciding.Begin("actions", a.ID, "auto:Lead (Vận hành)") {
		t.Fatal("Begin refused")
	}
	if asked() {
		t.Fatal("a card being decided is still asked")
	}
	resp, b := do(t, admin, "POST", e.srv.URL+"/api/actions/"+a.ID+"/approve", map[string]any{}, nil)
	if resp.StatusCode != 409 || b["action"].(map[string]any)["deciding"] != "auto:Lead (Vận hành)" {
		t.Fatalf("approve while deciding = %d %v", resp.StatusCode, b)
	}
	deciding.End("actions", a.ID)
	if !asked() {
		t.Fatal("the card is not asked again once its decision ended undecided")
	}
}
