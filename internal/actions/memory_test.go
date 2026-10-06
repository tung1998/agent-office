package actions

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/memory"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// An agent's "remember" waits for a person, unless the project keeps its
// agents' notes on its own; approved, the note is the agent's.
func TestRemember(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, proj.ID, solo.ID, false)
	model, _ := st.OrgModels().GetForRepo(ctx, proj.ID)
	agents, _ := st.Agents().List(ctx, model.ID)
	mem := memory.New(st, nil)
	svc := New(st, nil)
	svc.SetMemory(mem)
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", Agent: agents[0].Name, Level: perm.Propose}
	a, err := svc.Propose(ctx, sc, "remember", "project dùng pnpm", "thấy lockfile")
	if err != nil || a.Status != "pending" || a.TargetID != agents[0].ID {
		t.Fatalf("propose = %+v %v", a, err)
	}
	if _, err := svc.Decide(ctx, a.ID, true, "human:a"); err != nil {
		t.Fatal(err)
	}
	list, _ := st.Memories().List(ctx, proj.ID, agents[0].ID)
	if len(list) != 1 || list[0].Text != "project dùng pnpm" || list[0].Source != "agent" {
		t.Fatalf("notes = %+v", list)
	}
	mem.SetAuto(ctx, proj.ID, true)
	sc.RunRef = "r2"
	if a, _ = svc.Propose(ctx, sc, "remember", "chạy pnpm test trước khi báo xong", ""); a.Status != "done" {
		t.Fatalf("auto = %+v", a)
	}
	if _, err := svc.Propose(ctx, Scope{ProjectID: proj.ID, Agent: "không có", Level: perm.Propose}, "remember", "x", ""); err == nil {
		t.Fatal("a note of an agent that is not in the project")
	}
}

// A chat in direct mode approves what the agent proposes at once (the agent
// gets the result in the same turn and goes on), unless the chat says no.
func TestDirectApprover(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: proj.ID, Kind: "chat_turn", Origin: "automation", Trigger: "discord", Status: "running"})
	svc := New(st, nil)
	var asked []string
	svc.SetAutoApprover(func(_ context.Context, a storage.Action) (string, bool) {
		asked = append(asked, a.Kind+" "+a.Target)
		return "discord:an", a.Target != "echo không"
	})
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", JobID: job.ID, Agent: "a", Level: perm.Propose}
	a, err := svc.Propose(ctx, sc, "run_command", "echo có", "thử")
	if err != nil || a.Status != "done" || a.DecidedBy != "discord:an" || !strings.Contains(a.Detail, "có") {
		t.Fatalf("direct = %+v %v", a, err)
	}
	sc.RunRef = "r2"
	if a, _ = svc.Propose(ctx, sc, "run_command", "echo không", "thử"); a.Status != "pending" {
		t.Fatalf("refused by the chat = %+v", a)
	}
	sc.RunRef, sc.JobID = "r3", ""
	if a, _ = svc.Propose(ctx, sc, "run_command", "echo web", "thử"); a.Status != "pending" || len(asked) != 2 {
		t.Fatalf("no job, no chat to ask: %+v %v", a, asked)
	}
}
