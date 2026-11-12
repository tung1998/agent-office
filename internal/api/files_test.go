package api_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The file editor: ignored/secret files hidden until asked, a save based on
// a stale sha is refused, secrets need a confirm, nothing escapes the folder.
func TestFileEditor(t *testing.T) {
	t.Parallel()
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

	// upload: any bytes, a new folder made, an existing file only with overwrite
	up := func(p, data string, extra map[string]any) int {
		b := map[string]any{"path": p, "data": base64.StdEncoding.EncodeToString([]byte(data))}
		for k, v := range extra {
			b[k] = v
		}
		resp, _ := do(t, admin, "POST", base+"/file/upload", b, nil)
		return resp.StatusCode
	}
	if c := up("assets/img/x.bin", "\x00\x01", nil); c != 200 {
		t.Fatalf("upload = %d", c)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "assets/img/x.bin")); string(b) != "\x00\x01" {
		t.Fatalf("uploaded = %q", b)
	}
	if c := up("a.txt", "up", nil); c != 409 {
		t.Fatalf("upload over existing = %d", c)
	}
	if c := up("a.txt", "up", map[string]any{"overwrite": true}); c != 200 {
		t.Fatalf("upload overwrite = %d", c)
	}
	if c := up("sub/.env", "K=3", nil); c != 428 {
		t.Fatalf("upload secret = %d", c)
	}
	if c := up(".git/hooks/x", "x", nil); c == 200 {
		t.Fatal("uploaded into .git")
	}
	up("../up.txt", "x", nil) // cleaned to a path inside the project
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "up.txt")); err == nil {
		t.Fatal("uploaded outside the project")
	}
}

func TestFilePreview(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	image := []byte("\x89PNG\r\n\x1a\npreview")
	if err := os.WriteFile(filepath.Join(dir, "icon48.png"), image, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("<script>alert(1)</script>"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	_, body := do(t, admin, "POST", e.srv.URL+"/api/projects", map[string]any{"path": dir, "name": "preview"}, nil)
	base := e.srv.URL + "/api/projects/" + body["project"].(map[string]any)["id"].(string)
	_, meta := do(t, admin, "GET", base+"/file?path=icon48.png", nil, nil)
	if meta["binary"] != true || meta["preview"].(map[string]any)["kind"] != "image" {
		t.Fatalf("metadata = %v", meta)
	}
	req, _ := http.NewRequest("GET", base+"/file/preview?path=icon48.png", nil)
	req.Header.Set("Range", "bytes=0-7")
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Type") != "image/png" || string(data) != string(image[:8]) || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("preview = %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), data)
	}
	_, htmlMeta := do(t, admin, "GET", base+"/file?path=page.html", nil, nil)
	if htmlMeta["preview"].(map[string]any)["kind"] != "document" {
		t.Fatalf("HTML metadata = %v", htmlMeta)
	}
	resp, err = admin.Get(base + "/file/preview?path=page.html")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" || resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") || string(html) != "<script>alert(1)</script>" {
		t.Fatalf("HTML preview = %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), html)
	}
	resp, err = admin.Get(base + "/file/preview?path=page.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("script preview = %d", resp.StatusCode)
	}
}
