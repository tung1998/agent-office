package chat_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/actor"
	"github.com/tung1998/agent-office/internal/chat"
	"github.com/tung1998/agent-office/internal/mcpserver"
	"github.com/tung1998/agent-office/internal/officetools"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/provider"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/worktree"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// fakeAnthropic answers each turn with one tool call, then a short text.
func fakeAnthropic(t *testing.T, wantSystem string, tools []map[string]any) *httptest.Server {
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			System string           `json:"system"`
			Tools  []map[string]any `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		turn, second := calls/2, calls%2 == 1
		calls++
		if !second {
			names := ""
			for _, x := range body.Tools {
				names += x["name"].(string) + " "
			}
			if !strings.Contains(names, "edit_file") || !strings.Contains(body.System, wantSystem) {
				t.Errorf("turn %d: tools %q / system without %q", turn, names, wantSystem)
			}
			out, _ := json.Marshal(map[string]any{"model": "m", "stop_reason": "tool_use", "usage": map[string]int{"input_tokens": 10, "output_tokens": 5},
				"content": []map[string]any{{"type": "tool_use", "id": "tu", "name": tools[turn]["name"], "input": tools[turn]["input"]}}})
			w.Write(out)
			return
		}
		w.Write([]byte(`{"model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"Xong."}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
}

func TestWorktreeChatEditsThenMerge(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := fakeAnthropic(t, "worktree riêng", []map[string]any{
		{"name": "edit_file", "input": map[string]any{"path": "hello.txt", "old": "hello", "new": "xin chào"}},
		{"name": "write_file", "input": map[string]any{"path": "extra.txt", "content": "sai\n"}},
	})
	defer srv.Close()
	f := setup(t, func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, _ := provs.Create(context.Background(), provider.Input{Name: "C", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})
		return p
	})
	gitRun(t, f.dir, "init", "-q")
	gitRun(t, f.dir, "add", "-A")
	gitRun(t, f.dir, "commit", "-q", "-m", "init")
	trees := worktree.New(filepath.Join(t.TempDir(), "worktrees"))
	f.engine.SetWorktrees(trees)
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")

	// 1: the agent edits its worktree; the project is untouched until merged
	turn, _, err := f.engine.Send(ctx, conv.ID, "đổi lời chào", nil)
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, turn)
	done := evs[len(evs)-1].Message
	if done == nil || len(done.Patches) != 1 || done.Patches[0].Status != "pending" || strings.Join(done.Patches[0].Files, ",") != "hello.txt" {
		t.Fatalf("patches = %+v (events %+v)", done, evs)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "hello.txt")); string(b) != "hello\nworld\n" {
		t.Fatalf("project changed before merge: %q", b)
	}
	tree := trees.Path(f.project.ID, chat.ChatTree(conv.ID))
	if b, _ := os.ReadFile(filepath.Join(tree, "hello.txt")); string(b) != "xin chào\nworld\n" {
		t.Fatalf("worktree file = %q", b)
	}
	p, err := f.engine.DecidePatch(ctx, done.Patches[0].ID, true)
	if err != nil || p.Status != "applied" {
		t.Fatalf("merge = %+v %v", p, err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "hello.txt")); string(b) != "xin chào\nworld\n" {
		t.Fatalf("project after merge = %q", b)
	}
	if left, _ := worktree.Changed(context.Background(), tree); len(left) != 0 {
		t.Fatalf("still pending in worktree after merge: %v", left)
	}

	// 2: a new change is only what came after the merge; rejecting drops it
	turn, _, _ = f.engine.Send(ctx, conv.ID, "thêm file", nil)
	evs = collect(t, turn)
	done = evs[len(evs)-1].Message
	if len(done.Patches) != 1 || strings.Join(done.Patches[0].Files, ",") != "extra.txt" {
		t.Fatalf("second patch = %+v", done.Patches)
	}
	if _, err := f.engine.DecidePatch(ctx, done.Patches[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tree, "extra.txt")); !os.IsNotExist(err) {
		t.Fatal("rejected file still in worktree")
	}

	// deleting the conversation removes its worktree
	if err := f.engine.DeleteConversation(ctx, conv.ID); err != nil || trees.Exists(f.project.ID, chat.ChatTree(conv.ID)) {
		t.Fatalf("delete: %v, exists %v", err, trees.Exists(f.project.ID, chat.ChatTree(conv.ID)))
	}
}

func TestDirectModeEditsProject(t *testing.T) {
	srv := fakeAnthropic(t, "trong thư mục project của người dùng", []map[string]any{{"name": "edit_file", "input": map[string]any{"path": "hello.txt", "old": "hello", "new": "chào"}}})
	defer srv.Close()
	f := setup(t, func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, _ := provs.Create(context.Background(), provider.Input{Name: "C", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})
		return p
	})
	f.engine.SetWorktrees(worktree.New(t.TempDir()))
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	if err := f.engine.SetEditMode(ctx, conv.ID, perm.EditDirect); err != nil {
		t.Fatal(err)
	}
	turn, _, err := f.engine.Send(ctx, conv.ID, "đổi", nil)
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(t, turn)
	if done := evs[len(evs)-1].Message; done == nil || len(done.Patches) != 0 {
		t.Fatalf("direct mode made patches: %+v", evs[len(evs)-1])
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "hello.txt")); string(b) != "chào\nworld\n" {
		t.Fatalf("project = %q", b)
	}
}

