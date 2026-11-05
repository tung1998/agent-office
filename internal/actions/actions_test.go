package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/ops"
	"github.com/tung1998/agent-office/internal/peer"
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

	// ADR-189: a commit asked with the edits waits for their diff, then runs
	// on the agent's permission; only the diff's files, never another chat's
	wt.RunRef = "g6"
	p2, _ := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: conv.ID, Diff: "x", Files: []string{"d.txt"}, Status: "pending", Origin: "worktree"})
	os.WriteFile(filepath.Join(root, "other.txt"), []byte("another chat\n"), 0o644)
	a, err = svc.Propose(ctx, wt, "git_commit", "", "lưu", storage.ActionArgs{Message: "feat: d"})
	if err != nil || a.Status != "pending" || !a.Args.AfterDiff || strings.Join(a.Args.Files, ",") != "d.txt" {
		t.Fatalf("commit before the merge = %v %+v", err, a)
	}
	if q, err := svc.RunDeferred(ctx, wt, a.ID, true); err != nil || q.Status != "queued" {
		t.Fatalf("behind a waiting diff = %v %+v", err, q)
	}
	q, _ := st.Actions().Get(ctx, a.ID) // back to waiting, to run it after the merge below
	q.Status = "pending"
	st.Actions().Update(ctx, q)
	os.WriteFile(filepath.Join(root, "d.txt"), []byte("d\n"), 0o644) // the diff merged
	st.Chat().DecidePatch(ctx, p2.ID, "applied", "", "human:a", time.Now())
	if done, err := svc.RunDeferred(ctx, wt, a.ID, false); err != nil || done.Status != "done" {
		t.Fatalf("after the merge = %v %+v", err, done)
	}
	out, _ = exec.Command("git", "-C", root, "log", "--oneline", "-1", "--name-only").Output()
	if !strings.Contains(string(out), "feat: d") || !strings.Contains(string(out), "d.txt") || strings.Contains(string(out), "other.txt") {
		t.Fatalf("deferred commit content: %s", out)
	}
	// a read-only run: it waits for a person after the diff
	wt.RunRef, wt.Level, wt.Access = "g7", perm.Propose, at(perm.Propose)
	st.Chat().AddPatch(ctx, storage.Patch{ConversationID: conv.ID, Diff: "x", Files: []string{"e.txt"}, Status: "pending", Origin: "worktree"})
	a, _ = svc.Propose(ctx, wt, "git_commit", "", "lưu", storage.ActionArgs{Message: "feat: e"})
	if left, _ := svc.RunDeferred(ctx, wt, a.ID, false); left.Status != "pending" {
		t.Fatalf("propose level = %+v", left)
	}
	// an administrator's run (ADR-188): its held commit acts as its other
	// proposals do, whatever its own package lacks; queued behind a waiting diff
	adm := wt
	adm.Admin, adm.AdminBy = true, "a@b.c"
	if q, err := svc.RunDeferred(ctx, adm, a.ID, true); err != nil || q.Status != "queued" || !strings.Contains(q.DecidedBy, "administrator") {
		t.Fatalf("admin, behind a waiting diff = %v %+v", err, q)
	}
	// a chat in direct mode (ADR-054): approved by the chat, queued behind the
	// diff instead of left for a person once the diff is merged
	wt.RunRef, wt.JobID = "g8", "job_x"
	st.Chat().AddPatch(ctx, storage.Patch{ConversationID: conv.ID, Diff: "x", Files: []string{"f.txt"}, Status: "pending", Origin: "worktree"})
	a, _ = svc.Propose(ctx, wt, "git_commit", "", "lưu", storage.ActionArgs{Message: "feat: f"})
	svc.SetAutoApprover(func(context.Context, storage.Action) (string, bool) { return "discord:an", true })
	defer svc.SetAutoApprover(nil)
	if q, err := svc.RunDeferred(ctx, wt, a.ID, true); err != nil || q.Status != "queued" || q.DecidedBy != "discord:an" {
		t.Fatalf("direct, behind a waiting diff = %v %+v", err, q)
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

	// in a chat a pending command is reused, but not one for another folder
	conv, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: proj.ID})
	sc.ConversationID, sc.Dir = conv.ID, root
	first, _ := svc.Propose(ctx, sc, "run_command", "ls", "x")
	if again, _ := svc.Propose(ctx, sc, "run_command", "ls", "x"); again.ID != first.ID {
		t.Fatalf("same command, same folder: one card: %s %s", first.ID, again.ID)
	}
	sc.Dir = ""
	if other, _ := svc.Propose(ctx, sc, "run_command", "ls", "x"); other.ID == first.ID || other.Status != "pending" {
		t.Fatalf("same command in another folder is its own card: %+v", other)
	}

	// whoever decided gave up waiting (a tool call timed out): it still runs
	// and is recorded, not left pending
	gone, cancel := context.WithCancel(ctx)
	cancel()
	svc.Decide(gone, first.ID, true, "x")
	if got, _ := st.Actions().Get(ctx, first.ID); got.Status != "done" {
		t.Fatalf("a decided command ends recorded: %+v", got)
	}
}

