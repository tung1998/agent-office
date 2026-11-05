package burn_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/fakebin"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
	"github.com/tung1998/agent-office/internal/team"
	"github.com/tung1998/agent-office/internal/usage"
	"github.com/tung1998/agent-office/internal/worktree"
)

type fx struct {
	st      storage.Store
	svc     *burn.Service
	engine  *chat.Engine
	trees   *worktree.Manager
	project storage.Repo
	dir     string
	bin     string // the fake Claude Code: a test may write its own
}

// setup: a git project, its lead, a Claude Code that writes a file where it
// runs (the item's worktree) and takes a moment. wrap, when given, lets a
// test swap in a storage.Store that misbehaves on purpose (eg. panics).
func setup(t *testing.T, wrap ...func(storage.Store) storage.Store) fx {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	real, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	t.Cleanup(func() { real.Close() })
	real.Migrate(ctx)
	var st storage.Store = real
	for _, w := range wrap {
		st = w(st)
	}
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	org := team.NewService(st, nil)
	bin := filepath.Join(tmp, "claude")
	fakebin.Write(bin, []byte(`#!/bin/sh
cat > /dev/null
echo "do agent viết" > made-by-agent.txt
sleep 1
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"xong lượt","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`))
	provs.Create(ctx, provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
	dir := t.TempDir()
	git := func(args ...string) {
		c := exec.Command("git", append([]string{"-c", "user.email=t@x.io", "-c", "user.name=T"}, args...)...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	git("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	git("add", "-A")
	git("commit", "-qm", "init")
	project, _ := st.Repos().Create(ctx, storage.Repo{Name: "demo", Path: dir})
	org.AddFromLibrary(ctx, project.ID, team.StarterAgent)
	engine := chat.NewEngine(st, provs, u)
	trees := worktree.New(filepath.Join(tmp, "trees"))
	engine.SetWorktrees(trees)
	svc := burn.New(st, engine, trees)
	svc.SetFindWork(false) // these tests queue their pieces themselves
	return fx{st: st, svc: svc, engine: engine, trees: trees, project: project, dir: dir, bin: bin}
}

func waitItem(t *testing.T, st storage.Store, id, status string) storage.BurnItem {
	t.Helper()
	return waitItemFor(t, st, id, status, 15*time.Second)
}

func waitItemFor(t *testing.T, st storage.Store, id, status string, timeout time.Duration) storage.BurnItem {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
		if it, err := st.Burn().Item(context.Background(), id); err == nil && it.Status == status {
			return it
		}
	}
	it, _ := st.Burn().Item(context.Background(), id)
	t.Fatalf("item %s: %s, want %s", id, it.Status, status)
	return it
}

// waitDelivered waits for a piece done to be merged into its run (its
// worktree dropped).
func waitDelivered(t *testing.T, st storage.Store, id string) storage.BurnItem {
	t.Helper()
	it := waitItem(t, st, id, "done")
	for deadline := time.Now().Add(10 * time.Second); it.Worktree != "" && time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
		it, _ = st.Burn().Item(context.Background(), id)
	}
	if it.Worktree != "" {
		t.Fatalf("piece not merged into its run: %+v", it)
	}
	return it
}

// A chosen piece runs in its own worktree; reported done, what it changed is
// committed to the run's one burn/ branch (ADR-123), the next piece's too
// (nothing pushed, nothing on main).
func TestBurnDoesAPieceOnItsBranch(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.Start(ctx)
	ends := time.Now().Add(time.Hour)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", MaxParallel: 1, ResultMode: "branch", EndsAt: &ends, State: "stopped"})
	b, err := f.svc.Begin(ctx, f.project.ID, "admin@x.io")
	if err != nil {
		t.Fatal(err)
	}
	sc := actions.Scope{ProjectID: f.project.ID, ConversationID: b.ConversationID}
	out, err := f.svc.Tool(ctx, sc, "burn_add", burn.ToolInput{Title: "Sửa lỗi thanh toán", Kind: "bug", Detail: "checkout"})
	if err != nil || !strings.Contains(out, "bit_") {
		t.Fatal(out, err)
	}
	if again, _ := f.svc.Tool(ctx, sc, "burn_add", burn.ToolInput{Title: "sửa lỗi  thanh toán", Kind: "bug"}); !strings.Contains(again, "Đã có") {
		t.Fatalf("the same piece twice: %s", again)
	}
	items, _ := f.st.Burn().Items(ctx, b.ID)
	it := items[0]
	f.svc.Tool(ctx, sc, "burn_pick", burn.ToolInput{Item: it.ID})
	it = waitItem(t, f.st, it.ID, "doing")
	f.svc.Tool(ctx, sc, "burn_done", burn.ToolInput{Item: it.ID, Summary: "đã sửa, test qua"})
	it = waitDelivered(t, f.st, it.ID)
	if b, _ = f.st.Burn().Session(ctx, f.project.ID); it.Branch != b.RunBranch || !strings.HasPrefix(it.Branch, "burn/") {
		t.Fatalf("item = %+v, run %s", it, b.RunBranch)
	}
	show := exec.Command("git", "show", "--stat", it.Branch)
	show.Dir = f.dir
	if got, _ := show.CombinedOutput(); !strings.Contains(string(got), "made-by-agent.txt") || !strings.Contains(string(got), "burn: Sửa lỗi thanh toán") {
		t.Fatalf("branch %s: %s", it.Branch, got)
	}
	// the next piece starts from the run and lands on the same branch
	fakebin.Write(f.bin, []byte(`#!/bin/sh
cat > /dev/null
test -f made-by-agent.txt && echo "thứ hai" > second.txt
sleep 1
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"xong lượt","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`))
	f.svc.Tool(ctx, sc, "burn_add", burn.ToolInput{Title: "Việc thứ hai", Kind: "upgrade"})
	items, _ = f.st.Burn().Items(ctx, b.ID)
	next := items[len(items)-1]
	f.svc.Tool(ctx, sc, "burn_pick", burn.ToolInput{Item: next.ID})
	waitItem(t, f.st, next.ID, "doing")
	f.svc.Tool(ctx, sc, "burn_done", burn.ToolInput{Item: next.ID, Summary: "thêm second"})
	if next = waitDelivered(t, f.st, next.ID); next.Branch != it.Branch {
		t.Fatalf("second piece on %q, want %q", next.Branch, it.Branch)
	}
	log := exec.Command("git", "log", "--format=%s", "main.."+it.Branch)
	log.Dir = f.dir
	if got, _ := log.CombinedOutput(); string(got) != "burn: Việc thứ hai\nburn: Sửa lỗi thanh toán\n" {
		t.Fatalf("run branch log:\n%s", got)
	}
	if out, _ := exec.Command("git", "-C", f.dir, "branch", "--list", "burn/*").CombinedOutput(); strings.Count(string(out), "burn/") != 1 {
		t.Fatalf("one branch per run, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "made-by-agent.txt")); err == nil {
		t.Fatal("the project's own folder was changed")
	}
	// it was done in a hidden chat of its own (ADR-116), kept after it is done
	wc := it.WorkConversationID
	if c, err := f.st.Chat().GetConversation(ctx, wc); wc == "" || wc == b.ConversationID || err != nil || c.Purpose != chat.BurnWorkPurpose {
		t.Fatalf("work chat %q = %+v, %v", wc, c, err)
	}
	if s, err := f.st.Burn().SessionByConversation(ctx, wc); err != nil || s.ID != b.ID {
		t.Fatalf("its Burn = %+v, %v", s, err)
	}
	// there the burn_* tools touch that piece only
	other := next
	wsc := actions.Scope{ProjectID: f.project.ID, ConversationID: wc}
	if _, err := f.svc.Tool(ctx, wsc, "burn_fail", burn.ToolInput{Item: other.ID, Reason: "x"}); err == nil {
		t.Fatal("a piece's chat reported another piece")
	}
	if _, err := f.svc.Tool(ctx, wsc, "burn_pick", burn.ToolInput{Item: other.ID}); err == nil {
		t.Fatal("a piece's chat picked work")
	}
	if err := f.svc.Stop(ctx, f.project.ID); err != nil {
		t.Fatal(err)
	}
	// stopped, the run's worktree stays on its branch, to review there
	b, _ = f.st.Burn().Session(ctx, f.project.ID)
	tree := f.svc.RunTree(b)
	head := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	head.Dir = tree
	if got, err := head.CombinedOutput(); tree == "" || err != nil || strings.TrimSpace(string(got)) != b.RunBranch {
		t.Fatalf("run worktree %q on %s (%v), want %s", tree, got, err, b.RunBranch)
	}
}

// Stopped mid-piece, the piece waits (its worktree kept); started again it
// goes on first.
func TestBurnStopPausesAndGoesOn(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.Start(ctx)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", ResultMode: "branch", State: "stopped"})
	b, _ := f.svc.Begin(ctx, f.project.ID, "admin@x.io")
	it, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "làm dở", Kind: "unfinished", Status: "queued"})
	it = waitItem(t, f.st, it.ID, "doing")
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(it.Worktree, "made-by-agent.txt")); err == nil {
			break // its turn runs in its worktree
		}
	}
	if err := f.svc.Stop(ctx, f.project.ID); err != nil {
		t.Fatal(err)
	}
	it = waitItem(t, f.st, it.ID, "paused")
	if it.Worktree == "" {
		t.Fatal("no worktree kept")
	}
	if _, err := os.Stat(it.Worktree); err != nil {
		t.Fatalf("worktree gone: %v", err)
	}
	if _, err := f.svc.Begin(ctx, f.project.ID, "admin@x.io"); err != nil {
		t.Fatal(err)
	}
	waitItem(t, f.st, it.ID, "doing")
	f.svc.Stop(ctx, f.project.ID)
}

