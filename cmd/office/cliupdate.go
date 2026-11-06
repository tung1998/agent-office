package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tung1998/agent-office/internal/clitools"
	"github.com/tung1998/agent-office/internal/limitalert"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/selfupdate"
	"github.com/tung1998/agent-office/internal/storage"
)

// The AI CLIs kept up to date (ADR-259): updated once no run uses them, a
// broken version put back, new versions and broken installs told in the
// alert chat, the connections on a CLI checked again after it changed.

// cliKinds is the connection kind each CLI drives.
var cliKinds = map[string]storage.ProviderKind{
	"claude": storage.ProviderClaudeCLI, "codex": storage.ProviderCodexCLI, "gemini": storage.ProviderGeminiCLI,
	"antigravity": storage.ProviderAntigravityCLI, "dsh": storage.ProviderDSH,
}

// cliUpdates is the CLIs' updates for the cards (actions.CLIUpdater), the
// bots (channels.CLIUpdater) and the watch.
type cliUpdates struct {
	m         *clitools.Manager
	store     storage.Store
	providers *provider.Service
	notify    func(ctx context.Context, channelID, chatID, text string) error
}

func (c cliUpdates) name(tool string) string {
	if st, err := c.m.Status(context.Background(), tool); err == nil && st.Name != "" {
		return st.Name
	}
	return tool
}

// KnownCLI: tool is a CLI office knows and it is installed.
func (c cliUpdates) KnownCLI(tool string) error {
	installed, _, err := c.m.Detect(context.Background(), strings.TrimSpace(tool))
	switch {
	case err != nil:
		return fmt.Errorf("không có CLI %q (có: %s)", tool, strings.Join(c.m.IDs(), ", "))
	case !installed:
		return fmt.Errorf("máy chưa cài %s", tool)
	}
	return nil
}

// UpdateCLIWhenIdle is an approved cli_update: an update already on its way
// is fine.
func (c cliUpdates) UpdateCLIWhenIdle(_ context.Context, tool, by string) error {
	_, err := c.m.UpdateWhenIdle(strings.TrimSpace(tool), by)
	if errors.Is(err, clitools.ErrUpgrading) {
		return nil
	}
	return err
}

// CLIStatus says per installed CLI its version, the newest and its update.
func (c cliUpdates) CLIStatus(ctx context.Context) string {
	var lines []string
	for _, st := range c.m.List(ctx) {
		if !st.Installed {
			continue
		}
		line := "• " + st.Name + " " + cmpOr(clitools.VersionOf(st.Version), st.Version)
		if st.Outdated {
			line += " (có bản " + st.Latest + ")"
		}
		if st.Upgrade != nil && (st.Upgrade.Active() || st.Upgrade.Ended != nil && time.Since(*st.Upgrade.Ended) < time.Hour) {
			line += " — " + st.Upgrade.Summary(st.Name)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "Máy chưa cài CLI AI nào."
	}
	return "CLI AI trên máy:\n" + strings.Join(lines, "\n") + "\nCập nhật: /update cli <tên> (" + strings.Join(c.m.IDs(), ", ") + ")"
}

// UpdateCLI is /update cli <tool>.
func (c cliUpdates) UpdateCLI(_ context.Context, tool, by string) (string, error) {
	u, err := c.m.UpdateWhenIdle(tool, by)
	if errors.Is(err, clitools.ErrUpgrading) {
		return u.Summary(c.name(tool)), nil
	}
	if err != nil {
		return "", err
	}
	if u.Waiting == 0 {
		return c.name(tool) + ": đang cập nhật, office báo khi xong.", nil
	}
	return u.Summary(c.name(tool)) + ". Office báo khi xong.", nil
}

// recheck tests again the connections a CLI drives once it changed (as
// Kết nối AI's Kiểm tra): one marked broken by a run that failed on it is
// cleared when it answers. It says why the CLI's install is broken ("" = it
// is not): a signed-out account or a network error is the connection's, not
// the install's, and is not a reason to put the old version back.
func (c cliUpdates) recheck(ctx context.Context, tool string) string {
	kind, ok := cliKinds[tool]
	if !ok || c.providers == nil {
		return ""
	}
	list, err := c.store.Providers().List(ctx)
	if err != nil {
		return ""
	}
	shared := samePath(c.m.LookPath(cliBins[tool]))
	for _, p := range list {
		// only the connections that run this install: one pointed at a
		// binary of its own (a path in BaseURL) neither tells about it nor
		// makes it go back
		if p.Kind != kind || !c.runsInstall(c.providers.CLIBin(p), shared) {
			continue
		}
		res, err := c.providers.Test(ctx, p.ID, "", "")
		if err == nil && !res.OK && installBroken(res.Detail) {
			return res.Detail
		}
	}
	return ""
}

// cliBins is each CLI's command.
var cliBins = map[string]string{"claude": "claude", "codex": "codex", "gemini": "gemini", "antigravity": "agy", "dsh": "dsh"}

// samePath is p with its links followed ("" stays "").
func samePath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// runsInstall: a connection's command is the install at shared. A bare
// name is looked up on PATH the way runs find it (another command, e.g.
// claude-old, is another install); a relative path depends on the run's
// folder, so it cannot be told to be this install and is not counted.
func (c cliUpdates) runsInstall(bin, shared string) bool {
	switch {
	case shared == "" || bin == "":
		return false
	case filepath.IsAbs(bin):
		return samePath(bin) == shared
	case strings.ContainsRune(bin, filepath.Separator):
		return false
	}
	return samePath(c.m.LookPath(bin)) == shared
}

// installBroken: a check's failure says the CLI itself cannot start.
func installBroken(detail string) bool {
	return strings.Contains(detail, llm.ErrCLIBroken.Error()) || llm.CLIBroken(detail)
}

// tell sends text to the chat the admin picked for alerts (Kết nối AI); it
// says whether it went (none picked, or the bot failing: not sent, so the
// watch tells it again on its next look).
func (c cliUpdates) tell(ctx context.Context, text string) bool {
	s := limitalert.Load(ctx, c.store)
	if s.ChannelID == "" || s.ChatID == "" || c.notify == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.notify(ctx, s.ChannelID, s.ChatID, text); err != nil {
		slog.Warn("cli update: tell", "err", err)
		return false
	}
	return true
}

// ended tells how an update ended (not one a person cancelled).
func (c cliUpdates) ended(u clitools.UpgradeView) {
	if u.State == "cancelled" {
		return
	}
	icon := "✅ "
	if u.State != "done" {
		icon = "⚠️ "
	}
	c.tell(context.Background(), icon+u.Summary(c.name(u.Tool)))
}

// hooks are the watch's: the setting kept in the store; what was told is
// saved without overwriting a mode changed meanwhile.
func (c cliUpdates) hooks() clitools.WatchHooks {
	return clitools.WatchHooks{
		Load: func(ctx context.Context) clitools.AutoSetting {
			var s clitools.AutoSetting
			_, _ = c.store.Settings().Get(ctx, clitools.AutoUpdateKey, &s)
			return s
		},
		Save: func(ctx context.Context, s clitools.AutoSetting) {
			var cur clitools.AutoSetting
			_, _ = c.store.Settings().Get(ctx, clitools.AutoUpdateKey, &cur)
			cur.Told = s.Told
			_ = c.store.Settings().Set(ctx, clitools.AutoUpdateKey, cur)
		},
		Notify: c.tell,
	}
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// officeRoot is office's source folder ("" when this binary was not built
// from one): its project is offered update_cli.
func officeRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	if src, ok := selfupdate.FindSource(exe); ok {
		return src.Root
	}
	return ""
}