// TestRunCommandRisky: a command that deletes or publishes follows the
// permissions like any other: in the agent's commands it runs on its own,
// out of them it waits for a person.
func TestRunCommandRisky(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "r", Path: t.TempDir()})
	svc := New(st, nil)
	acc := perm.Access{Level: perm.Operate, Caps: []string{perm.CapPropose, perm.CapCommands},
		Commands: []string{"rm *", "go version *"}}
	sc := Scope{ProjectID: proj.ID, RunRef: "r1", Agent: "a", Level: perm.Operate, Access: acc}

	if a, err := svc.Propose(ctx, sc, "run_command", "rm -f nothing-here", "x"); err != nil || !strings.HasPrefix(a.DecidedBy, "auto:") {
		t.Fatalf("an allowed risky command runs on its own: %v %+v", err, a)
	}
	sc.RunRef = "r2"
	if a, err := svc.Propose(ctx, sc, "run_command", "git push origin main", "x"); err != nil || a.Status != "pending" {
		t.Fatalf("a command out of the agent's list waits for a person: %v %+v", err, a)
	}
	sc.RunRef = "r3"
	if a, err := svc.Propose(ctx, sc, "run_command", "go version", "x"); err != nil || a.Status != "done" {
		t.Fatalf("an allowed command runs on its own: %v %+v", err, a)
	}
}

type fakeSender struct{}

func (fakeSender) Relay(ctx context.Context, a storage.Action) (string, string, error) {
	return "", a.TargetID, nil
}
func (fakeSender) Relayed(ctx context.Context, conversationID string) bool { return false }

// TestCheckSendCrossProject: checkSend must reject a target chat that belongs
// to a different project than the one the run is in, even when the target is
// otherwise a valid, un-cleaned conversation (IDOR: a run in project A could
// otherwise inject a message into a chat of project B).
func TestCheckSendCrossProject(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p1, _ := st.Repos().Create(ctx, storage.Repo{Name: "p1", Path: t.TempDir()})
	p2, _ := st.Repos().Create(ctx, storage.Repo{Name: "p2", Path: t.TempDir()})

	own, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p1.ID, Title: "own"})
	other, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p2.ID, Title: "other"})
	job, err := st.Jobs().Create(ctx, storage.Job{ProjectID: p1.ID, Kind: "chat_turn", Origin: "user", Status: "done", CreatedBy: "human:u1", ConversationID: own.ID})
	if err != nil {
		t.Fatal(err)
	}

	svc := New(st, nil)
	svc.SetSender(fakeSender{})
	sc := Scope{ProjectID: p1.ID, ConversationID: own.ID, JobID: job.ID}
	a := &storage.Action{ProjectID: p1.ID, Target: other.ID, Args: storage.ActionArgs{Message: "xin chào"}}
	if err := svc.checkSend(ctx, sc, a); err == nil {
		t.Fatalf("send_message must not reach a chat of another project, got nil error, action=%+v", a)
	}
}