// The real case (2026-10-01): a Burn whose agent may only read in its mode
// still runs with full access, so it writes. It must write in its own
// worktree, never in the project's folder.
func TestBurnReadOnlyAgentStillWorksInItsWorktree(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agents, _ := f.st.Agents().List(ctx, f.project.ID)
	for _, a := range agents {
		a.Permissions.Level = perm.Read
		f.st.Agents().Update(ctx, a)
	}
	f.svc.Start(ctx)
	ends := time.Now().Add(time.Hour)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", ResultMode: "branch", EndsAt: &ends, State: "stopped"})
	b, err := f.svc.Begin(ctx, f.project.ID, "admin@x.io")
	if err != nil {
		t.Fatal(err)
	}
	sc := actions.Scope{ProjectID: f.project.ID, ConversationID: b.ConversationID}
	f.svc.Tool(ctx, sc, "burn_add", burn.ToolInput{Title: "Việc chỉ đọc", Kind: "bug"})
	items, _ := f.st.Burn().Items(ctx, b.ID)
	f.svc.Tool(ctx, sc, "burn_pick", burn.ToolInput{Item: items[0].ID})
	it := waitItem(t, f.st, items[0].ID, "doing")
	f.svc.Tool(ctx, sc, "burn_done", burn.ToolInput{Item: it.ID, Summary: "xong"})
	it = waitItem(t, f.st, it.ID, "done")
	if _, err := os.Stat(filepath.Join(f.dir, "made-by-agent.txt")); err == nil {
		t.Fatal("a read-only agent with full access wrote in the project's own folder")
	}
	if strings.Contains(it.Summary, "không giao được") {
		t.Fatalf("not committed: %s", it.Summary)
	}
	f.svc.Stop(ctx, f.project.ID)
}

