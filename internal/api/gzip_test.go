package api_test

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// JSON goes gzip'd to a client that takes it (a phone over a VPN downloads a
// fraction); what is streamed (SSE) is never held back in a buffer.
func TestAPIGzip(t *testing.T) {
	e := setup(t)
	admin := e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/templates", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("encoding = %q", resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body["templates"] == nil {
		t.Fatalf("body = %s %v", raw, err)
	}
}