// A project that links another (perm.Policy.LinkedProjects) may send to its
// chats or open a new one there; one it does not link stays out of reach,
// and sending there runs on its own only with chat.cross.
func TestCheckSendLinkedProject(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p1, _ := st.Repos().Create(ctx, storage.Repo{Name: "mockup", Path: t.TempDir()})
	p2, _ := st.Repos().Create(ctx, storage.Repo{Name: "Admin v3", Path: t.TempDir()})
	p3, _ := st.Repos().Create(ctx, storage.Repo{Name: "p3", Path: t.TempDir()})
	if err := perm.SavePolicyIf(ctx, st, p1.ID, "", perm.Policy{LinkedProjects: []string{p2.ID}}); err != nil {
		t.Fatal(err)
	}
	own, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p1.ID, Title: "own"})
	linked, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p2.ID, Title: "admin"})
	unlinked, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p3.ID, Title: "p3"})
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p1.ID, Kind: "chat_turn", Origin: "user", Status: "done", CreatedBy: "human:u1", ConversationID: own.ID})
	svc := New(st, nil)
	svc.SetSender(fakeSender{})
	sc := Scope{ProjectID: p1.ID, ConversationID: own.ID, JobID: job.ID}

	a := &storage.Action{ProjectID: p1.ID, Target: linked.ID, Args: storage.ActionArgs{Message: "sửa trang admin"}}
	if err := svc.checkSend(ctx, sc, a); err != nil || a.TargetID != linked.ID || a.Args.Project != p2.ID {
		t.Fatalf("linked chat: %v %+v", err, a)
	}
	a = &storage.Action{ProjectID: p1.ID, Args: storage.ActionArgs{Message: "sửa trang admin", Project: "admin V3"}}
	if err := svc.checkSend(ctx, sc, a); err != nil || a.TargetID != "" || a.Args.Project != p2.ID || !strings.Contains(a.Target, "Admin v3") {
		t.Fatalf("new chat by name: %v %+v", err, a)
	}
	a = &storage.Action{ProjectID: p1.ID, Target: unlinked.ID, Args: storage.ActionArgs{Message: "x"}}
	if err := svc.checkSend(ctx, sc, a); err == nil {
		t.Fatal("a chat of a project not linked was accepted")
	}
	a = &storage.Action{ProjectID: p1.ID, Args: storage.ActionArgs{Message: "x", Project: p3.ID}}
	if err := svc.checkSend(ctx, sc, a); err == nil {
		t.Fatal("a new chat in a project not linked was accepted")
	}
	a = &storage.Action{ProjectID: p1.ID, Target: unlinked.ID, Args: storage.ActionArgs{Message: "x", Project: p2.ID}}
	if err := svc.checkSend(ctx, sc, a); err == nil {
		t.Fatal("a chat not of the project named was accepted")
	}

	cross := storage.Action{Kind: "send_message", ProjectID: p1.ID, Args: storage.ActionArgs{Project: p2.ID}}
	same := storage.Action{Kind: "send_message", ProjectID: p1.ID}
	sendOnly := perm.Access{Caps: []string{perm.CapChatSend}}
	if svc.autoAllowed(ctx, cross, sendOnly) || !svc.autoAllowed(ctx, same, sendOnly) {
		t.Fatal("chat.send alone: only the project's own chats run on their own")
	}
	if !svc.autoAllowed(ctx, cross, perm.Access{Caps: []string{perm.CapChatCross}}) {
		t.Fatal("chat.cross: a linked project's chat runs on its own")
	}
}