// Office starting sweeps worktrees whose chat is gone: a Burn piece's own is
// not one of those (its work waits there while paused).
func TestSweepKeepsBurnWorktrees(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	b, _ := f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ResultMode: "branch", State: "stopped"})
	it, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "x", Kind: "bug", Status: "paused"})
	for _, name := range []string{"burn-" + it.ID, "burn-scan-" + b.ID, "burn-bit_gone"} {
		if _, err := f.trees.Ensure(ctx, f.dir, f.project.ID, name, nil); err != nil {
			t.Fatal(err)
		}
	}
	f.engine.SweepWorktrees(ctx, 0)
	for name, kept := range map[string]bool{"burn-" + it.ID: true, "burn-scan-" + b.ID: true, "burn-bit_gone": false} {
		if got := f.trees.Exists(f.project.ID, name); got != kept {
			t.Errorf("%s: exists=%v, want %v", name, got, kept)
		}
	}
}

// Office sweeps worktrees idle for a while by their folder's time, which
// does not move when only files inside change: a piece in progress (paused,
// or doing) and a running Burn's scan and run keep theirs however old; a
// piece that is over, a scan of a session that is gone, an old run, an
// orphan go.
func TestSweepOldKeepsWorkInProgress(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	b, _ := f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ResultMode: "branch", State: "running", RunBranch: "burn/2026-10-12-0800"})
	paused, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "dở", Kind: "bug", Status: "paused"})
	doing, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "đang làm", Kind: "bug", Status: "doing"})
	done, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "xong", Kind: "bug", Status: "done"})
	want := map[string]bool{"burn-" + paused.ID: true, "burn-" + doing.ID: true, "burn-" + done.ID: false,
		"burn-scan-bse_gone": false, "burn-bit_gone": false,
		"burn-scan-" + b.ID: true, "burn-run-2026-10-12-0800": true, "burn-run-2026-10-01-0800": false}
	month := time.Now().Add(-30 * 24 * time.Hour)
	for name := range want {
		dir, err := f.trees.Ensure(ctx, f.dir, f.project.ID, name, nil)
		if err != nil {
			t.Fatal(err)
		}
		os.Chtimes(dir, month, month)
	}
	f.engine.SweepWorktrees(ctx, 14*24*time.Hour)
	for name, kept := range want {
		if got := f.trees.Exists(f.project.ID, name); got != kept {
			t.Errorf("%s: exists=%v, want %v", name, got, kept)
		}
	}
}

