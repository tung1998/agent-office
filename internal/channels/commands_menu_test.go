package channels

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// An automation's command made before the bot took that name for its own
// (/update, ADR-255), or a name two automations bear: the menu keeps one of
// each name, else Discord/Telegram refuse it whole (ADR-258).
func TestCommandsSkipReservedNames(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "shop", Path: t.TempDir()})
	ch, _ := st.Channels().Create(ctx, storage.Channel{ProjectID: project.ID, Kind: "telegram", Name: "Bot", Enabled: true})
	for _, c := range []string{"update", "tra-don", "tra-don", "stop"} {
		if _, err := st.Automations().Create(ctx, storage.Automation{ProjectID: project.ID, Name: c, Source: "telegram", Action: "script", Enabled: true,
			Config: storage.AutomationConfig{ChannelID: ch.ID, Command: c}, Script: storage.AutomationScript{Lang: "bash", Body: "echo"}}); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManager(st, nil, nil, nil)
	m.updater = noUpdater{}
	seen := map[string]int{}
	for _, c := range m.commands(ctx, ch) {
		seen[c.Name]++
	}
	for name, n := range seen {
		if n != 1 {
			t.Fatalf("/%s %d times in the menu: %v", name, n, seen)
		}
	}
	if seen["tra-don"] != 1 || seen["update"] != 1 {
		t.Fatalf("menu = %v", seen)
	}
}

type noUpdater struct{ Updater }