// newChatSender opens a new chat for a send with no target (Engine.Relay).
type newChatSender struct{ fakeSender }

func (newChatSender) Relay(ctx context.Context, a storage.Action) (string, string, error) {
	return "đã gửi", "cnv_opened", nil
}

// A new chat opened in a linked project is kept on the action (its target),
// so the chat that sent it finds it again (LinkedPending, report back).
func TestSendNewChatKeepsTarget(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p1, _ := st.Repos().Create(ctx, storage.Repo{Name: "mockup", Path: t.TempDir()})
	p2, _ := st.Repos().Create(ctx, storage.Repo{Name: "admin", Path: t.TempDir()})
	perm.SavePolicyIf(ctx, st, p1.ID, "", perm.Policy{LinkedProjects: []string{p2.ID}})
	own, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p1.ID})
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p1.ID, Kind: "chat_turn", Origin: "user", Status: "running", CreatedBy: "human:u1", ConversationID: own.ID})
	svc := New(st, nil)
	svc.SetSender(newChatSender{})
	sc := Scope{ProjectID: p1.ID, ConversationID: own.ID, JobID: job.ID, Agent: "Lead", Level: perm.Operate, Access: perm.Access{Level: perm.Operate, Caps: []string{perm.CapChatCross}}}
	a, err := svc.Propose(ctx, sc, "send_message", "", "giao việc", storage.ActionArgs{Message: "làm trang mockup", Project: "admin"})
	if err != nil || a.Status != "done" || a.TargetID != "cnv_opened" {
		t.Fatalf("propose = %v %+v", err, a)
	}
	if got, _ := st.Actions().Get(ctx, a.ID); got.TargetID != "cnv_opened" {
		t.Fatalf("stored target = %q", got.TargetID)
	}
	// a copy read before the chat was known never clears it
	stale := a
	stale.TargetID = ""
	st.Actions().Update(ctx, stale)
	if got, _ := st.Actions().Get(ctx, a.ID); got.TargetID != "cnv_opened" {
		t.Fatalf("target cleared by a stale copy: %q", got.TargetID)
	}
}

// What may run on its own (a check) runs at once even while a proposal of its
// chat waits for a person: it changes nothing, so the order is kept.
func TestAutoRunsWhileAProposalWaits(t *testing.T) {
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
	m := ops.NewManager(st, t.TempDir(), nil)
	defer m.Shutdown()
	svc := New(st, m)
	allProcs = []string{test.ID}
	conv, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: proj.ID})
	_, _ = st.Actions().Create(ctx, storage.Action{ProjectID: proj.ID, ConversationID: conv.ID, Kind: "run_command", Target: "env X=1 pnpm typecheck"})
	sc := Scope{ProjectID: proj.ID, ConversationID: conv.ID, RunRef: "r1", Agent: "a", Level: perm.Check, Access: at(perm.Check)}
	if a, _ := svc.Propose(ctx, sc, "run_process", "test", "kiểm tra"); a.Status != "done" {
		t.Fatalf("a check runs on its own: %s", a.Status)
	}
}