// A chat worktree's own directory mtime is frozen at creation (nested writes
// never bump it), so Sweep must not rely on it alone: a recently active
// conversation, or one still holding an unreviewed patch from that worktree,
// must survive even when its folder looks untouched for maxAge.
func TestSweepChatWorktreePinsActiveWork(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	month := time.Now().Add(-30 * 24 * time.Hour)

	mkConv := func(updated time.Time) storage.Conversation {
		c, err := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: f.project.ID, Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.Chat().AddMessage(ctx, storage.Message{ConversationID: c.ID, Role: "user", Content: "hi", CreatedAt: updated}); err != nil {
			t.Fatal(err)
		}
		return c
	}

	recent := mkConv(time.Now())  // just touched: kept, even if its folder looks old
	oldWithPatch := mkConv(month) // old, but an unreviewed patch from its own worktree waits
	oldNoPatch := mkConv(month)   // old, nothing waiting: swept
	cleaned, _ := f.st.Chat().CreateConversation(ctx, storage.Conversation{ProjectID: f.project.ID, Title: "c"})
	otherAgentTree := mkConv(month) // the patch below names a different worktree than this agent's own

	if err := f.st.Chat().CleanConversation(ctx, cleaned.ID, "CleanContent", "note"); err != nil {
		t.Fatal(err)
	}

	if _, err := f.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: oldWithPatch.ID, Diff: "x", Origin: "worktree", Tree: chat.ChatTree(oldWithPatch.ID)}); err != nil {
		t.Fatal(err)
	}
	// pins only the tree it names, not a sibling agent tree of the same chat
	if _, err := f.st.Chat().AddPatch(ctx, storage.Patch{ConversationID: otherAgentTree.ID, Diff: "x", Origin: "worktree", Tree: chat.ChatTree(otherAgentTree.ID)}); err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		chat.ChatTree(recent.ID):                           true,
		chat.ChatTree(oldWithPatch.ID):                     true,
		chat.ChatTree(oldNoPatch.ID):                       false,
		chat.ChatTree(cleaned.ID):                          false,
		chat.ChatAgentTree(otherAgentTree.ID, "agent-two"): false,
	}
	for name := range want {
		dir, err := f.trees.Ensure(ctx, f.dir, f.project.ID, name, nil)
		if err != nil {
			t.Fatal(err)
		}
		os.Chtimes(dir, month, month)
	}

	f.engine.SweepWorktrees(ctx, 14*24*time.Hour)
	for name, kept := range want {
		if got := f.trees.Exists(f.project.ID, name); got != kept {
			t.Errorf("%s: exists=%v, want %v", name, got, kept)
		}
	}
}

// Without git there is no worktree: Burn would edit the project's own folder,
// so it does not start.
func TestBurnNeedsGit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	plain, _ := f.st.Repos().Create(ctx, storage.Repo{Name: "plain", Path: t.TempDir()})
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: plain.ID, ResultMode: "branch", State: "stopped"})
	_, err := f.svc.Begin(ctx, plain.ID, "admin@x.io")
	if err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("Burn on a folder without git: %v", err)
	}
}

// reviewer answers the Burn's reviews (ADR-112): the problem turned down,
// the result agreed; a work turn writes a file, as setup's.
const reviewer = `#!/bin/sh
p=$(cat)
case "$p" in
*"issue review"*) r="VERDICT: DISAGREE. Không có thật";;
*"result review"*) r="VERDICT: AGREE. Đúng phạm vi";;
*) echo "do agent viết" > made-by-agent.txt; sleep 1; r="xong lượt";;
esac
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"'"$r"'","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`

