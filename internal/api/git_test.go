package api_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/api"
	"github.com/tung1998/agent-office/internal/fakebin"
)

// Fetch refreshes the remote so the bar under the title shows "behind".
func TestGitFetch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	base := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x.io", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x.io")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	remote, a, b := filepath.Join(base, "r.git"), filepath.Join(base, "a"), filepath.Join(base, "b")
	run(base, "init", "-q", "--bare", "-b", "main", remote)
	run(base, "clone", "-q", remote, a)
	os.WriteFile(filepath.Join(a, "x"), []byte("1"), 0o644)
	run(a, "add", ".")
	run(a, "commit", "-qm", "one")
	run(a, "push", "-q", "-u", "origin", "main")
	run(base, "clone", "-q", remote, b)
	os.WriteFile(filepath.Join(b, "x"), []byte("2"), 0o644)
	run(b, "commit", "-qam", "two")
	run(b, "push", "-q")

	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": a, "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/git/fetch", map[string]any{}, nil)
	st, _ := body["status"].(map[string]any)
	if resp.StatusCode != 200 || st == nil || st["behind"] != float64(1) || st["branch"] != "main" {
		t.Fatalf("fetch = %d %v", resp.StatusCode, body)
	}
}

// A push slower than the request waits goes on in the background (202): the
// request does not hold a browser connection, and git ends it all the same.
func TestGitPushBackground(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	defer api.SetGitWait(50 * time.Millisecond)()
	base := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x.io", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x.io")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	remote, a := filepath.Join(base, "r.git"), filepath.Join(base, "a")
	run(base, "init", "-q", "--bare", "-b", "main", remote)
	hook := filepath.Join(remote, "hooks", "pre-receive")
	fakebin.Write(hook, []byte("#!/bin/sh\nsleep 1\n")) // a slow remote
	run(base, "clone", "-q", remote, a)
	os.WriteFile(filepath.Join(a, "x"), []byte("1"), 0o644)
	run(a, "add", ".")
	run(a, "commit", "-qm", "one")
	run(a, "push", "-q", "-u", "origin", "main")
	os.WriteFile(filepath.Join(a, "x"), []byte("2"), 0o644)
	run(a, "commit", "-qam", "two")

	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": a, "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/git/push", map[string]any{}, nil)
	if resp.StatusCode != 202 || body["running"] != "push" {
		t.Fatalf("push = %d %v", resp.StatusCode, body)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/git/push", map[string]any{}, nil); resp.StatusCode != 409 {
		t.Fatalf("a second push while one runs = %d", resp.StatusCode)
	}
	for i := 0; ; i++ {
		_, body = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/git", nil, nil)
		if st, _ := body["status"].(map[string]any); st != nil && st["ahead"] == float64(0) {
			break
		}
		if i > 100 {
			t.Fatalf("the push did not end: %v", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A push still running in the background must block a branch switch (and a
// commit) on the same project repo: two git processes must not race on the
// same working directory/refs.
func TestGitSwitchBlockedWhilePushRunning(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	defer api.SetGitWait(50 * time.Millisecond)()
	base := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x.io", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x.io")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	remote, a := filepath.Join(base, "r.git"), filepath.Join(base, "a")
	run(base, "init", "-q", "--bare", "-b", "main", remote)
	hook := filepath.Join(remote, "hooks", "pre-receive")
	fakebin.Write(hook, []byte("#!/bin/sh\nsleep 1\n")) // a slow remote
	run(base, "clone", "-q", remote, a)
	os.WriteFile(filepath.Join(a, "x"), []byte("1"), 0o644)
	run(a, "add", ".")
	run(a, "commit", "-qm", "one")
	run(a, "push", "-q", "-u", "origin", "main")
	run(a, "branch", "side")
	os.WriteFile(filepath.Join(a, "x"), []byte("2"), 0o644)
	run(a, "commit", "-qam", "two")

	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": a, "name": "shop"}, nil)
	pid := body["project"].(map[string]any)["id"].(string)
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/git/push", map[string]any{}, nil)
	if resp.StatusCode != 202 || body["running"] != "push" {
		t.Fatalf("push = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/git/switch", map[string]any{"name": "side"}, nil); resp.StatusCode != 409 {
		t.Fatalf("switch while push runs = %d %v", resp.StatusCode, body)
	}
	if resp, body := do(t, admin, "POST", e.srv.URL+"/api/projects/"+pid+"/git/fetch", map[string]any{}, nil); resp.StatusCode != 409 {
		t.Fatalf("fetch while push runs = %d %v (must not answer 200 with running:\"fetch\": no fetch is actually running, so the UI would wait forever for its end event)", resp.StatusCode, body)
	}
	for i := 0; ; i++ {
		_, body = do(t, admin, "GET", e.srv.URL+"/api/projects/"+pid+"/git", nil, nil)
		if st, _ := body["status"].(map[string]any); st != nil && st["ahead"] == float64(0) {
			break
		}
		if i > 100 {
			t.Fatalf("the push did not end: %v", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
