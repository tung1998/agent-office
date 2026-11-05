package officetools

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-188: only an administrator's run sees and decides the waiting cards.
func TestAdminRequests(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p1, _ := st.Repos().Create(ctx, storage.Repo{Name: "p1", Path: t.TempDir()})
	p2, _ := st.Repos().Create(ctx, storage.Repo{Name: "p2", Path: t.TempDir()})
	acts := actions.New(st, nil)
	mine, err := st.Actions().Create(ctx, storage.Action{ProjectID: p1.ID, Kind: "git_commit", Target: "mine", Status: "pending", ProposedBy: "a"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := st.Actions().Create(ctx, storage.Action{ProjectID: p2.ID, Kind: "git_commit", Target: "other", Status: "pending", ProposedBy: "b"})
	tb := New(st, nil, acts)
	var decided []string
	tb.SetDecide(func(_ context.Context, _ Scope, kind, id string, approve bool) (string, error) {
		decided = append(decided, kind+":"+id)
		return "ok", nil
	})
	names := func(sc Scope) (out []string) {
		for _, x := range tb.ToolsFor(sc) {
			out = append(out, x.Name)
		}
		return out
	}
	plain := Scope{ProjectID: p1.ID, Agent: "a", Level: perm.Operate, Access: perm.Admin(perm.Access{})}
	if n := names(plain); slices.Contains(n, "pending_requests") || slices.Contains(n, "decide_request") {
		t.Fatal("a run that is not administrator sees the admin tools")
	}
	if _, isErr := tb.Call(ctx, plain, "decide_request", []byte(`{"kind":"action","id":"`+other.ID+`","approve":true}`)); !isErr || len(decided) > 0 {
		t.Fatal("a run that is not administrator decided a card")
	}
	admin := plain
	admin.Admin = true
	if n := names(admin); !slices.Contains(n, "pending_requests") || !slices.Contains(n, "decide_request") {
		t.Fatalf("administrator tools missing: %v", n)
	}
	out, isErr := tb.Call(ctx, admin, "pending_requests", []byte(`{}`))
	if isErr || !strings.Contains(out, mine.ID) || strings.Contains(out, other.ID) {
		t.Fatalf("pending of its project only: %v %s", isErr, out)
	}
	if _, isErr := tb.Call(ctx, admin, "decide_request", []byte(`{"kind":"action","id":"`+mine.ID+`","approve":true}`)); isErr || len(decided) != 1 {
		t.Fatalf("administrator could not decide: %v", decided)
	}
}

// A decide_request without approve (or with a wrong type) is refused with a
// message, never read as a rejection of the card.
func TestDecideRequestNeedsApprove(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	tb := New(st, nil, actions.New(st, nil))
	var decided []string
	tb.SetDecide(func(_ context.Context, _ Scope, kind, id string, approve bool) (string, error) {
		decided = append(decided, kind+":"+id)
		return "ok", nil
	})
	admin := Scope{ProjectID: p.ID, Agent: "a", Level: perm.Operate, Access: perm.Admin(perm.Access{}), Admin: true}
	for _, raw := range []string{
		`{"kind":"action","id":"act_1"}`,
		`{"kind":"action","id":"act_1","approve":"yes"}`,
		`{"kind":"action","approve":true}`,
		`{"id":"act_1","approve":true}`,
	} {
		out, isErr := tb.Call(ctx, admin, "decide_request", []byte(raw))
		if !isErr || len(decided) > 0 {
			t.Fatalf("%s: decided %v (%q)", raw, decided, out)
		}
	}
	if _, isErr := tb.Call(ctx, admin, "decide_request", []byte(`{"kind":"action","id":"act_1","approve":false}`)); isErr || len(decided) != 1 {
		t.Fatalf("an explicit rejection is still taken: %v", decided)
	}
}