// A project in another office is reached from the MCP other_office
// (ADR-169): the send carries the office; the project must be linked, or the
// chat one this conversation handed work to (the assistant: any). The old
// names in send_to_chat point there. It runs on its own only with chat.cross.
func TestCheckSendPeerProject(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	p1, _ := st.Repos().Create(ctx, storage.Repo{Name: "mockup", Path: t.TempDir()})
	st.Settings().Set(ctx, "peers", []peer.Peer{{ID: "peer_b", Name: "office-b", URL: "https://b.example",
		Remote: []peer.RemoteProject{{ID: "rep_shop", Name: "shop"}, {ID: "rep_other", Name: "other"}}}})
	perm.SavePolicyIf(ctx, st, p1.ID, "", perm.Policy{LinkedProjects: []string{"peer:peer_b/rep_shop"}})
	own, _ := st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: p1.ID})
	job, _ := st.Jobs().Create(ctx, storage.Job{ProjectID: p1.ID, Kind: "chat_turn", Origin: "user", Status: "done", CreatedBy: "human:u1", ConversationID: own.ID})
	svc := New(st, nil)
	svc.SetSender(fakeSender{})
	svc.SetPeers(peer.New(st, nil))
	sc := Scope{ProjectID: p1.ID, ConversationID: own.ID, JobID: job.ID}

	a := &storage.Action{ProjectID: p1.ID, Args: storage.ActionArgs{Message: "làm trang shop", Project: "rep_shop", Peer: "peer_b"}}
	if err := svc.checkSend(ctx, sc, a); err != nil || a.TargetID != "" || a.Target != "office-b/shop · chat mới" {
		t.Fatalf("new chat there: %v %+v", err, a)
	}
	a = &storage.Action{ProjectID: p1.ID, Target: "cnv_9", Args: storage.ActionArgs{Message: "tiếp", Project: "rep_shop", Peer: "peer_b"}}
	if err := svc.checkSend(ctx, sc, a); err != nil || a.TargetID != "cnv_9" {
		t.Fatalf("its chat: %v %+v", err, a)
	}
	a = &storage.Action{ProjectID: p1.ID, Args: storage.ActionArgs{Message: "x", Project: "rep_other", Peer: "peer_b"}}
	if err := svc.checkSend(ctx, sc, a); err == nil {
		t.Fatal("a project of the office not linked was accepted")
	}
	a = &storage.Action{ProjectID: p1.ID, Target: "cnv_1", Args: storage.ActionArgs{Message: "x", Project: "rep_other", Peer: "peer_b"}}
	if err := svc.checkSend(ctx, sc, a); err == nil {
		t.Fatal("a chat of a project not linked was accepted")
	}
	// a chat this conversation handed work to: on, even unlinked
	st.Actions().Create(ctx, storage.Action{Kind: "send_message", ProjectID: p1.ID, ConversationID: own.ID, Status: "done", TargetID: "cnv_1", Args: storage.ActionArgs{Peer: "peer_b", Project: "rep_other"}})
	a = &storage.Action{ProjectID: p1.ID, Target: "cnv_1", Args: storage.ActionArgs{Message: "x", Peer: "peer_b"}}
	if err := svc.checkSend(ctx, sc, a); err != nil || a.TargetID != "cnv_1" || a.Args.Project != "rep_other" {
		t.Fatalf("a chat handed work to before (its project kept): %v %+v", err, a)
	}
	// having handed work to one chat opens no new chat in an unlinked project
	a = &storage.Action{ProjectID: p1.ID, Args: storage.ActionArgs{Message: "x", Project: "rep_other", Peer: "peer_b"}}
	if err := svc.checkSend(ctx, sc, a); err == nil {
		t.Fatal("a new chat in an unlinked project was accepted after a hand-over")
	}
	// checked again when approved: a link removed meanwhile stops it
	prop, err := svc.Propose(ctx, sc, "send_message", "", "r", storage.ActionArgs{Message: "x", Project: "rep_shop", Peer: "peer_b"})
	if err != nil || prop.Status != "pending" {
		t.Fatalf("propose: %v %+v", err, prop)
	}
	perm.SavePolicy(ctx, st, p1.ID, perm.Policy{})
	if got, err := svc.Decide(ctx, prop.ID, true, "human:u1"); err != nil || got.Status != "failed" {
		t.Fatalf("approved after the link was removed: %v %+v", err, got)
	}
	perm.SavePolicy(ctx, st, p1.ID, perm.Policy{LinkedProjects: []string{"peer:peer_b/rep_shop"}})
	// the assistant: any project the office opens
	a = &storage.Action{ProjectID: p1.ID, Args: storage.ActionArgs{Message: "x", Project: "rep_other", Peer: "peer_b"}}
	if err := svc.checkSend(ctx, Scope{ProjectID: p1.ID, ConversationID: own.ID, JobID: job.ID, Office: true}, a); err != nil {
		t.Fatalf("assistant: %v", err)
	}
	a = &storage.Action{ProjectID: p1.ID, Args: storage.ActionArgs{Message: "x", Project: "rep_shop", Peer: "peer_gone"}}
	if err := svc.checkSend(ctx, sc, a); err == nil {
		t.Fatal("an office no longer paired was accepted")
	}
	// the old ways point to other_office
	for _, a := range []*storage.Action{
		{ProjectID: p1.ID, Args: storage.ActionArgs{Message: "x", Project: "office-b/shop"}},
		{ProjectID: p1.ID, Target: "https://b.example/projects/rep_shop?tab=chat&c=cnv_9", Args: storage.ActionArgs{Message: "x"}},
	} {
		if err := svc.checkSend(ctx, sc, a); err == nil || !strings.Contains(err.Error(), "other_office") {
			t.Fatalf("old way %+v: %v", a, err)
		}
	}
	// the assistant's new chat: checked again when approved, against what the office still opens
	st.Settings().Set(ctx, "office_assistant_project", p1.ID)
	asc := Scope{ProjectID: p1.ID, ConversationID: own.ID, JobID: job.ID, Office: true}
	prop2, err := svc.Propose(ctx, asc, "send_message", "", "r", storage.ActionArgs{Message: "x", Project: "rep_other", Peer: "peer_b"})
	if err != nil || prop2.Status != "pending" {
		t.Fatalf("assistant propose: %v %+v", err, prop2)
	}
	st.Settings().Set(ctx, "peers", []peer.Peer{{ID: "peer_b", Name: "office-b", URL: "https://b.example",
		Remote: []peer.RemoteProject{{ID: "rep_shop", Name: "shop"}}}})
	if got, err := svc.Decide(ctx, prop2.ID, true, "human:u1"); err != nil || got.Status != "failed" {
		t.Fatalf("assistant's send to a project no longer open was approved: %v %+v", err, got)
	}
	byID := storage.Action{Kind: "send_message", ProjectID: p1.ID, Args: storage.ActionArgs{Peer: "peer_b"}}
	if svc.autoAllowed(ctx, byID, perm.Access{Caps: []string{perm.CapChatSend}}) {
		t.Fatal("another office's chat with no project ran on chat.send")
	}
	cross := storage.Action{Kind: "send_message", ProjectID: p1.ID, Args: storage.ActionArgs{Project: "rep_shop", Peer: "peer_b"}}
	if svc.autoAllowed(ctx, cross, perm.Access{Caps: []string{perm.CapChatSend}}) || !svc.autoAllowed(ctx, cross, perm.Access{Caps: []string{perm.CapChatCross}}) {
		t.Fatal("another office's project runs on its own only with chat.cross")
	}
}