// profile saves a review profile reviewing stages, by the Burn's agent.
func (f fx) profile(t *testing.T, stages ...string) string {
	t.Helper()
	p := storage.BurnReviewProfile{ProjectID: f.project.ID, Name: "test", Stages: map[string]storage.BurnReviewStage{}}
	for _, st := range stages {
		p.Stages[st] = storage.BurnReviewStage{}
	}
	p, err := f.st.Burn().SaveReviewProfile(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// A person skipping an item while its result review is still running (slow
// AI call) must win: finish() must not overwrite the skip with "done" once
// the review comes back, and must not merge the item's diff into the run.
func TestBurnFinishDoesNotOverwriteASkipRacingTheReview(t *testing.T) {
	f := setup(t)
	tmp := t.TempDir()
	started := filepath.Join(tmp, "started") // set once finish()'s review call actually starts
	marker := filepath.Join(tmp, "go")
	fakebin.Write(f.bin, []byte(`#!/bin/sh
p=$(cat)
case "$p" in
*"result review"*)
  touch "`+started+`"
  while [ ! -f "`+marker+`" ]; do sleep 0.05; done
  r="VERDICT: AGREE. Đúng phạm vi";;
*) echo "do agent viết" > made-by-agent.txt; sleep 1; r="xong lượt";;
esac
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"'"$r"'","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.Start(ctx)
	ends := time.Now().Add(time.Hour)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", ResultMode: "branch", EndsAt: &ends, State: "stopped", ReviewProfileID: f.profile(t, "result")})
	b, err := f.svc.Begin(ctx, f.project.ID, "admin@x.io")
	if err != nil {
		t.Fatal(err)
	}
	sc := actions.Scope{ProjectID: f.project.ID, ConversationID: b.ConversationID}
	f.svc.Tool(ctx, sc, "burn_add", burn.ToolInput{Title: "Thêm file", Kind: "upgrade"})
	items, _ := f.st.Burn().Items(ctx, b.ID)
	f.svc.Tool(ctx, sc, "burn_pick", burn.ToolInput{Item: items[0].ID})
	it := waitItem(t, f.st, items[0].ID, "doing")
	f.svc.Tool(ctx, sc, "burn_done", burn.ToolInput{Item: it.ID, Summary: "đã thêm"})
	it = waitItem(t, f.st, it.ID, "review")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break // finish() is now genuinely blocked inside the review call
		}
		if time.Now().After(deadline) {
			t.Fatal("finish() never started its result review call")
		}
	}

	skipped := it
	skipped.Status, skipped.Summary = "skipped", "bỏ qua trong lúc đang review"
	if err := f.st.Burn().UpdateItemFrom(ctx, skipped, "review"); err != nil {
		t.Fatalf("skip raced out: %v", err)
	}
	os.WriteFile(marker, nil, 0o644) // let the review answer come back

	time.Sleep(500 * time.Millisecond) // finish() must settle without a further status change
	got := waitItemFor(t, f.st, it.ID, "skipped", 10*time.Second)
	if got.Summary != "bỏ qua trong lúc đang review" {
		t.Fatalf("skip overwritten: %+v", got)
	}
	log := exec.Command("git", "log", "--name-only", b.RunBranch)
	log.Dir = f.dir
	if out, _ := log.CombinedOutput(); strings.Contains(string(out), "made-by-agent") {
		t.Fatalf("skipped item's diff merged into the run anyway: %s", out)
	}
	f.svc.Stop(ctx, f.project.ID)
}

// A person skipping an item while work()'s own checks (checked(), ADR-131)
// are still running must win: work() must not overwrite the skip once the
// checks come back, and must not merge or deliver the item.
func TestBurnWorkDoesNotOverwriteASkipRacingTheChecks(t *testing.T) {
	f := setup(t)
	fakebin.Write(f.bin, []byte(strings.Replace(reviewer, "sleep 1", "sleep 0.2", 1)))
	tmp := t.TempDir()
	started := filepath.Join(tmp, "started") // set once checked()'s verify command actually starts
	marker := filepath.Join(tmp, "go")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.Start(ctx)
	ends := time.Now().Add(time.Hour)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{
		ProjectID: f.project.ID, ModelTier: "fast", ResultMode: "branch", EndsAt: &ends, State: "stopped",
		Verify: `touch "` + started + `"; while [ ! -f "` + marker + `" ]; do sleep 0.05; done`,
	}) // no review profile: a done item goes straight to checked()
	b, err := f.svc.Begin(ctx, f.project.ID, "admin@x.io")
	if err != nil {
		t.Fatal(err)
	}
	sc := actions.Scope{ProjectID: f.project.ID, ConversationID: b.ConversationID}
	f.svc.Tool(ctx, sc, "burn_add", burn.ToolInput{Title: "Thêm file", Kind: "upgrade"})
	items, _ := f.st.Burn().Items(ctx, b.ID)
	f.svc.Tool(ctx, sc, "burn_pick", burn.ToolInput{Item: items[0].ID})
	it := waitItem(t, f.st, items[0].ID, "doing")
	f.svc.Tool(ctx, sc, "burn_done", burn.ToolInput{Item: it.ID, Summary: "đã thêm"})
	if got, _ := f.st.Burn().Item(ctx, it.ID); got.Status != "done" {
		t.Fatalf("expected done (no review configured): %s", got.Status)
	}

	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break // checked()'s verify command is now genuinely blocked
		}
		if time.Now().After(deadline) {
			t.Fatal("checked() never started its verify command")
		}
	}

	skipped, _ := f.st.Burn().Item(ctx, it.ID)
	skipped.Status, skipped.Summary = "skipped", "bỏ qua trong lúc đang kiểm chứng"
	if err := f.st.Burn().UpdateItemFrom(ctx, skipped, "done"); err != nil {
		t.Fatalf("skip raced out: %v", err)
	}
	os.WriteFile(marker, nil, 0o644) // let the verify command finish

	time.Sleep(500 * time.Millisecond) // work() must settle without a further status change
	got := waitItemFor(t, f.st, it.ID, "skipped", 10*time.Second)
	if got.Summary != "bỏ qua trong lúc đang kiểm chứng" {
		t.Fatalf("skip overwritten: %+v", got)
	}
	log := exec.Command("git", "log", "--name-only", b.RunBranch)
	log.Dir = f.dir
	if out, _ := log.CombinedOutput(); strings.Contains(string(out), "made-by-agent") {
		t.Fatalf("skipped item's diff merged into the run anyway: %s", out)
	}
	f.svc.Stop(ctx, f.project.ID)
}

// burn_list gives what the coordination prompt leaves out (ADR-121).
func TestBurnListTool(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", ResultMode: "branch", State: "stopped"})
	b, _ := f.svc.Begin(ctx, f.project.ID, "a")
	f.svc.Stop(ctx, f.project.ID)
	f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "việc mở", Kind: "bug", Detail: "ở a.go:1", Status: "found"})
	f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "việc xong", Kind: "bug", Status: "done", Summary: "đã sửa"})
	f.st.Burn().SetScanned(ctx, b.ID, "08/10 09:00 internal/chat")
	sc := actions.Scope{ProjectID: f.project.ID, ConversationID: b.ConversationID}
	for what, want := range map[string]string{"open": "a.go:1", "closed": "đã sửa", "scanned": "internal/chat"} {
		out, err := f.svc.Tool(ctx, sc, "burn_list", burn.ToolInput{What: what})
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("burn_list(%s) = %q, %v", what, out, err)
		}
	}
	if out, _ := f.svc.Tool(ctx, sc, "burn_list", burn.ToolInput{What: "open"}); strings.Contains(out, "việc xong") {
		t.Errorf("open lists a closed piece: %s", out)
	}
}

// panicOnWorktreeSave panics once UpdateItem is called right after work()
// has saved a piece's worktree and work chat (burn.go: the save right before
// it runs the agent) — the spot a real panic in run/gate/finish would follow.
type panicOnWorktreeSave struct {
	storage.Store
	armed *int32
}

func (w panicOnWorktreeSave) Burn() storage.BurnRepo {
	return panicBurnRepo{w.Store.Burn(), w.armed}
}

type panicBurnRepo struct {
	storage.BurnRepo
	armed *int32
}

func (r panicBurnRepo) UpdateItem(ctx context.Context, it storage.BurnItem) error {
	if it.Status == "doing" && it.Worktree != "" && it.WorkConversationID != "" && atomic.CompareAndSwapInt32(r.armed, 0, 1) {
		_ = r.BurnRepo.UpdateItem(ctx, it) // the real save still happens first, as it would before any later panic
		panic("boom: simulated panic in work/run")
	}
	return r.BurnRepo.UpdateItem(ctx, it)
}

func (r panicBurnRepo) UpdateItemFrom(ctx context.Context, it storage.BurnItem, from string) error {
	if it.Status == "doing" && it.Worktree != "" && it.WorkConversationID != "" && atomic.CompareAndSwapInt32(r.armed, 0, 1) {
		_ = r.BurnRepo.UpdateItemFrom(ctx, it, from) // the real save still happens first, as it would before any later panic
		panic("boom: simulated panic in work/run")
	}
	return r.BurnRepo.UpdateItemFrom(ctx, it, from)
}

// waitWorker waits for a worker of the Burn (ADR-126) at work, not one of seen.
func waitWorker(t *testing.T, st storage.Store, sessionID string, seen map[string]bool) storage.BurnItem {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
		items, _ := st.Burn().Items(context.Background(), sessionID)
		for _, it := range items {
			if it.Kind == "" && it.Status == "doing" && it.WorkConversationID != "" && !seen[it.ID] {
				return it
			}
		}
	}
	t.Fatal("no worker started")
	return storage.BurnItem{}
}

// ADR-130: a scan only records pieces with their brief and ends; workers do
// each piece from found (highest priority first), each landing on the run's
// branch; the code map and the areas looked at are kept. Three scans in a
// row that record nothing, all done: the Burn finishes.
func TestBurnScanRecordsThenWorkersDo(t *testing.T) {
	f := setup(t)
	f.svc.SetFindWork(true)
	fakebin.Write(f.bin, []byte(`#!/bin/sh
cat > /dev/null
echo "do agent viết" > made-by-agent.txt
sleep 3
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"xong lượt","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.Start(ctx)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", MaxParallel: 1, State: "stopped"})
	b, err := f.svc.Begin(ctx, f.project.ID, "admin@x.io")
	if err != nil {
		t.Fatal(err)
	}
	defer f.svc.Stop(context.Background(), f.project.ID)
	seen := map[string]bool{}
	scan := waitWorker(t, f.st, b.ID, seen)
	seen[scan.ID] = true
	if !strings.Contains(scan.Title, "quét") {
		t.Fatalf("scan = %+v", scan)
	}
	ssc := actions.Scope{ProjectID: f.project.ID, ConversationID: scan.WorkConversationID}
	if _, err := f.svc.Tool(ctx, ssc, "burn_done", burn.ToolInput{Item: scan.ID, Summary: "x"}); err == nil {
		t.Fatal("a scan reported work done")
	}
	f.svc.Tool(ctx, ssc, "burn_add", burn.ToolInput{Title: "Sửa chú thích", Kind: "upgrade", Priority: "low", Detail: "a.go:1"})
	if out, err := f.svc.Tool(ctx, ssc, "burn_add", burn.ToolInput{Title: "Thêm file", Kind: "upgrade", Priority: "high", Detail: "Where: a.go"}); err != nil || !strings.Contains(out, "bit_") {
		t.Fatal(out, err)
	}
	if again, _ := f.svc.Tool(ctx, ssc, "burn_add", burn.ToolInput{Title: "thêm  file", Kind: "upgrade"}); !strings.Contains(again, "Đã có") {
		t.Fatalf("the same piece twice: %s", again)
	}
	if _, err := f.svc.Tool(ctx, ssc, "burn_scan_done", burn.ToolInput{Item: scan.ID, Scanned: "internal/a", Map: "a.go: gói a"}); err != nil {
		t.Fatal(err)
	}
	if b, _ = f.st.Burn().Session(ctx, f.project.ID); b.CodeMap != "a.go: gói a" || !strings.Contains(b.Scanned, "internal/a") {
		t.Fatalf("map %q, scanned %q", b.CodeMap, b.Scanned)
	}
	// each by a worker of its own, one at a time (one slot); the scan runs
	// beside them, so the first may be taken before the scan ends
	done := map[string]bool{}
	for range 2 {
		var it storage.BurnItem
		for deadline := time.Now().Add(15 * time.Second); it.ID == "" && time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
			items, _ := f.st.Burn().Items(ctx, b.ID)
			doing := 0
			for _, x := range items {
				if x.Status == "doing" && x.Kind != "" {
					doing++
					if x.WorkConversationID != "" && !done[x.Title] {
						it = x
					}
				}
			}
			if doing > 1 {
				t.Fatalf("%d pieces at once with one slot", doing)
			}
		}
		if it.ID == "" {
			t.Fatalf("no piece taken after %v", done)
		}
		done[it.Title] = true
		f.svc.Tool(ctx, actions.Scope{ProjectID: f.project.ID, ConversationID: it.WorkConversationID}, "burn_done", burn.ToolInput{Item: it.ID, Summary: "xong"})
		waitDelivered(t, f.st, it.ID)
	}
	if !done["Thêm file"] || !done["Sửa chú thích"] {
		t.Fatalf("done %v", done)
	}
	log := exec.Command("git", "log", "--format=%s", "main.."+b.RunBranch)
	log.Dir = f.dir
	if got, _ := log.CombinedOutput(); !strings.Contains(string(got), "burn: Thêm file") && !strings.Contains(string(got), "burn: Sửa chú thích") { // the first done brings the file
		t.Fatalf("run branch log:\n%s", got)
	}
	// scans that find nothing (they run beside the work, so some may be over
	// already): three in a row, all done, it finishes
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if cur, _ := f.st.Burn().Session(ctx, f.project.ID); cur.State == "stopped" {
			break
		}
		items, _ := f.st.Burn().Items(ctx, b.ID)
		for _, n := range items {
			if n.Kind == "" && n.Status == "doing" && n.WorkConversationID != "" && !seen[n.ID] {
				seen[n.ID] = true
				f.svc.Tool(ctx, actions.Scope{ProjectID: f.project.ID, ConversationID: n.WorkConversationID}, "burn_scan_done", burn.ToolInput{Item: n.ID, Scanned: "x", Reason: "hết việc"})
			}
		}
		if time.Now().After(deadline) {
			cur, _ := f.st.Burn().Session(ctx, f.project.ID)
			t.Fatalf("still %s after three scans found nothing", cur.State)
		}
	}
}

