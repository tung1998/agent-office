package officetools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// ADR-277: work_in_progress shows the chats' unmerged edits and the open
// quests with who follows them; another office may call it read-only.
func TestWorkInProgress(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p, _ := st.Repos().Create(ctx, storage.Repo{Name: "hq", Path: t.TempDir()})
	b, _ := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID, ModelTier: storage.TierBalanced, MaxParallel: 1, State: "stopped"})
	st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "sửa thanh toán", Kind: "quest", Status: "doing", Owner: "cto", Files: []string{"pay.go"}})
	st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "xong rồi", Kind: "quest", Status: "done"})

	tb := New(st, nil, nil)
	var asked [2]string
	tb.SetInProgress(func(_ context.Context, projectID, conv string) string {
		asked = [2]string{projectID, conv}
		return `- "chat A" (/projects/x?tab=chat&c=c1) by a@b.c: pay.go`
	})
	out, err := tb.workInProgress(ctx, Scope{ProjectID: p.ID, ConversationID: "c9"})
	if err != nil {
		t.Fatal(err)
	}
	if asked != [2]string{p.ID, "c9"} {
		t.Fatalf("chats asked for %v", asked)
	}
	for _, want := range []string{`"chat A"`, "[doing] sửa thanh toán — follows: cto — files: pay.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "xong rồi") {
		t.Fatalf("a done quest is listed:\n%s", out)
	}
	found := false
	for _, x := range tb.PeerTools(PeerScope(p.ID, "other", "")) {
		found = found || x.Name == "work_in_progress"
	}
	if !found {
		t.Fatal("another office cannot read work_in_progress")
	}
}
