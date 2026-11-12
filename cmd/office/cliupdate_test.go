package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	loaded.Bad = map[string]string{"gemini": "0.67.0"}
	loaded.Repair = map[string]bool{"claude": true}
	h.Save(ctx, loaded)
	got := h.Load(ctx)
	// what was put back and what was reinstalled are kept as what was told
	if got.Mode != clitools.AutoUpdate || got.Told["codex"] != "0.167.0" || got.Bad["gemini"] != "0.67.0" || !got.Repair["claude"] {
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
	if !strings.Contains(cu.CLIStatus(ctx), "Chưa có kết nối AI") {
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

// A connection's command is the updated install only when it resolves to it:
// another command name on PATH (claude-old) or a relative path is not.
func TestRunsInstallResolvesTheCommand(t *testing.T) {
	dir := t.TempDir()
	script := []byte("#!/bin/sh\necho 1.0.0\n")
	for _, n := range []string{"claude", "claude-old"} {
		if err := os.WriteFile(filepath.Join(dir, n), script, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "claude"), filepath.Join(dir, "claude-link")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir) // the PATH a connection's bare command is started with
	cu := cliUpdates{m: clitools.NewManagerWithPath(dir)}
	shared := samePath(cu.m.LookPath("claude"))
	cases := map[string]bool{
		"claude":                         true, // found on PATH: the install
		"claude-link":                    true, // a link to it
		filepath.Join(dir, "claude"):     true,
		"claude-old":                     false, // another command
		filepath.Join(dir, "claude-old"): false,
		"./tools/claude":                 false, // relative: cannot be told
		filepath.Join("tools", "claude"): false,
		"claude-missing":                 false,
		"":                               false,
	}
	for bin, want := range cases {
		if got := cu.runsInstall(bin, shared); got != want {
			t.Errorf("runsInstall(%q) = %v, want %v", bin, got, want)
		}
	}
}

// tell says whether the text went: none picked or a bot failing is not sent.
func TestTellSaysWhetherItWent(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_ = st.Migrate(ctx)
	fail := true
	cu := cliUpdates{store: st, notify: func(context.Context, string, string, string) error {
		if fail {
			return errors.New("bot offline")
		}
		return nil
	}}
	if cu.tell(ctx, "x") {
		t.Fatal("sent with no alert chat")
	}
	_ = limitalert.Save(ctx, st, limitalert.Settings{ChannelID: "chn_1", ChatID: "42"})
	if cu.tell(ctx, "x") {
		t.Fatal("a failed send counted as sent")
	}
	fail = false
	if !cu.tell(ctx, "x") {
		t.Fatal("a send that went counted as not sent")
	}
}

// office's own PATH (where a connection's bare command is started) and the
// CLI manager's (where the updated install is found) may hold two binaries of
// the same name: a bare "claude" connection runs office's, so its broken
// binary never puts the manager's install back; a connection with no command
// runs the manager's and is checked.
func TestRecheckResolvesLikeTheRun(t *testing.T) {
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
	managerDir, processDir := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, d := range []string{managerDir, processDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	healthy := "#!/bin/sh\ncase \"$1 $2\" in\n  \"--version \"*) echo 2.1.319;;\n  \"auth status\") echo '{\"loggedIn\":true}';;\nesac\n"
	broken := "#!/bin/sh\necho 'Error: spawn /x/vendor/claude ENOENT' >&2\nexit 1\n"
	_ = os.WriteFile(filepath.Join(managerDir, "claude"), []byte(healthy), 0o755)
	_ = os.WriteFile(filepath.Join(processDir, "claude"), []byte(broken), 0o755)
	t.Setenv("PATH", processDir+":/usr/bin:/bin")
	m := clitools.NewManagerWithPath(managerDir)
	provs.SetBinResolver(m.LookPath)
	cu := cliUpdates{m: m, store: st, providers: provs}
	shared := samePath(m.LookPath("claude"))
	if cu.runsInstall("claude", shared) {
		t.Fatal("a bare command found on office's PATH was taken for the manager's install")
	}
	bare, _ := st.Providers().Create(ctx, storage.Provider{Name: "bare", Kind: storage.ProviderClaudeCLI, BaseURL: "claude", Enabled: true})
	if why := cu.recheck(ctx, "claude"); why != "" {
		t.Fatalf("another binary of the same name made the update broken: %q", why)
	}
	if p, _ := st.Providers().Get(ctx, bare.ID); p.Status == "ok" || p.Status == "error" {
		t.Fatalf("the bare connection was tested: %s", p.Status)
	}
	own, _ := st.Providers().Create(ctx, storage.Provider{Name: "default", Kind: storage.ProviderClaudeCLI, Enabled: true})
	if why := cu.recheck(ctx, "claude"); why != "" {
		t.Fatalf("healthy install reported broken: %q", why)
	}
	if p, _ := st.Providers().Get(ctx, own.ID); p.Status != "ok" {
		t.Fatalf("the connection on the manager's install was not checked: %s", p.Status)
	}
}

// keepBad and hooks().Save share one settings row (AutoUpdateKey) across CLI
// tools: a concurrent call for "codex" must not stomp what another call just
// wrote for "gemini" (or vice versa).
func TestCLIUpdatesAutoSettingConcurrentMerge(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		editAutoSetting(ctx, st, func(cur *clitools.AutoSetting) bool {
			if cur.Told == nil {
				cur.Told = map[string]string{}
			}
			cur.Told["codex"] = "1.0.0"
			return true
		})
	}()
	go func() {
		defer wg.Done()
		editAutoSetting(ctx, st, func(cur *clitools.AutoSetting) bool {
			if cur.Told == nil {
				cur.Told = map[string]string{}
			}
			cur.Told["gemini"] = "2.0.0"
			return true
		})
	}()
	wg.Wait()
	var got clitools.AutoSetting
	if _, err := st.Settings().Get(ctx, clitools.AutoUpdateKey, &got); err != nil {
		t.Fatal(err)
	}
	if got.Told["codex"] != "1.0.0" || got.Told["gemini"] != "2.0.0" {
		t.Fatalf("lost a concurrent tool's Told entry: %+v", got.Told)
	}
}

// keepBad (an upgrade ending) and hooks().Save (the watch loop's tick) write
// the same AutoUpdateKey row; Save must not overwrite a Bad entry keepBad
// just wrote with the stale snapshot it loaded before the tick started.
func TestCLIUpdatesKeepBadSurvivesHooksSave(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings().Set(ctx, clitools.AutoUpdateKey, clitools.AutoSetting{
		Bad: map[string]string{"codex": "1.0"},
	}); err != nil {
		t.Fatal(err)
	}
	cu := cliUpdates{m: clitools.NewManagerWithPath(t.TempDir()), store: st}

	// keepBad loads the manager's view of put-back versions, which seeds
	// from the store on its first call ("codex": "1.0").
	cu.keepBad(ctx)

	// The watch tick started before keepBad ran, so its snapshot (s) knows
	// nothing about "codex" and carries a different tool's Told/Repair.
	cu.hooks().Save(ctx, clitools.AutoSetting{
		Told:   map[string]string{"gemini": "2.0"},
		Repair: map[string]bool{"gemini": true},
	})

	var got clitools.AutoSetting
	if _, err := st.Settings().Get(ctx, clitools.AutoUpdateKey, &got); err != nil {
		t.Fatal(err)
	}
	if got.Bad["codex"] != "1.0" {
		t.Fatalf("hooks().Save dropped keepBad's entry: Bad = %+v", got.Bad)
	}
	if got.Told["gemini"] != "2.0" || !got.Repair["gemini"] {
		t.Fatalf("hooks().Save lost its own Told/Repair: %+v", got)
	}
}
