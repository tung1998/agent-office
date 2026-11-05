package api_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// Dashboard backgrounds are the account's own library: each upload kept as
// uploaded and typed by its bytes, served by its version, refused when it is
// not an image or video, and gone once removed.
func TestBackgroundPerAccount(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	member := e.client(t)
	login(t, e, member, "member@x.io", "member-password")
	url := e.srv.URL + "/api/me/background"

	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	resp, got := do(t, admin, "PUT", url, map[string]any{"data": base64.StdEncoding.EncodeToString(png)}, nil)
	if resp.StatusCode != 200 || got["mime"] != "image/png" || got["version"] == nil {
		t.Fatalf("put = %d %v", resp.StatusCode, got)
	}
	r, err := admin.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/png" || len(body) != len(png) {
		t.Fatalf("get = %d %q %d bytes", r.StatusCode, r.Header.Get("Content-Type"), len(body))
	}
	if r, _ := do(t, member, "GET", url, nil, nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("member sees admin's: %d", r.StatusCode)
	}
	// a page is not a background, whatever it is called
	html := base64.StdEncoding.EncodeToString([]byte("<html><script>alert(1)</script></html>"))
	if r, _ := do(t, admin, "PUT", url, map[string]any{"data": html}, nil); r.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("html = %d", r.StatusCode)
	}
	gif := append([]byte("GIF89a"), make([]byte, 32)...)
	_, second := do(t, admin, "PUT", url, map[string]any{"data": base64.StdEncoding.EncodeToString(gif)}, nil)
	var list []map[string]any
	if list = library(t, admin, url+"s"); len(list) != 2 || list[0]["mime"] != "image/gif" {
		t.Fatalf("library = %v", list)
	}
	if list = library(t, member, url+"s"); len(list) != 0 {
		t.Fatalf("member's library = %v", list)
	}
	first := fmt.Sprintf("%.0f", got["version"].(float64))
	r, err = admin.Get(url + "?v=" + first)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("older one = %d %q", r.StatusCode, r.Header.Get("Content-Type"))
	}
	if r, _ := do(t, admin, "DELETE", url, nil, nil); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("delete without v = %d", r.StatusCode)
	}
	if r, _ := do(t, admin, "DELETE", url+"?v="+first, nil, nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", r.StatusCode)
	}
	if r, _ := do(t, admin, "GET", url+"?v="+first, nil, nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete = %d", r.StatusCode)
	}
	if list = library(t, admin, url+"s"); len(list) != 1 || list[0]["version"] != second["version"] {
		t.Fatalf("library after delete = %v", list)
	}
}

func library(t *testing.T, c *http.Client, url string) []map[string]any {
	t.Helper()
	r, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var out []map[string]any
	if r.StatusCode != 200 || json.NewDecoder(r.Body).Decode(&out) != nil {
		t.Fatalf("library = %d", r.StatusCode)
	}
	return out
}