// A quest the person gives waits in found, with no run until it starts; a
// running Burn takes it at once, even with no worker looking for work, and
// it lands in the run it was done in.
func TestBurnDoesAQuest(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ends := time.Now().Add(time.Hour)
	b, _ := f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", MaxParallel: 1, EndsAt: &ends, State: "stopped"})
	if _, err := f.svc.AddQuest(ctx, b, "  ", ""); err == nil {
		t.Fatal("a quest with no title")
	}
	q, err := f.svc.AddQuest(ctx, b, "Thêm nút  xuất CSV", "trang đơn hàng")
	if err != nil || q.Kind != burn.KindQuest || q.Status != "found" || q.RunBranch != "" || q.Title != "Thêm nút xuất CSV" {
		t.Fatalf("quest = %+v, %v", q, err)
	}
	f.svc.Start(ctx)
	if b, err = f.svc.Begin(ctx, f.project.ID, "admin@x.io"); err != nil {
		t.Fatal(err)
	}
	waitItem(t, f.st, q.ID, "doing")
	sc := actions.Scope{ProjectID: f.project.ID, ConversationID: b.ConversationID}
	f.svc.Tool(ctx, sc, "burn_done", burn.ToolInput{Item: q.ID, Summary: "đã thêm"})
	if q = waitDelivered(t, f.st, q.ID); q.RunBranch != b.RunBranch || q.Kind != burn.KindQuest {
		t.Fatalf("quest done = %+v, run %s", q, b.RunBranch)
	}
}

