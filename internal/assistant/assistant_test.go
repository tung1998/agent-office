package assistant_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/assistant"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func TestEnsureOnce(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	dir := t.TempDir()
	id, err := assistant.Ensure(ctx, st, org, dir)
	if err != nil || id == "" {
		t.Fatalf("ensure = %q %v", id, err)
	}
	again, err := assistant.Ensure(ctx, st, org, dir)
	if err != nil || again != id {
		t.Fatalf("second ensure = %q %v (want %q)", again, err, id)
	}
	if got := assistant.ID(ctx, st); got != id {
		t.Fatalf("ID = %q", got)
	}
	m, err := st.OrgModels().GetForRepo(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	agents, _ := st.Agents().List(ctx, m.ID)
	if len(agents) != 1 || agents[0].Name != "Trợ lý office" || agents[0].Instructions == "" {
		t.Fatalf("agents = %+v", agents)
	}
	repos, _ := st.Repos().List(ctx)
	if len(repos) != 1 || repos[0].Path != dir {
		t.Fatalf("repos = %+v", repos)
	}
}

// The assistant's rights: answer only, help run the office (default), or administrator.
func TestMode(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	if m := assistant.Mode(ctx, st); m != assistant.ModeManage {
		t.Fatalf("default = %q", m)
	}
	if err := assistant.SetMode(ctx, st, "root"); err == nil {
		t.Fatal("an unknown mode was saved")
	}
	if err := assistant.SetMode(ctx, st, assistant.ModeAdmin); err != nil || assistant.Mode(ctx, st) != assistant.ModeAdmin {
		t.Fatalf("admin = %v %q", err, assistant.Mode(ctx, st))
	}
	// administrator only for an admin of the office; others get the default
	if assistant.Powers(assistant.ModeAdmin, false) != assistant.ModeManage || assistant.Powers(assistant.ModeAdmin, true) != assistant.ModeAdmin {
		t.Fatal("administrator must need an admin")
	}
	if assistant.Powers(assistant.ModeAnswer, true) != assistant.ModeAnswer {
		t.Fatal("answer only is for everyone")
	}
}
