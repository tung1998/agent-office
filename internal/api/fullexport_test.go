package api_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"
)

// ADR-275: the whole office from the dashboard: an admin exports (a
// passphrase of 12+), downloads, and uploads it back as a raw body with the
// passphrase in a header; nobody else, and no cross-site page, can.
func TestFullExportImportFromDashboard(t *testing.T) {
	e := setup(t)
	admin, member := e.client(t), e.client(t)
	login(t, e, admin, "admin@x.io", "admin-password")
	login(t, e, member, "member@x.io", "member-password")
	const pass = "a long enough passphrase"

	if resp, _ := do(t, member, "POST", e.srv.URL+"/api/transfer/full-export", map[string]string{"passphrase": pass}, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member export = %d", resp.StatusCode)
	}
	if resp, _ := do(t, admin, "POST", e.srv.URL+"/api/transfer/full-export", map[string]string{"passphrase": "short"}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("short passphrase = %d", resp.StatusCode)
	}
	resp, body := do(t, admin, "POST", e.srv.URL+"/api/transfer/full-export", map[string]string{"passphrase": pass}, nil)
	name, _ := body["name"].(string)
	if resp.StatusCode != 200 || name == "" {
		t.Fatalf("export = %d %v", resp.StatusCode, body)
	}
	if resp, _ := do(t, admin, "GET", e.srv.URL+"/api/transfer/full-export/..%2Fkey", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("path escape = %d", resp.StatusCode)
	}
	dl, err := admin.Get(e.srv.URL + "/api/transfer/full-export/" + name)
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	if dl.StatusCode != 200 || len(archive) < 100 {
		t.Fatalf("download = %d, %d bytes", dl.StatusCode, len(archive))
	}

	upload := func(c *http.Client, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/transfer/full-import?mode=clone", bytes.NewReader(archive))
		req.Header.Set("Content-Type", "application/octet-stream")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, _ := upload(admin, nil); code != http.StatusUnsupportedMediaType {
		t.Fatalf("raw body without the passphrase header = %d (csrf)", code)
	}
	if code, _ := upload(admin, map[string]string{"X-Office-Passphrase": pass, "Origin": "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("cross-site upload = %d", code)
	}
	if code, _ := upload(member, map[string]string{"X-Office-Passphrase": pass}); code != http.StatusForbidden {
		t.Fatalf("member upload = %d", code)
	}
	if code, b := upload(admin, map[string]string{"X-Office-Passphrase": "a wrong passphrase!!"}); code != http.StatusBadRequest {
		t.Fatalf("wrong passphrase = %d %s", code, b)
	}
	if code, b := upload(admin, map[string]string{"X-Office-Passphrase": pass}); code != 200 || !bytes.Contains([]byte(b), []byte(`"pending":true`)) {
		t.Fatalf("import = %d %s", code, b)
	}

	// another office fetches only with its key, and only what it was offered
	if resp, _ := do(t, e.client(t), "GET", e.srv.URL+"/api/peer/full-export", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("peer fetch without a key = %d", resp.StatusCode)
	}
}