// ADR-044 review C1/I1: two agents of one chat edit in their own worktrees;
// each diff is accepted in its own tree, and one agent's diff never replaces
// or reverts the other's.
func TestTwoAgentsEditInTheirOwnTrees(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	os.WriteFile(bin, []byte(`#!/bin/sh
cat >/dev/null
who=lead
case "$*" in *"Bạn là Dev"*) who=dev;; esac
printf '%s\n' "$who" > "$who.txt"
echo '{"type":"system","subtype":"init","session_id":"sess-'$who'"}'
echo '{"type":"result","subtype":"success","is_error":false,"result":"'$who' sửa xong","session_id":"sess-'$who'","usage":{"input_tokens":1,"output_tokens":1}}'
`), 0o755)
	f := setup(t, func(provs *provider.Service) storage.Provider {
		p, _ := provs.Create(context.Background(), provider.Input{Name: "CC", Kind: storage.ProviderClaudeCLI, BaseURL: bin})
		return p
	})
	gitRun(t, f.dir, "init", "-q")
	gitRun(t, f.dir, "add", "-A")
	gitRun(t, f.dir, "commit", "-q", "-m", "init")
	f.engine.SetWorktrees(worktree.New(filepath.Join(t.TempDir(), "worktrees")))
	ctx := actor.With(context.Background(), "human:a@b.c")
	m, _ := f.st.OrgModels().GetForRepo(ctx, f.project.ID)
	for _, a := range mustAgents(t, f) { // both may edit, and a person approves every diff
		a.Permissions = storage.Permissions{Level: perm.Edit, Caps: &[]string{perm.CapPropose}}
		f.st.Agents().Update(ctx, a)
	}
	caps := []string{perm.CapPropose}
	dev, err := f.st.Agents().Create(ctx, storage.Agent{OrgModelID: m.ID, Key: "dev", Name: "Dev", Tier: storage.TierWorker, ModelTier: "fast",
		Permissions: storage.Permissions{Level: perm.Edit, Caps: &caps}})
	if err != nil {
		t.Fatal(err)
	}
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	f.engine.SetMode(ctx, conv.ID, perm.Operate)
	say := func(text string) *chat.MessageDTO {
		t.Helper()
		turn, _, err := f.engine.Send(ctx, conv.ID, text, nil)
		if err != nil {
			t.Fatal(err)
		}
		evs := collect(t, turn)
		return evs[len(evs)-1].Message
	}
	leadMsg := say("sửa đi")
	devMsg := say("@Dev sửa phần của bạn")
	if len(leadMsg.Patches) != 1 || len(devMsg.Patches) != 1 || strings.Join(devMsg.Patches[0].Files, ",") != "dev.txt" {
		t.Fatalf("patches: lead %+v dev %+v", leadMsg.Patches, devMsg.Patches)
	}
	patches, _ := f.st.Chat().ListPatches(ctx, conv.ID)
	for _, p := range patches {
		if p.Status != "pending" {
			t.Fatalf("one agent's diff replaced the other's: %+v", patches)
		}
	}
	if _, err := f.engine.DecidePatch(ctx, devMsg.Patches[0].ID, true); err != nil {
		t.Fatal(err)
	}
	// the lead again: its worktree knows nothing of Dev's change and must not revert it
	again := say("còn gì nữa không")
	for _, p := range again.Patches {
		if strings.Contains(p.Diff, "dev.txt") {
			t.Fatalf("the lead's diff touches Dev's file:\n%s", p.Diff)
		}
	}
	if b, err := os.ReadFile(filepath.Join(f.dir, "dev.txt")); err != nil || string(b) != "dev\n" {
		t.Fatalf("dev.txt in the project = %q %v", b, err)
	}
	_ = dev
}

func mustAgents(t *testing.T, f fixture) []storage.Agent {
	t.Helper()
	agents, err := f.engine.Agents(context.Background(), f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return agents
}

// ADR-046: the assistant's chat runs in office scope (its tools span projects);
// a project's chat does not.
func TestAssistantChatGetsOfficeScope(t *testing.T) {
	var names []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []map[string]any `json:"tools"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		names = names[:0]
		for _, x := range body.Tools {
			names = append(names, x["name"].(string))
		}
		w.Write([]byte(`{"model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	f := setup(t, func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, _ := provs.Create(context.Background(), provider.Input{Name: "C", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})
		return p
	})
	ctx := actor.With(context.Background(), "human:a@b.c")
	box := officetools.New(f.st, nil, actions.New(f.st, nil))
	f.engine.SetOffice(box, mcpserver.New(box, "test"), "http://127.0.0.1:1/mcp")
	f.engine.SetAssistant(func(context.Context) string { return f.project.ID })
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	turn, _, err := f.engine.Send(ctx, conv.ID, "tuần này tốn bao nhiêu", nil)
	if err != nil {
		t.Fatal(err)
	}
	collect(t, turn)
	if !slices.Contains(names, "projects") || !slices.Contains(names, "usage_summary") {
		t.Fatalf("assistant tools = %v", names)
	}
	f.engine.SetAssistant(func(context.Context) string { return "" })
	turn, _, _ = f.engine.Send(ctx, conv.ID, "lại", nil)
	collect(t, turn)
	if slices.Contains(names, "projects") {
		t.Fatalf("a project's chat got office tools: %v", names)
	}
}