// ADR-188: an administrator's run carries out what it proposes, but a push
// to master/main and a workflow's gate still wait for a person.
func TestAdminAllowed(t *testing.T) {
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
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "g", Path: root})
	svc := New(st, nil)

	for kind, want := range map[string]bool{"config_change": true, "create_automation": true, "data_delete": true, "mcp_call": true, "install_skill": true, "workflow_gate": false} {
		if got := svc.adminAllowed(ctx, storage.Action{Kind: kind, ProjectID: proj.ID}); got != want {
			t.Errorf("%s: %v, want %v", kind, got, want)
		}
	}
	push := storage.Action{Kind: "git_push", ProjectID: proj.ID}
	if svc.adminAllowed(ctx, push) {
		t.Fatal("a push from main ran without a person")
	}
	gitRun("checkout", "-q", "-b", "feat/x")
	if !svc.adminAllowed(ctx, push) {
		t.Fatal("a push of a feature branch waited")
	}
	gitRun("config", "branch.feat/x.remote", "origin")
	gitRun("config", "branch.feat/x.merge", "refs/heads/master")
	gitRun("remote", "add", "origin", t.TempDir())
	gitRun("update-ref", "refs/remotes/origin/master", "HEAD")
	if svc.adminAllowed(ctx, push) {
		t.Fatal("a branch tracking master pushed without a person")
	}
	// a git_push proposed by an admin's run on main still waits
	gitRun("checkout", "-q", "main")
	a, err := svc.Propose(ctx, Scope{ProjectID: proj.ID, RunRef: "r", Agent: "a", Level: perm.Operate, Access: perm.Admin(perm.Access{}), Admin: true}, "git_push", "", "push", storage.ActionArgs{})
	if err != nil || a.Status != "pending" {
		t.Fatalf("push from main: %v %+v", err, a)
	}
}

