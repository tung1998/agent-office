package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/auth"
	"github.com/tung1998/agent-office/internal/storage"
)

// ADR-277: a chat handed over becomes the teammate's (unread on their bell),
// with a note in it; only someone who sees the project can take it, and only
// its owner or a manager hands it over.
func TestHandoffConversation(t *testing.T) {
	t.Parallel()
	e := setup(t)
	ctx := context.Background()
	p, _ := e.st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	other, _ := e.st.Repos().Create(ctx, storage.Repo{Name: "q", Path: t.TempDir()})
	if _, err := e.auth.CreateUser(ctx, auth.NewUser{Email: "outsider@x.io", Role: storage.RoleMember, Password: "outsider-password"}, "system"); err != nil {
		t.Fatal(err)
	}
	out, _ := e.st.Users().GetByEmail(ctx, "outsider@x.io")
	_ = e.st.Users().AddProject(ctx, out.ID, other.ID)

	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password") // sees p and q

	conv, err := e.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p.ID, Title: "on-call", CreatedBy: "human:admin@x.io"})
	if err != nil {
		t.Fatal(err)
	}
	url := e.srv.URL + "/api/conversations/" + conv.ID + "/handoff"

	resp, body := do(t, admin, "GET", url, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("candidates = %d %v", resp.StatusCode, body)
	}
	people, _ := body["people"].([]any)
	if len(people) != 1 || people[0].(map[string]any)["email"] != "member@x.io" {
		t.Fatalf("only the member sees p: %v", body)
	}
	// the member is neither owner nor manager
	if resp, _ := do(t, member, "POST", url, map[string]string{"to": "member@x.io"}, nil); resp.StatusCode != 403 {
		t.Fatalf("member hands over a chat not theirs = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", url, map[string]string{"to": "outsider@x.io"}, nil); resp.StatusCode != 400 {
		t.Fatalf("hand over to someone not seeing the project = %d", resp.StatusCode)
	}
	resp, body = do(t, admin, "POST", url, map[string]string{"to": "member@x.io", "note": "deploy đang dở, xem diff cuối"}, nil)
	if resp.StatusCode != 200 || body["owner"] != "member@x.io" {
		t.Fatalf("handoff = %d %v", resp.StatusCode, body)
	}
	got, _ := e.st.Chat().GetConversation(ctx, conv.ID)
	if got.CreatedBy != "human:member@x.io" || got.Title != "on-call" {
		t.Fatalf("owner not moved or row overwritten: %+v", got)
	}
	msgs, _ := e.st.Chat().ListMessages(ctx, conv.ID)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "admin@x.io → member@x.io") || !strings.Contains(msgs[0].Content, "deploy đang dở") {
		t.Fatalf("handoff note = %+v", msgs)
	}
	// now theirs: the member may hand it on
	if resp, _ := do(t, member, "GET", url, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("new owner reads candidates = %d", resp.StatusCode)
	}
}
