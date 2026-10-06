package actions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
)

func TestAutoByPackage(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	test, _ := st.Processes().Create(ctx, storage.Process{ProjectID: proj.ID, Name: "test", Kind: "job", Command: "echo ok"})
	dev, _ := st.Processes().Create(ctx, storage.Process{ProjectID: proj.ID, Name: "dev", Kind: "service", Command: "sleep 30"})
	m := ops.NewManager(st, t.TempDir(), nil)
	defer m.Shutdown()
	svc := New(st, m)
	allProcs = []string{test.ID, dev.ID}

	sc := Scope{ProjectID: proj.ID, RunRef: "r1", Agent: "a", Level: perm.Propose}
	if a, _ := svc.Propose(ctx, sc, "run_process", "test", "kiểm tra"); a.Status != "pending" {
		t.Fatalf("propose level must wait: %s", a.Status)
	}
	sc.Level, sc.Access, sc.RunRef = perm.Check, at(perm.Check), "r2"
	sc.ConversationID, sc.JobID = "", "job_7"
	auto, _ := svc.Propose(ctx, sc, "run_process", "test", "kiểm tra")
	if auto.Status != "done" {
		t.Fatalf("check level runs an allowed job: %s %s", auto.Status, auto.Detail)
	}
	// ADR-043: what the agent did on its own is in the log, as the agent's
	rows, _ := st.Audit().List(ctx, storage.AuditFilter{JobID: "job_7"})
	if len(rows) != 1 || rows[0].Action != "action.approve" || rows[0].ActorKind != "agent" || rows[0].ActorName != "a" ||
		rows[0].ApprovedBy != "" || rows[0].ActionID != auto.ID || rows[0].ProjectID != proj.ID || rows[0].Detail["auto"] != true || !rows[0].OK {
		t.Fatalf("auto action audit: %+v", rows)
	}
	if a, _ := svc.Propose(ctx, sc, "restart_process", "dev", "treo"); a.Status != "pending" {
		t.Fatalf("restarting a service needs operate: %s", a.Status)
	}
	sc.Level, sc.Access, sc.RunRef = perm.Operate, at(perm.Operate), "r3"
	if a, _ := svc.Propose(ctx, sc, "run_process", "dev", "bật"); a.Status != "done" {
		t.Fatalf("operate runs an allowed service: %s %s", a.Status, a.Detail)
	}
	if _, err := svc.Propose(ctx, sc, "run_process", "nope", ""); err == nil {
		t.Fatal("unknown target must fail")
	}
}

func TestGitActions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	root := t.TempDir()
	gitRun := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	gitRun("init", "-q", "-b", "main")
	gitRun("config", "user.email", "t@x.io")
	gitRun("config", "user.name", "T")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("1\n"), 0o644)
	gitRun("add", ".")
	gitRun("commit", "-qm", "init")
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("2\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".env"), []byte("S=1\n"), 0o644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("b\n"), 0o644)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "g", Path: root})
	pol := perm.DefaultPolicy()
	pol.MaxLevel = perm.Operate
	perm.SavePolicy(ctx, st, proj.ID, pol)
	svc := New(st, nil)

	sc := Scope{ProjectID: proj.ID, RunRef: "g1", Agent: "a", Level: perm.Propose}
	a, err := svc.Propose(ctx, sc, "git_commit", "", "lưu", storage.ActionArgs{Message: "feat: a", Files: []string{"a.txt"}})
	if err != nil || a.Status != "pending" {
		t.Fatalf("propose must wait: %v %+v", err, a)
	}
	if _, err := svc.Propose(ctx, sc, "git_commit", "", "x", storage.ActionArgs{Message: "leak", Files: []string{".env"}}); err == nil {
		t.Fatal("denied file must not be committed")
	}
	sc.Level, sc.Access, sc.RunRef = perm.Edit, at(perm.Edit), "g2"
	a, err = svc.Propose(ctx, sc, "git_commit", "", "lưu", storage.ActionArgs{Message: "feat: b", Files: []string{"b.txt"}})
	if err != nil || a.Status != "done" {
		t.Fatalf("edit level commits on its own: %v %+v", err, a)
	}
	out, _ := exec.Command("git", "-C", root, "log", "--oneline", "-1", "--name-only").Output()
	if !strings.Contains(string(out), "feat: b") || !strings.Contains(string(out), "b.txt") || strings.Contains(string(out), "a.txt") {
		t.Fatalf("commit content: %s", out)
	}
	sc.Level, sc.Access, sc.RunRef = perm.Operate, at(perm.Operate), "g3"
	if a, _ := svc.Propose(ctx, sc, "git_push", "", "đẩy"); a.Status != "pending" {
		t.Fatalf("push must always wait: %s", a.Status)
	}
	if a, err := svc.Propose(ctx, sc, "git_branch", "", "nhánh", storage.ActionArgs{Branch: "feat/x"}); err != nil || a.Status != "done" {
		t.Fatalf("operate creates branches: %v %+v", err, a)
	}

	// a chat in its own worktree: git waits while its changes are not merged
	// into the project, then works on the project once they are
	conv, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: proj.ID})
	wt := Scope{ProjectID: proj.ID, ConversationID: conv.ID, RunRef: "g4", Agent: "a", Level: perm.Operate, Access: at(perm.Operate), Dir: t.TempDir()}
	p, _ := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: conv.ID, Diff: "x", Files: []string{"c.txt"}, Status: "pending", Origin: "worktree"})
	if _, err := svc.Propose(ctx, wt, "git_branch", "", "nhánh", storage.ActionArgs{Branch: "feat/y"}); err == nil || !strings.Contains(err.Error(), "gộp") {
		t.Fatalf("git with unmerged changes = %v", err)
	}
	st.Chat().DecidePatch(ctx, p.ID, "applied", "", "human:a", time.Now())
	wt.RunRef = "g5"
	if a, err := svc.Propose(ctx, wt, "git_branch", "", "nhánh", storage.ActionArgs{Branch: "feat/y"}); err != nil || a.Status != "done" {
		t.Fatalf("git after the merge = %v %+v", err, a)
	}
}

// at is the preset access of a package.
// at: an agent at level allowed to run/restart every process of the test
func at(level string) perm.Access {
	return perm.Access{Level: level, Caps: perm.Preset(level), Processes: allProcs}
}

var allProcs []string

func TestRunCommand(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "hello.txt"), []byte("xin chào\n"), 0o644)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "c", Path: root})
	svc := New(st, nil)
	acc := perm.Access{Level: perm.Check, Caps: []string{perm.CapPropose, perm.CapCommands}, Commands: []string{"cat *"}}
	sc := Scope{ProjectID: proj.ID, RunRef: "c1", Agent: "a", Level: perm.Check, Access: acc}

	a, err := svc.Propose(ctx, sc, "run_command", "cat hello.txt", "xem")
	if err != nil || a.Status != "done" || !strings.Contains(a.Detail, "xin chào") {
		t.Fatalf("allowed command runs: %v %+v", err, a)
	}
	sc.RunRef = "c2"
	if a, err := svc.Propose(ctx, sc, "run_command", "ls", "xem"); err != nil || a.Status != "pending" {
		t.Fatalf("other commands wait: %v %+v", err, a)
	}
	if _, err := svc.Propose(ctx, sc, "run_command", "cat hello.txt | wc", "x"); err == nil {
		t.Fatal("shell syntax must be rejected")
	}
	sc.RunRef = "c3"
	if a, _ := svc.Propose(ctx, sc, "run_command", "cat missing.txt", "x"); a.Status != "failed" {
		t.Fatalf("a failing command is failed: %+v", a)
	}
}
