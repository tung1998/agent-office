package burn

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// Only the Burns set to turn on by themselves are looked at.
func TestScheduledSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i, plan := range []storage.BurnPlan{{Auto: true, At: "22:00", Days: []int{1}}, {End: EndHours, Hours: 2}} {
		p, err := st.Repos().Create(ctx, storage.Repo{Name: "p" + string(rune('a'+i)), Path: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		b, err := st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: p.ID, State: "stopped", Plan: plan})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	list, err := st.Burn().Scheduled(ctx)
	if err != nil || len(list) != 1 || list[0].ID != ids[0] || list[0].Plan.At != "22:00" {
		t.Fatalf("scheduled = %+v, %v", list, err)
	}
	b, _ := st.Burn().SessionByID(ctx, ids[1])
	if b.Plan.End != EndHours || b.Plan.Hours != 2 {
		t.Fatalf("plan read back = %+v", b.Plan)
	}
}
