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

type fakeUpdater struct{ by []string }

func (f *fakeUpdater) UpdateWhenIdle(_ context.Context, by, why string) error {
	f.by = append(f.by, by+"|"+why)
	return nil
}

// request_update (ADR-255): offered where office's own code is worked on; a
// plain run makes a card, an administrator's run starts the update at once;
// an office that cannot update itself refuses it.
func TestRequestUpdate(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	root := t.TempDir()
	self, _ := st.Repos().Create(ctx, storage.Repo{Name: "agent-office", Path: root})
	other, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	acts := actions.New(st, nil)
	up := &fakeUpdater{}
	tools := UpdateTools{Store: st, Actions: acts, Root: root + "/"}
	has := func(sc Scope) bool {
		return slices.ContainsFunc(tools.Tools(sc), func(x Tool) bool { return x.Name == "request_update" })
	}
	c1, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: self.ID, Title: "1"})
	c2, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: self.ID, Title: "2"})
	plain := Scope{ProjectID: self.ID, ConversationID: c1.ID, Agent: "Dev", Level: perm.Propose}
	if !has(plain) {
		t.Fatal("not offered in office's own project")
	}
	if has(Scope{ProjectID: other.ID, Agent: "Dev", Level: perm.Propose}) {
		t.Fatal("offered in another project")
	}
	if has(Scope{ProjectID: self.ID, Consult: true}) {
		t.Fatal("offered to a consult")
	}
	if out, isErr := tools.Call(ctx, plain, "request_update", []byte(`{"reason":"x"}`)); !isErr || !strings.Contains(out, "không tự cập nhật") {
		t.Fatalf("no updater = %v %s", isErr, out)
	}
	acts.SetUpdater(up)
	if out, isErr := tools.Call(ctx, Scope{ProjectID: self.ID, Agent: "Dev", Level: perm.Read}, "request_update", []byte(`{"reason":"x"}`)); !isErr {
		t.Fatalf("a read-only run proposed: %s", out)
	}
	out, isErr := tools.Call(ctx, plain, "request_update", []byte(`{"reason":"ADR-255 đã gộp"}`))
	if isErr || !strings.Contains(out, "approves") || len(up.by) != 0 {
		t.Fatalf("plain run = %v %s %v", isErr, out, up.by)
	}
	list, _ := st.Actions().List(ctx, c1.ID, "", "")
	if len(list) != 1 || list[0].Kind != "office_update" || list[0].Status != "pending" {
		t.Fatalf("cards = %+v", list)
	}
	if _, err := acts.Decide(ctx, list[0].ID, true, "admin@x.io"); err != nil {
		t.Fatal(err)
	}
	if len(up.by) != 1 || !strings.HasPrefix(up.by[0], "admin@x.io|ADR-255") {
		t.Fatalf("approved: %v", up.by)
	}
	admin := Scope{ProjectID: self.ID, ConversationID: c2.ID, Agent: "Dev", Level: perm.Operate, Admin: true, AdminBy: "admin@x.io"}
	if out, isErr := tools.Call(ctx, admin, "request_update", []byte(`{"reason":"y"}`)); isErr || !strings.Contains(out, "restarts on it once no work runs") || len(up.by) != 2 {
		t.Fatalf("administrator run = %v %s %v", isErr, out, up.by)
	}
}
