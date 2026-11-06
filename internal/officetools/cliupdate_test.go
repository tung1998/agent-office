package officetools

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

type fakeCLI struct{ updated []string }

func (f *fakeCLI) KnownCLI(tool string) error {
	if tool != "codex" {
		return errors.New("chưa cài " + tool)
	}
	return nil
}
func (f *fakeCLI) UpdateCLIWhenIdle(_ context.Context, tool, by string) error {
	f.updated = append(f.updated, tool+"|"+by)
	return nil
}

// update_cli (ADR-259): a card a person approves; the update starts then.
func TestUpdateCLITool(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	root := t.TempDir()
	self, _ := st.Repos().Create(ctx, storage.Repo{Name: "agent-office", Path: root})
	other, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	acts := actions.New(st, nil)
	tools := CLIUpdateTools{Store: st, Actions: acts, Root: root}
	c, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: self.ID, Title: "1"})
	sc := Scope{ProjectID: self.ID, ConversationID: c.ID, Agent: "Dev", Level: perm.Propose}
	if !slices.ContainsFunc(tools.Tools(sc), func(x Tool) bool { return x.Name == "update_cli" }) {
		t.Fatal("not offered in office's own project")
	}
	if len(tools.Tools(Scope{ProjectID: other.ID, Level: perm.Propose})) != 0 {
		t.Fatal("offered in another project")
	}
	if out, isErr := tools.Call(ctx, sc, "update_cli", []byte(`{"tool":"codex","reason":"x"}`)); !isErr || !strings.Contains(out, "không cập nhật CLI") {
		t.Fatalf("no updater = %v %s", isErr, out)
	}
	cli := &fakeCLI{}
	acts.SetCLIUpdater(cli)
	if out, isErr := tools.Call(ctx, sc, "update_cli", []byte(`{"tool":"gemini","reason":"x"}`)); !isErr || !strings.Contains(out, "chưa cài") {
		t.Fatalf("unknown cli = %v %s", isErr, out)
	}
	out, isErr := tools.Call(ctx, sc, "update_cli", []byte(`{"tool":"codex","reason":"spawn ENOENT"}`))
	if isErr || !strings.Contains(out, "approves") || len(cli.updated) != 0 {
		t.Fatalf("propose = %v %s %v", isErr, out, cli.updated)
	}
	list, _ := st.Actions().List(ctx, c.ID, "", "")
	if len(list) != 1 || list[0].Kind != "cli_update" || list[0].Target != "codex" {
		t.Fatalf("cards = %+v", list)
	}
	if _, err := acts.Decide(ctx, list[0].ID, true, "an@x.io"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cli.updated, []string{"codex|an@x.io"}) {
		t.Fatalf("updated = %v", cli.updated)
	}
}