// ADR-196: a Burn run's branch merges on its own for an agent that may
// commit; any other branch waits for a person.
func TestMergeBurnBranchOnItsOwn(t *testing.T) {
	svc := &Service{}
	acc := perm.Access{Caps: []string{perm.CapCommit}}
	burnRun := storage.Action{Kind: "git_merge", Args: storage.ActionArgs{Branch: "burn/2026-10-25-0800"}}
	feature := storage.Action{Kind: "git_merge", Args: storage.ActionArgs{Branch: "feat/x"}}
	if !svc.autoAllowed(context.Background(), burnRun, acc) {
		t.Fatal("a reviewed Burn run did not merge on its own")
	}
	if svc.autoAllowed(context.Background(), feature, acc) || svc.autoAllowed(context.Background(), burnRun, perm.Access{}) {
		t.Fatal("merged without a person")
	}
}

// ADR-200: an edited automation is turned on or off by enabled (a past
// automatic turn-off cleared); without it, it stays as it is; a new one with
// enabled false comes in off.
func TestAutomationEnabled(t *testing.T) {
	ctx := context.Background()
	st, _ := sqlite.Open(filepath.Join(t.TempDir(), "o.db"))
	defer st.Close()
	st.Migrate(ctx)
	proj, _ := st.Repos().Create(ctx, storage.Repo{Name: "p", Path: t.TempDir()})
	a, _ := st.Automations().Create(ctx, storage.Automation{ProjectID: proj.ID, Name: "Điều phối", Source: "schedule", Action: "chat", Prompt: "x",
		Config: storage.AutomationConfig{EveryMinutes: 60}, DisabledCode: "failures", Failures: 3})
	svc := New(st, nil)
	save := func(kind, spec string) {
		t.Helper()
		if err := svc.saveAutomation(ctx, storage.Action{Kind: kind, ProjectID: proj.ID, Args: storage.ActionArgs{Automation: []byte(spec)}}); err != nil {
			t.Fatal(err)
		}
	}
	spec := `{"automation_id":"` + a.ID + `","name":"Điều phối","source":"schedule","every_minutes":60,"action":"chat","prompt":"y"%s}`
	save("update_automation", fmt.Sprintf(spec, ""))
	if got, _ := st.Automations().Get(ctx, a.ID); got.Enabled || got.Prompt != "y" {
		t.Fatalf("an edit without enabled turned it on: %+v", got)
	}
	save("update_automation", fmt.Sprintf(spec, `,"enabled":true`))
	if got, _ := st.Automations().Get(ctx, a.ID); !got.Enabled || got.DisabledCode != "" || got.Failures != 0 {
		t.Fatalf("not turned on: %+v", got)
	}
	save("create_automation", `{"name":"Mới","source":"schedule","every_minutes":60,"action":"chat","prompt":"z","enabled":false}`)
	list, _ := st.Automations().List(ctx, proj.ID)
	for _, x := range list {
		if x.Name == "Mới" && x.Enabled {
			t.Fatal("a new one with enabled false came in on")
		}
	}
}
