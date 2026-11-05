package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/actions"
	"github.com/tung1998/agent-office/internal/perm"
	"github.com/tung1998/agent-office/internal/storage"
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
