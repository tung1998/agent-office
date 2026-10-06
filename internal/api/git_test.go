package api_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