// At its time a scheduled Burn turns on by itself, stopping by its plan;
// turned off by hand, the same slot does not turn it on again.
func TestBurnTurnsOnBySchedule(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	at := now.Add(-time.Minute).Format("15:04")
	plan := storage.BurnPlan{End: burn.EndHours, Hours: 2, Auto: true, At: at, Days: []int{int(now.Add(-time.Minute).Weekday())}}
	if _, err := f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", MaxParallel: 1, State: "stopped", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	f.svc.StartDue(ctx, now)
	b, _ := f.st.Burn().Session(ctx, f.project.ID)
	if b.State != "running" || b.StartedBy != burn.SchedSource || b.EndsAt == nil || b.EndsAt.Sub(now) < 119*time.Minute || b.EndsAt.Sub(now) > 121*time.Minute {
		t.Fatalf("not turned on by its plan: %s %s %v", b.State, b.StartedBy, b.EndsAt)
	}
	if err := f.svc.Stop(ctx, f.project.ID); err != nil {
		t.Fatal(err)
	}
	f.svc.StartDue(ctx, now.Add(time.Minute))
	if b, _ := f.st.Burn().Session(ctx, f.project.ID); b.State != "stopped" {
		t.Fatalf("turned on again after being turned off: %s", b.State)
	}
}

// While an update waits for office to be idle (ADR-255), a Burn takes no new
// piece; once the hold lets go it does.
func TestBurnHoldTakesNoNewPiece(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var held atomic.Bool
	held.Store(true)
	f.svc.SetHold(held.Load)
	f.svc.SetFindWork(false)
	f.svc.Start(ctx)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", ResultMode: "branch", State: "stopped"})
	b, err := f.svc.Begin(ctx, f.project.ID, "admin@x.io")
	if err != nil {
		t.Fatal(err)
	}
	it, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "việc mới", Kind: "unfinished", Status: "queued"})
	f.svc.Wake(f.project.ID)
	time.Sleep(300 * time.Millisecond)
	if cur, _ := f.st.Burn().Item(ctx, it.ID); cur.Status != "queued" {
		t.Fatalf("a piece started while held: %s", cur.Status)
	}
	held.Store(false)
	f.svc.Wake(f.project.ID)
	waitItem(t, f.st, it.ID, "doing")
	f.svc.Stop(ctx, f.project.ID)
}
