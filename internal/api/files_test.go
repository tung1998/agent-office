package api_test

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The file editor: ignored/secret files hidden until asked, a save based on
// a stale sha is refused, secrets need a confirm, nothing escapes the folder.
func TestFileEditor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git missing")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("dist/\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("K=1\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dist"), 0o755)

	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir, "name": "shop"}, nil)
	base := e.srv.URL + "/api/projects/" + body["project"].(map[string]any)["id"].(string)

	names := func(q string) map[string]bool {
		_, b := do(t, admin, "GET", base+"/files"+q, nil, nil)
		out := map[string]bool{}
		for _, x := range b["entries"].([]any) {
			out[x.(map[string]any)["name"].(string)] = true
		}
		return out
	}
	if n := names(""); !n["a.txt"] || n[".env"] || n["dist"] || n[".git"] {
		t.Fatalf("default list = %v", n)
	}
	if n := names("?hidden=1"); !n[".env"] || !n["dist"] || n[".git"] {
		t.Fatalf("hidden list = %v", n)
	}

	resp, f := do(t, admin, "GET", base+"/file?path=a.txt", nil, nil)
	if resp.StatusCode != 200 || f["content"] != "one\n" {
		t.Fatalf("read = %d %v", resp.StatusCode, f)
	}
	sha := f["sha"].(string)
	resp, _ = do(t, admin, "PUT", base+"/file", map[string]any{"path": "a.txt", "content": "two\n", "sha": sha}, nil)
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); resp.StatusCode != 200 || string(b) != "two\n" {
		t.Fatalf("write = %d %q", resp.StatusCode, b)
	}
	// the old sha is stale now
	if resp, _ = do(t, admin, "PUT", base+"/file", map[string]any{"path": "a.txt", "content": "x", "sha": sha}, nil); resp.StatusCode != 409 {
		t.Fatalf("stale write = %d", resp.StatusCode)
	}

	_, f = do(t, admin, "GET", base+"/file?path=.env", nil, nil)
	envSha := f["sha"].(string)
	if resp, _ = do(t, admin, "PUT", base+"/file", map[string]any{"path": ".env", "content": "K=2\n", "sha": envSha}, nil); resp.StatusCode != 428 {
		t.Fatalf("secret without confirm = %d", resp.StatusCode)
	}
	if resp, _ = do(t, admin, "PUT", base+"/file", map[string]any{"path": ".env", "content": "K=2\n", "sha": envSha, "confirm": true}, nil); resp.StatusCode != 200 {
		t.Fatalf("secret with confirm = %d", resp.StatusCode)
	}

	if resp, _ = do(t, admin, "PUT", base+"/file", map[string]any{"path": "src/new.go", "content": "package x\n"}, nil); resp.StatusCode != 200 {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	for _, p := range []string{"../out.txt", ".git/config"} {
		resp, _ = do(t, admin, "GET", base+"/file?path="+url.QueryEscape(p), nil, nil)
		if resp.StatusCode == 200 {
			t.Fatalf("%s readable", p)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "out.txt")); err == nil {
		t.Fatal("wrote outside the project")
	}
	if resp, _ = do(t, admin, "PUT", base+"/file", map[string]any{"path": "../out.txt", "content": "x"}, nil); resp.StatusCode == 200 {
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "out.txt")); err == nil {
			t.Fatal("wrote outside the project")
		}
	}
}
