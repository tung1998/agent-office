package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/limitalert"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// The watch saves what it told without undoing a mode a person changed
// meanwhile; how an update ended goes to the alert chat, a cancel does not.
func TestCLIUpdatesHooks(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var sent []string
	cu := cliUpdates{m: clitools.NewManagerWithPath(t.TempDir()), store: st,
		notify: func(_ context.Context, channelID, chatID, text string) error {
			sent = append(sent, channelID+"/"+chatID+": "+text)
			return nil
		}}
	h := cu.hooks()
	loaded := h.Load(ctx)
	_ = st.Settings().Set(ctx, clitools.AutoUpdateKey, clitools.AutoSetting{Mode: clitools.AutoUpdate})
	loaded.Told = map[string]string{"codex": "0.167.0"}
	h.Save(ctx, loaded)
	got := h.Load(ctx)
	if got.Mode != clitools.AutoUpdate || got.Told["codex"] != "0.167.0" {
		t.Fatalf("saved = %+v", got)
	}

	cu.ended(clitools.UpgradeView{Tool: "codex", State: "done", From: "0.166.0", To: "0.167.0"})
	if len(sent) != 0 {
		t.Fatalf("told with no alert chat: %v", sent)
	}
	_ = limitalert.Save(ctx, st, limitalert.Settings{ChannelID: "chn_1", ChatID: "42"})
	cu.ended(clitools.UpgradeView{Tool: "codex", State: "cancelled"})
	cu.ended(clitools.UpgradeView{Tool: "codex", State: "rolled_back", From: "0.166.0", Note: "spawn ENOENT"})
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "chn_1/42: ⚠️") || !strings.Contains(sent[0], "quay về bản 0.166.0") {
		t.Fatalf("sent = %v", sent)
	}
	if err := cu.KnownCLI("nope"); err == nil || cu.KnownCLI("codex") == nil {
		t.Fatal("known cli")
	}
	if !strings.Contains(cu.CLIStatus(ctx), "chưa cài") {
		t.Fatalf("status = %q", cu.CLIStatus(ctx))
	}
}
