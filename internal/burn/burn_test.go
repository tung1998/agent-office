package burn_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/burn"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/llm"
	"github.com/tung1998/agent-office/internal/orgmodel"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/secrets"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/storage/sqlite"
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
}

// setup: a git project, its lead, a Claude Code that writes a file where it
// runs (the item's worktree) and takes a moment.
func setup(t *testing.T) fx {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	st, _ := sqlite.Open(filepath.Join(tmp, "o.db"))
	t.Cleanup(func() { st.Close() })
	st.Migrate(ctx)
	box, _ := secrets.Load(filepath.Join(tmp, "k"))
	provs := provider.NewService(st, box, llm.Options{})
	u := usage.New(st, time.UTC)
	provs.SetUsage(u)
	org := orgmodel.NewService(st)
	org.SeedBuiltins(ctx)
	bin := filepath.Join(tmp, "claude")
	os.WriteFile(bin, []byte(`#!/bin/sh
cat > /dev/null
echo "do agent viết" > made-by-agent.txt
sleep 1
echo '{"type":"system","subtype":"init","session_id":"s1"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"xong lượt","session_id":"s1","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
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
	solo, _ := st.OrgModels().GetTemplateByKey(ctx, "solo")
	org.ApplyToRepo(ctx, project.ID, solo.ID, false)
	engine := chat.NewEngine(st, provs, u)
	trees := worktree.New(filepath.Join(tmp, "trees"))
	engine.SetWorktrees(trees)
	svc := burn.New(st, engine, trees)
	svc.SetIdle(50 * time.Millisecond)
	return fx{st: st, svc: svc, engine: engine, trees: trees, project: project, dir: dir}
}

func waitItem(t *testing.T, st storage.Store, id, status string) storage.BurnItem {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
		if it, err := st.Burn().Item(context.Background(), id); err == nil && it.Status == status {
			return it
		}
	}
	it, _ := st.Burn().Item(context.Background(), id)
	t.Fatalf("item %s: %s, want %s", id, it.Status, status)
	return it
}

// A chosen piece runs in its own worktree; reported done, what it changed is
// committed to its burn/ branch of the project (nothing pushed, nothing on main).
func TestBurnDoesAPieceOnItsBranch(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.Start(ctx)
	ends := time.Now().Add(time.Hour)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", MaxSubagents: 1, ResultMode: "branch", EndsAt: &ends, State: "stopped"})
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
	it = waitItem(t, f.st, it.ID, "done")
	if it.Status != "done" || !strings.HasPrefix(it.Branch, "burn/") {
		t.Fatalf("item = %+v", it)
	}
	var got []byte
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		show := exec.Command("git", "show", "--stat", it.Branch)
		show.Dir = f.dir
		got, _ = show.CombinedOutput()
		if strings.Contains(string(got), "made-by-agent.txt") {
			break
		}
	}
	if !strings.Contains(string(got), "made-by-agent.txt") || !strings.Contains(string(got), "burn: Sửa lỗi thanh toán") {
		t.Fatalf("branch %s: %s", it.Branch, got)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "made-by-agent.txt")); err == nil {
		t.Fatal("the project's own folder was changed")
	}
	if err := f.svc.Stop(ctx, f.project.ID); err != nil {
		t.Fatal(err)
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

// The real case (2026-10-05): its agent changed (the old one paused), a Burn
// started again talks with the new one, in a chat of its own.
func TestBurnFollowsItsAgent(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	agents, _ := f.engine.Agents(ctx, f.project.ID)
	old := agents[0]
	other, err := f.st.Agents().Create(ctx, storage.Agent{OrgModelID: old.OrgModelID, Name: "Thay", Tier: old.Tier, ModelTier: old.ModelTier, Instructions: "x"})
	if err != nil {
		t.Fatal(err)
	}
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, AgentID: old.ID, ModelTier: "fast", ResultMode: "branch", State: "stopped"})
	b, err := f.svc.Begin(ctx, f.project.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	first := b.ConversationID
	f.svc.Stop(ctx, f.project.ID)
	b.AgentID, b.State = other.ID, "stopped"
	f.st.Burn().SaveSession(ctx, b)
	if b, err = f.svc.Begin(ctx, f.project.ID, "a"); err != nil {
		t.Fatal(err)
	}
	c, _ := f.st.Chat().GetConversation(ctx, b.ConversationID)
	if b.ConversationID == first || c.AgentID != other.ID {
		t.Fatalf("conversation %s (first %s) of %s, want %s", b.ConversationID, first, c.AgentID, other.ID)
	}
	if again, _ := f.svc.Begin(ctx, f.project.ID, "a"); again.ConversationID != b.ConversationID {
		t.Fatal("same agent, a new chat each start")
	}
	f.svc.Stop(ctx, f.project.ID)
	f.st.Agents().SetEnabled(ctx, other.ID, false) // paused: refused at once, with what to do
	if _, err := f.svc.Begin(ctx, f.project.ID, "a"); err == nil || !strings.Contains(err.Error(), "đang tắt") {
		t.Fatalf("a paused agent started: %v", err)
	}
}

// Its time up, a Burn stops; the tools are its conversation's only.
func TestBurnEndsAndToolsAreItsOwn(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	past := time.Now().Add(-time.Minute)
	f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ResultMode: "branch", EndsAt: &past, State: "stopped"})
	if _, err := f.svc.Begin(ctx, f.project.ID, "a"); err == nil {
		t.Fatal("started past its stop time")
	}
	if _, err := f.svc.Tool(ctx, actions.Scope{ProjectID: f.project.ID, ConversationID: "cnv_other"}, "burn_add", burn.ToolInput{Title: "x"}); err == nil {
		t.Fatal("a burn tool worked outside its conversation")
	}
}

// The real case (2026-10-01): a Burn whose agent may only read in its mode
// still runs with full access, so it writes. It must write in its own
// worktree, never in the project's folder.
func TestBurnReadOnlyAgentStillWorksInItsWorktree(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, _ := f.st.OrgModels().GetForRepo(ctx, f.project.ID)
	agents, _ := f.st.Agents().List(ctx, m.ID)
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
	if strings.Contains(it.Summary, "không commit được") {
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

// Office's start sweeps worktrees idle for 14 days by their folder's time,
// which does not move when only files inside change: a piece in progress
// (paused, or doing) keeps its worktree however old; a piece that is over,
// a scan of a session that is gone, an orphan go.
func TestSweepOldKeepsWorkInProgress(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	b, _ := f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ResultMode: "branch", State: "stopped"})
	paused, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "dở", Kind: "bug", Status: "paused"})
	doing, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "đang làm", Kind: "bug", Status: "doing"})
	done, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "xong", Kind: "bug", Status: "done"})
	want := map[string]bool{"burn-" + paused.ID: true, "burn-" + doing.ID: true, "burn-" + done.ID: false,
		"burn-scan-bse_gone": false, "burn-bit_gone": false}
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

// Office restarted while a piece ran: started again, the piece waits (its
// worktree and what it did there kept), then goes on first.
func TestBurnGoesOnAfterRestart(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, _ := f.st.Burn().SaveSession(ctx, storage.BurnSession{ProjectID: f.project.ID, ModelTier: "fast", ResultMode: "branch", State: "stopped"})
	b, err := f.svc.Begin(ctx, f.project.ID, "admin@x.io") // its conversation; no loop yet (not started)
	if err != nil {
		t.Fatal(err)
	}
	it, _ := f.st.Burn().AddItem(ctx, storage.BurnItem{SessionID: b.ID, Title: "đang làm khi tắt", Kind: "bug", Status: "doing", Attempts: 1})
	dir, err := f.trees.Ensure(ctx, f.dir, f.project.ID, "burn-"+it.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "half-done.txt"), []byte("dở\n"), 0o644)
	it.Worktree = dir
	f.st.Burn().UpdateItem(ctx, it)
	// as office starts again: the sweep, then Burn
	f.engine.SweepWorktrees(ctx, 14*24*time.Hour)
	f.svc.Start(ctx)
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(30 * time.Millisecond) {
		if it, _ = f.st.Burn().Item(ctx, it.ID); it.Status == "doing" && it.Attempts == 2 {
			break // its turn again, after the restart
		}
	}
	if it.Status != "doing" || it.Attempts != 2 {
		t.Fatalf("after restart = %s, attempts %d", it.Status, it.Attempts)
	}
	if it.Worktree != dir {
		t.Fatalf("worktree = %s, want %s", it.Worktree, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "half-done.txt")); err != nil {
		t.Fatalf("what it did is gone: %v", err)
	}
	f.svc.Stop(ctx, f.project.ID)
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
