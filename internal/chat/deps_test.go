package chat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/deciding"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
	"github.com/tung1998/agent-office/internal/workflow"
)

const depDiff = `diff --git a/web/package.json b/web/package.json
--- a/web/package.json
+++ b/web/package.json
@@ -1 +1 @@
-{"dependencies":{"nuxt":"4.5.2"}}
+{"dependencies":{"nuxt":"4.6.4"}}
diff --git a/web/pnpm-lock.yaml b/web/pnpm-lock.yaml
--- a/web/pnpm-lock.yaml
+++ b/web/pnpm-lock.yaml
@@ -1 +1 @@
-nuxt: 4.5.2
+nuxt: 4.6.4
`

// depsChat is a project with web/ on Nuxt 4.5.2, a chat, and a pending diff
// that bumps it (depDiff).
func depsChat(t *testing.T) (*Engine, storage.Store, storage.Repo, storage.Conversation, storage.Patch) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	st, p, own := mergeStore(t)
	ctx := context.Background()
	os.MkdirAll(filepath.Join(p.Path, "web"), 0o755)
	os.WriteFile(filepath.Join(p.Path, "web", "package.json"), []byte(`{"dependencies":{"nuxt":"4.5.2"}}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(p.Path, "web", "pnpm-lock.yaml"), []byte("nuxt: 4.5.2\n"), 0o644)
	e := &Engine{store: st, acts: actions.New(st, nil)}
	msg, _ := st.Chat().AddMessage(ctx, storage.Message{ConversationID: own.ID, Role: "assistant", Content: "done"})
	pat, err := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, MessageID: msg.ID, Diff: depDiff, Files: []string{"web/package.json", "web/pnpm-lock.yaml"}, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	return e, st, p, own, pat
}

// The project installs merged dependency changes on its own (auto_deps):
// approving the diff does not wait for the install, which runs in the
// background, holds a workflow check step until it ends, and fills the
// project's node_modules.
func TestAutoDepsInstallsInBackground(t *testing.T) {
	bin := t.TempDir()
	release := filepath.Join(bin, "go")
	os.WriteFile(filepath.Join(bin, "pnpm"), []byte("#!/bin/sh\nwhile [ ! -e "+release+" ]; do sleep 0.02; done\nmkdir -p node_modules/nuxt && echo 4.6.4 > node_modules/nuxt/v\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e, st, p, own, pat := depsChat(t)
	ctx := context.Background()
	pol := perm.LoadPolicy(ctx, st, p.ID)
	pol.AutoDeps = true
	if err := perm.SavePolicy(ctx, st, p.ID, pol); err != nil {
		t.Fatal(err)
	}
	ended := make(chan storage.Action, 1)
	depsInstalled = func(a storage.Action) { ended <- a }
	defer func() { depsInstalled = nil }()

	d, err := e.DecidePatch(ctx, pat.ID, true) // returns while pnpm still runs
	if err != nil || d.Status != "applied" {
		t.Fatalf("merge = %s %v", d.Status, err)
	}
	if work, _ := e.unmergedWork(ctx, own); len(work) != 1 || !strings.Contains(work[0], "web (pnpm)") {
		t.Fatalf("a check step does not wait for the install: %v", work)
	}
	os.WriteFile(release, nil, 0o644)
	select {
	case a := <-ended:
		if a.Status != "done" || !strings.HasPrefix(a.DecidedBy, "auto:office") {
			t.Fatalf("install = %s %q %q", a.Status, a.DecidedBy, a.Detail)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the background install never ended")
	}
	if b, _ := os.ReadFile(filepath.Join(p.Path, "web", "node_modules", "nuxt", "v")); strings.TrimSpace(string(b)) != "4.6.4" {
		t.Fatalf("project's node_modules = %q", b)
	}
	if work, _ := e.unmergedWork(ctx, own); len(work) != 0 {
		t.Fatalf("still waiting after the install: %v", work)
	}
}

// The project does not install on its own and no one decides the merged
// diff's install card: a check step does not wait the 2 hours a diff gets,
// it runs on the old libraries after depsWaitMax, saying so and how to have
// office install on its own. An install that is running is waited for.
func TestWaitMergedDepsOnlyShortCap(t *testing.T) {
	e, st, _, own, pat := depsChat(t)
	ctx := context.Background()
	oldPoll, oldDeps := mergeWaitPoll, depsWaitMax
	mergeWaitPoll, depsWaitMax = 5*time.Millisecond, 150*time.Millisecond
	defer func() { mergeWaitPoll, depsWaitMax = oldPoll, oldDeps }()
	if d, err := e.DecidePatch(ctx, pat.ID, true); err != nil || d.Status != "applied" {
		t.Fatalf("merge = %s %v", d.Status, err)
	}
	acts, _ := st.Actions().List(ctx, own.ID, "", "")
	if len(acts) != 1 || acts[0].Status != "pending" {
		t.Fatalf("install card = %+v", acts)
	}
	step := workflow.Step{ID: "kiem", Type: workflow.StepCheck}

	// being installed: waited for (no cap)
	if !deciding.Begin("actions", acts[0].ID, "auto:office") {
		t.Fatal("begin")
	}
	cctx, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	err := e.waitMerged(cctx, own, step)
	cancel()
	deciding.End("actions", acts[0].ID)
	if err == nil {
		t.Fatal("a running install was not waited for")
	}

	// waiting for a person: the step goes on after the short cap
	done := make(chan error, 1)
	go func() { done <- e.waitMerged(ctx, own, step) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the step waits for an install card no one decides")
	}
	if countNotes(t, st, own.ID, "Tự cài thư viện") < 2 || countNotes(t, st, own.ID, "thư viện cũ") < 1 {
		t.Fatal("no note of the old libraries and of the auto install switch")
	}
}

// A merged diff whose install card could not be made (its package folder
// gone from the project) was only logged: the chat said nothing, the project
// kept its old libraries, and "Cài lại" answered "no dependency changed".
func TestProposeDepsFailureTold(t *testing.T) {
	e, st, p, own, pat := depsChat(t)
	ctx := context.Background()
	pat.Files = []string{"web/pnpm-lock.yaml", "gone/pnpm-lock.yaml"}
	pat.Diff = ""
	acts, err := e.proposeDeps(ctx, p, pat)
	if len(acts) != 1 || acts[0].Target != "web (pnpm)" {
		t.Fatalf("cards = %+v: the other package still gets its card", acts)
	}
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("err = %v, want why gone's card failed", err)
	}
	if countNotes(t, st, own.ID, "Không tạo được thẻ cài thư viện cho gone") != 1 {
		t.Fatal("the chat is not told")
	}
	// none could be made: "Cài lại" says why, not ErrNoDeps
	only, err := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, MessageID: pat.MessageID, Files: []string{"gone/pnpm-lock.yaml"}, Status: "applied"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.InstallDeps(ctx, only.ID); err == nil || err == ErrNoDeps || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("install again = %v", err)
	}
}

// A merged diff that only changes a package.json's scripts installs
// nothing: no card on the diff, no install card, nothing for a check to wait on.
func TestProposeDepsSkipsScriptsOnlyDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	st, p, own := mergeStore(t)
	ctx := context.Background()
	os.MkdirAll(filepath.Join(p.Path, "web"), 0o755)
	os.WriteFile(filepath.Join(p.Path, "web", "package.json"), []byte("{\n  \"scripts\": {\n    \"dev\": \"nuxt dev\"\n  },\n  \"dependencies\": {\n    \"nuxt\": \"4.5.2\"\n  }\n}\n"), 0o644)
	e := &Engine{store: st, acts: actions.New(st, nil)}
	msg, _ := st.Chat().AddMessage(ctx, storage.Message{ConversationID: own.ID, Role: "assistant", Content: "done"})
	diff := "diff --git a/web/package.json b/web/package.json\n--- a/web/package.json\n+++ b/web/package.json\n@@ -1,4 +1,4 @@\n {\n   \"scripts\": {\n-    \"dev\": \"nuxt dev\"\n+    \"dev\": \"nuxt dev --port 3001\"\n   },\n"
	pat, err := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, MessageID: msg.ID, Diff: diff, Files: []string{"web/package.json"}, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if dto := FullPatchDTO(pat); len(dto.Deps) != 0 {
		t.Fatalf("card deps of a scripts-only diff = %+v", dto.Deps)
	}
	d, err := e.DecidePatch(ctx, pat.ID, true)
	if err != nil || d.Status != "applied" {
		t.Fatalf("merge = %s %q %v", d.Status, d.Detail, err)
	}
	if acts, _ := st.Actions().List(ctx, own.ID, "", ""); len(acts) != 0 {
		t.Fatalf("a scripts-only diff made install cards: %+v", acts)
	}
	if work, _ := e.unmergedWork(ctx, own); len(work) != 0 {
		t.Fatalf("a check would wait for: %v", work)
	}
}

// The diff card reads a package.json against the project's file, as the
// merge does: one indented by four spaces whose scripts alone change shows
// no dependency change (the diff alone cannot tell its top level), wherever
// the card is read (the chat's history, the card, the full diff, a link).
func TestPatchCardDepsUsesProjectFile(t *testing.T) {
	st, p, own := mergeStore(t)
	ctx := context.Background()
	os.MkdirAll(filepath.Join(p.Path, "web"), 0o755)
	var long strings.Builder // the scripts' hunk is far from the file's top
	long.WriteString("{\n    \"name\": \"web\",\n    \"scripts\": {\n")
	for i := range 6 {
		fmt.Fprintf(&long, "        \"s%d\": \"echo %d\",\n", i, i)
	}
	long.WriteString("        \"dev\": \"nuxt dev\"\n    },\n    \"dependencies\": {\n        \"nuxt\": \"4.5.2\"\n    }\n}\n")
	os.WriteFile(filepath.Join(p.Path, "web", "package.json"), []byte(long.String()), 0o644)
	e := &Engine{store: st, acts: actions.New(st, nil)}
	msg, _ := st.Chat().AddMessage(ctx, storage.Message{ConversationID: own.ID, Role: "assistant", Content: "done"})
	diff := "diff --git a/web/package.json b/web/package.json\n--- a/web/package.json\n+++ b/web/package.json\n@@ -7,4 +7,4 @@\n         \"s3\": \"echo 3\",\n         \"s4\": \"echo 4\",\n         \"s5\": \"echo 5\",\n-        \"dev\": \"nuxt dev\"\n+        \"dev\": \"nuxt dev --port 3001\"\n"
	pat, err := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, MessageID: msg.ID, Diff: diff, Files: []string{"web/package.json"}, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if dto := FullPatchDTO(pat); len(dto.Deps) == 0 {
		t.Log("the diff alone counts it (unknown): expected, the card must not")
	}
	if dto, _ := e.GetPatchDTO(ctx, pat.ID); len(dto.Deps) != 0 {
		t.Fatalf("card deps = %+v", dto.Deps)
	}
	if dto := e.FullPatch(ctx, pat); len(dto.Deps) != 0 {
		t.Fatalf("full diff deps = %+v", dto.Deps)
	}
	hist, _ := e.History(ctx, own.ID)
	for _, m := range hist {
		for _, d := range m.Patches {
			if len(d.Deps) != 0 {
				t.Fatalf("history deps = %+v", d.Deps)
			}
		}
	}
	// a dependency bump is still one
	bump := "diff --git a/web/package.json b/web/package.json\n--- a/web/package.json\n+++ b/web/package.json\n@@ -10,4 +10,4 @@\n         \"dev\": \"nuxt dev\"\n     },\n     \"dependencies\": {\n-        \"nuxt\": \"4.5.2\"\n+        \"nuxt\": \"4.6.4\"\n"
	pb, _ := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, MessageID: msg.ID, Diff: bump, Files: []string{"web/package.json"}, Status: "pending"})
	if dto, _ := e.GetPatchDTO(ctx, pb.ID); len(dto.Deps) != 1 || dto.Deps[0].Dir != "web" {
		t.Fatalf("bump deps = %+v", dto.Deps)
	}
}

// A merged diff that changes a package's dependencies leaves a "Cài thư
// viện" card next to it for that package; it is shown after the diff and
// holds nothing of the chat back. A diff of code only leaves none.
func TestAppliedDepDiffProposesInstall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	st, p, own := mergeStore(t)
	ctx := context.Background()
	os.MkdirAll(filepath.Join(p.Path, "web"), 0o755)
	os.WriteFile(filepath.Join(p.Path, "web", "package.json"), []byte(`{"dependencies":{"nuxt":"4.5.2"}}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(p.Path, "web", "pnpm-lock.yaml"), []byte("nuxt: 4.5.2\n"), 0o644)
	os.WriteFile(filepath.Join(p.Path, "a.go"), []byte("package a\n"), 0o644)
	e := &Engine{store: st, acts: actions.New(st, nil)}
	msg, _ := st.Chat().AddMessage(ctx, storage.Message{ConversationID: own.ID, Role: "assistant", Content: "done"})
	pat, err := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, MessageID: msg.ID, Diff: depDiff, Files: []string{"web/package.json", "web/pnpm-lock.yaml"}, Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if dto := FullPatchDTO(pat); len(dto.Deps) != 1 || dto.Deps[0].Dir != "web" || dto.Deps[0].Manager != "pnpm" {
		t.Fatalf("card deps = %+v", dto.Deps)
	}
	d, err := e.DecidePatch(ctx, pat.ID, true)
	if err != nil || d.Status != "applied" {
		t.Fatalf("merge = %s %q %v", d.Status, d.Detail, err)
	}
	acts, _ := st.Actions().List(ctx, own.ID, "", "")
	if len(acts) != 1 {
		t.Fatalf("cards = %d, want 1", len(acts))
	}
	a := acts[0]
	if a.Kind != "deps_install" || a.Status != "pending" || a.Target != "web (pnpm)" || a.Args.Patch != pat.ID || a.MessageID != msg.ID || a.Args.Dir != "" {
		t.Fatalf("card = %+v", a)
	}
	if e.Waiting(ctx, own.ID) {
		t.Fatal("the install card holds the chat back")
	}
	seq, _ := e.cardSeqs(ctx, own.ID, acts, []storage.Patch{pat})
	if seq[pat.ID] != 1 || seq[a.ID] != 2 {
		t.Fatalf("numbers = %v", seq)
	}

	// "Cài lại": the waiting card is given back, a new one once it was decided
	if again, err := e.InstallDeps(ctx, pat.ID); err != nil || len(again) != 1 || again[0].ID != a.ID {
		t.Fatalf("install again while waiting = %+v %v", again, err)
	}
	a.Status = "failed"
	_ = st.Actions().Update(ctx, a)
	if again, err := e.InstallDeps(ctx, pat.ID); err != nil || len(again) != 1 || again[0].ID == a.ID || again[0].Patch != pat.ID {
		t.Fatalf("install again after a failure = %+v %v", again, err)
	}

	code := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-package a\n+package b\n"
	pat2, _ := st.Chat().AddPatch(ctx, storage.Patch{ConversationID: own.ID, MessageID: msg.ID, Diff: code, Files: []string{"a.go"}, Status: "pending"})
	if _, err := e.DecidePatch(ctx, pat2.ID, true); err != nil {
		t.Fatal(err)
	}
	if acts, _ := st.Actions().List(ctx, own.ID, "", ""); len(acts) != 2 {
		t.Fatalf("a code diff made a card: %d", len(acts))
	}
	if _, err := e.InstallDeps(ctx, pat2.ID); err != ErrNoDeps {
		t.Fatalf("a code diff's install = %v", err)
	}
}
