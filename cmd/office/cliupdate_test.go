package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/limitalert"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
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

// After an update the connections on the CLI are checked for real: a
// connection marked broken goes back to ok in the store; a CLI that does not
// start is reported (the update puts the old version back), a signed-out
// account is not.
func TestCLIRecheckClearsBrokenMark(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlite.Open(filepath.Join(dir, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.Load(filepath.Join(dir, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	bin := filepath.Join(dir, "claude")
	write := func(script string) {
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, err := st.Providers().Create(ctx, storage.Provider{Name: "Claude Code", Kind: storage.ProviderClaudeCLI, BaseURL: bin, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Providers().SetStatus(ctx, p.ID, "error", "claude: CLI không chạy được: spawn ENOENT", nil, time.Now())
	cu := cliUpdates{m: clitools.NewManagerWithPath(dir), store: st, providers: provs}

	write("#!/bin/sh\ncase \"$1 $2\" in\n  \"--version \"*) echo 2.1.319;;\n  \"auth status\") echo '{\"loggedIn\":true,\"email\":\"a@b.c\"}';;\nesac\n")
	if why := cu.recheck(ctx, "claude"); why != "" {
		t.Fatalf("healthy = %q", why)
	}
	if got, _ := st.Providers().Get(ctx, p.ID); got.Status != "ok" {
		t.Fatalf("broken mark stayed: %s %s", got.Status, got.StatusDetail)
	}

	write("#!/bin/sh\necho 'Error: spawn /x/vendor/claude ENOENT' >&2\nexit 1\n")
	if why := cu.recheck(ctx, "claude"); why == "" {
		t.Fatal("a CLI that does not start was not reported")
	}

	write("#!/bin/sh\ncase \"$1 $2\" in\n  \"--version \"*) echo 2.1.319;;\n  \"auth status\") echo '{\"loggedIn\":false}'; exit 1;;\nesac\n")
	if why := cu.recheck(ctx, "claude"); why != "" {
		t.Fatalf("a signed-out account was taken for a broken install: %q", why)
	}
}

// A connection pointed at a binary of its own is not the install that was
// updated: its own broken binary neither marks the update broken nor is
// tested for it.
func TestCLIRecheckOnlyTheUpdatedInstall(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := sqlite.Open(filepath.Join(dir, "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.Load(filepath.Join(dir, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	shared, own := filepath.Join(dir, "bin"), filepath.Join(dir, "own")
	for _, d := range []string{shared, own} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	healthy := "#!/bin/sh\ncase \"$1 $2\" in\n  \"--version \"*) echo 2.1.319;;\n  \"auth status\") echo '{\"loggedIn\":true}';;\nesac\n"
	broken := "#!/bin/sh\necho 'Error: spawn /x/vendor/claude ENOENT' >&2\nexit 1\n"
	_ = os.WriteFile(filepath.Join(shared, "claude"), []byte(healthy), 0o755)
	_ = os.WriteFile(filepath.Join(own, "claude"), []byte(broken), 0o755)
	m := clitools.NewManagerWithPath(shared)
	provs.SetBinResolver(m.LookPath)
	onShared, _ := st.Providers().Create(ctx, storage.Provider{Name: "shared", Kind: storage.ProviderClaudeCLI, Enabled: true})
	onOwn, _ := st.Providers().Create(ctx, storage.Provider{Name: "own", Kind: storage.ProviderClaudeCLI, BaseURL: filepath.Join(own, "claude"), Enabled: true})
	cu := cliUpdates{m: m, store: st, providers: provs}
	if why := cu.recheck(ctx, "claude"); why != "" {
		t.Fatalf("a binary of its own made the update broken: %q", why)
	}
	if p, _ := st.Providers().Get(ctx, onShared.ID); p.Status != "ok" {
		t.Fatalf("shared connection = %s", p.Status)
	}
	if p, _ := st.Providers().Get(ctx, onOwn.ID); p.Status == "ok" || p.Status == "error" {
		t.Fatalf("a connection on another binary was tested: %s", p.Status)
	}
	_ = os.WriteFile(filepath.Join(shared, "claude"), []byte(broken), 0o755)
	if why := cu.recheck(ctx, "claude"); why == "" {
		t.Fatal("the updated install broken was not reported")
	}
}
