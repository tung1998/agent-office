package actions

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

// "Duyệt & luôn cho phép" runs the command, adds its pattern to a pack of the
// project and to the agent's commands: the next one like it runs on its own.
func TestDecideAlways(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@x.io"}, {"config", "user.name", "T"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("1\n"), 0o644)
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "init"}, {"branch", "dev"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: root})
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, proj.ID, solo.ID, false)
	model, _ := st.OrgModels().GetForRepo(ctx, proj.ID)
	agents, _ := st.Agents().List(ctx, model.ID)
	ag := agents[0]
	ag.Permissions = storage.Permissions{Level: perm.Check}
	st.Agents().Update(ctx, ag)
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: proj.ID, Kind: "chat_turn", Origin: "user", Trigger: "ui", Status: "running", AgentID: ag.ID})
	svc := New(st, nil)
	scope := func(ref string) Scope {
		a, _ := st.Agents().Get(ctx, ag.ID)
		acc := perm.Resolve(a, perm.Check, perm.LoadPolicy(ctx, st, proj.ID))
		return Scope{ProjectID: proj.ID, RunRef: ref, JobID: job.ID, Agent: ag.Name, Level: perm.Check, Access: acc}
	}

	a, err := svc.Propose(ctx, scope("r1"), "run_command", "git switch main", "về main")
	if err != nil || a.Status != "pending" {
		t.Fatalf("not allowed yet: %+v %v", a, err)
	}
	done, x, err := svc.DecideAlways(ctx, a.ID, "an@x.io")
	if err != nil || done.Status != "done" || x.Pattern != "git switch *" || x.Pack != "git (đã duyệt)" || !x.NewPack || !x.Auto || x.Error != "" {
		t.Fatalf("decide always = %+v %+v %v", done, x, err)
	}
	pol := perm.LoadPolicy(ctx, st, proj.ID)
	if !slices.ContainsFunc(pol.Packs, func(p perm.Pack) bool {
		return p.Label == "git (đã duyệt)" && slices.Contains(p.Commands, "git switch *")
	}) {
		t.Fatalf("packs = %+v", pol.Packs)
	}
	got, _ := st.Agents().Get(ctx, ag.ID)
	if got.Permissions.Commands == nil || !slices.Contains(*got.Permissions.Commands, "git switch *") || !slices.Contains(*got.Permissions.Commands, "git status") {
		t.Fatalf("agent commands = %v", got.Permissions.Commands)
	}
	rows, _ := st.Audit().List(ctx, storage.AuditFilter{JobID: job.ID})
	if !slices.ContainsFunc(rows, func(r storage.AuditEntry) bool {
		return r.Action == "agent.update" && r.Detail["allow_always"] == "git switch *" && r.Detail["approved_by"] == "an@x.io"
	}) {
		t.Fatalf("audit = %+v", rows)
	}

	// the next one like it runs on its own
	if b, err := svc.Propose(ctx, scope("r2"), "run_command", "git switch dev", "sang dev"); err != nil || b.Status != "done" || !strings.HasPrefix(b.DecidedBy, "auto:") {
		t.Fatalf("auto next = %+v %v", b, err)
	}

	// a risky command is refused before it runs
	c, _ := svc.Propose(ctx, scope("r3"), "run_command", "git push origin main", "đẩy")
	if _, _, err := svc.DecideAlways(ctx, c.ID, "an@x.io"); !errors.Is(err, perm.ErrNotAlways) {
		t.Fatalf("risky = %v", err)
	}
	if c, _ = st.Actions().Get(ctx, c.ID); c.Status != "pending" {
		t.Fatalf("risky must stay pending: %+v", c)
	}
}