// The project moved on after the chat's worktree was made: merging refreshes
// the worktree onto it first; a clash is left in the worktree for the agent.
func TestMergeAfterProjectMoved(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	srv := fakeAnthropic(t, "worktree riêng", []map[string]any{
		{"name": "edit_file", "input": map[string]any{"path": "hello.txt", "old": "hello", "new": "xin chào"}},
		{"name": "edit_file", "input": map[string]any{"path": "hello.txt", "old": "thế giới", "new": "bạn"}},
		{"name": "write_file", "input": map[string]any{"path": "note.txt", "content": "ghi chú\n"}},
	})
	defer srv.Close()
	f := setup(t, func(provs *provider.Service) storage.Provider {
		key := "sk-ant-test-key-0000"
		p, _ := provs.Create(context.Background(), provider.Input{Name: "C", Kind: storage.ProviderAnthropic, BaseURL: srv.URL, APIKey: &key})
		return p
	})
	os.WriteFile(filepath.Join(f.dir, "hello.txt"), []byte("hello\n1\n2\n3\nworld\n"), 0o644)
	gitRun(t, f.dir, "init", "-q")
	gitRun(t, f.dir, "add", "-A")
	gitRun(t, f.dir, "commit", "-q", "-m", "init")
	trees := worktree.New(filepath.Join(t.TempDir(), "worktrees"))
	f.engine.SetWorktrees(trees)
	ctx := actor.With(context.Background(), "human:a@b.c")
	conv, _ := f.engine.StartConversation(ctx, f.project.ID, "")
	turn, _, _ := f.engine.Send(ctx, conv.ID, "đổi lời chào", nil)
	evs := collect(t, turn)
	pid := evs[len(evs)-1].Message.Patches[0].ID
	// meanwhile someone changes another line of the project and commits it
	os.WriteFile(filepath.Join(f.dir, "hello.txt"), []byte("hello\n1\n2\n3\nthế giới\n"), 0o644)
	gitRun(t, f.dir, "commit", "-q", "-am", "moved")
	p, err := f.engine.DecidePatch(ctx, pid, true)
	if err != nil || p.Status != "applied" {
		t.Fatalf("merge after the project moved = %+v %v", p, err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "hello.txt")); string(b) != "xin chào\n1\n2\n3\nthế giới\n" {
		t.Fatalf("project = %q", b)
	}
	// a clash: the same line changed on both sides
	turn, _, _ = f.engine.Send(ctx, conv.ID, "đổi tiếp", nil)
	evs = collect(t, turn)
	if len(evs[len(evs)-1].Message.Patches) == 0 { // the worktree followed the project: "thế giới" is there to edit
		b, _ := os.ReadFile(filepath.Join(trees.Path(f.project.ID, chat.ChatTree(conv.ID)), "hello.txt"))
		t.Fatalf("no second patch: %+v worktree=%q", evs[len(evs)-1].Message, b)
	}
	pid = evs[len(evs)-1].Message.Patches[0].ID
	os.WriteFile(filepath.Join(f.dir, "hello.txt"), []byte("xin chào\n1\n2\n3\nworld again\n"), 0o644)
	gitRun(t, f.dir, "commit", "-q", "-am", "clash")
	p, err = f.engine.DecidePatch(ctx, pid, true)
	if err != nil || p.Status != "failed" || !strings.Contains(p.Detail, "xung đột") || !strings.Contains(p.Detail, "hello.txt") {
		t.Fatalf("clash = %+v %v", p, err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "hello.txt")); string(b) != "xin chào\n1\n2\n3\nworld again\n" {
		t.Fatalf("the project was touched by a clash: %q", b)
	}
	// the agent does something else without settling the clash: nothing goes to merge
	turn, _, _ = f.engine.Send(ctx, conv.ID, "ghi chú", nil)
	evs = collect(t, turn)
	for _, pt := range evs[len(evs)-1].Message.Patches {
		if pt.Status == "pending" || pt.Status == "applied" {
			t.Fatalf("a diff with conflict markers went to merge: %+v", pt)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "hello.txt")); strings.Contains(string(b), "<<<<<<<") {
		t.Fatalf("conflict markers reached the project: %q", b)
	}
}
