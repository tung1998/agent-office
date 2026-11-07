package workflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoHTTPBlocksSSRFTargets(t *testing.T) {
	// A reachable loopback server stands in for an internal/admin service or
	// the cloud metadata endpoint: without the guard this request succeeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not be reachable"))
	}))
	defer srv.Close()

	cases := []string{
		srv.URL,
		"http://127.0.0.1:1/",
		"http://localhost/",
	}
	for _, u := range cases {
		_, _, err := DoHTTP(context.Background(), Step{URL: u})
		if err == nil {
			t.Fatalf("DoHTTP(%q): expected error, got nil", u)
		}
	}
}

func TestDoHTTPRejectsNonHTTPScheme(t *testing.T) {
	_, _, err := DoHTTP(context.Background(), Step{URL: "file:///etc/passwd"})
	if err == nil {
		t.Fatal("expected error for non-http(s) scheme")
	}
}
