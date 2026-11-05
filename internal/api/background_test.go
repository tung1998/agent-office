package api_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"testing"
)

// A dashboard background is the account's own: kept as uploaded, typed by its
// bytes, refused when it is not an image or video, and gone once removed.
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
	if r, _ := do(t, admin, "DELETE", url, nil, nil); r.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", r.StatusCode)
	}
	if r, _ := do(t, admin, "GET", url, nil, nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete = %d", r.StatusCode)
	}
}
