package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tung1998/agent-office/internal/storage"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@x", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// ADR-279: a project whose repo this machine cannot clone still comes in,
// waiting for its code; the preview says so first, and a retry with the
// repo's new place ends the wait.
func TestImportedProjectWaitsForCode(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitT(t, root, "init", "-q", "--bare", origin)
	src := filepath.Join(root, "app")
	gitT(t, root, "clone", "-q", origin, src)
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("hi"), 0o644)
	gitT(t, src, "add", ".")
	gitT(t, src, "commit", "-qm", "init")
	gitT(t, src, "push", "-q", "origin", "HEAD:main")
	repo, _ := e.st.Repos().Create(ctx, storage.Repo{Name: "App", Path: src})
	const pass = "a long enough passphrase"

	_, body := do(t, admin, "POST", e.srv.URL+"/api/transfer/projects-export", map[string]any{"project_ids": []string{repo.ID}, "passphrase": pass}, nil)
	dl, _ := admin.Get(e.srv.URL + "/api/transfer/full-export/" + body["name"].(string))
	archive, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	moved := filepath.Join(root, "moved.git")
	_ = os.Rename(origin, moved) // out of reach

	req, _ := http.NewRequest("POST", e.srv.URL+"/api/transfer/projects-import", bytes.NewReader(archive))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Office-Passphrase", pass)
	res, _ := admin.Do(req)
	var prev map[string]any
	_ = json.NewDecoder(res.Body).Decode(&prev)
	res.Body.Close()
	sp := prev["projects"].([]any)[0].(map[string]any)
	if sp["access"] == "ok" || sp["access"] == nil {
		t.Fatalf("preview did not flag the unreachable repo: %v", sp)
	}
	resp, out := do(t, admin, "POST", e.srv.URL+"/api/transfer/projects-import/apply", map[string]any{"token": prev["token"], "decisions": map[string]string{sp["slug"].(string): "new"}}, nil)
	r0 := out["results"].([]any)[0].(map[string]any)
	if resp.StatusCode != 200 || r0["ok"] != true || r0["id"] == nil {
		t.Fatalf("apply = %d %v", resp.StatusCode, out)
	}
	id := r0["id"].(string)
	resp, out = do(t, admin, "GET", e.srv.URL+"/api/projects/"+id+"/pending-code", nil, nil)
	if resp.StatusCode != 200 || out["pending"] != true || out["reason"] == "" {
		t.Fatalf("pending = %d %v", resp.StatusCode, out)
	}
	resp, out = do(t, admin, "POST", e.srv.URL+"/api/projects/"+id+"/pending-code/retry", map[string]any{"url": moved}, nil)
	if resp.StatusCode != 200 || out["ok"] != true {
		t.Fatalf("retry = %d %v", resp.StatusCode, out)
	}
	if _, out = do(t, admin, "GET", e.srv.URL+"/api/projects/"+id+"/pending-code", nil, nil); out["pending"] != false {
		t.Fatalf("still waiting: %v", out)
	}
	got, _ := e.st.Repos().Get(ctx, id)
	if got.GitRemote != moved {
		t.Errorf("remote = %q, want the new URL", got.GitRemote)
	}
	if b, _ := os.ReadFile(filepath.Join(got.Path, "a.txt")); string(b) != "hi" {
		t.Errorf("code not in %s", got.Path)
	}
}
