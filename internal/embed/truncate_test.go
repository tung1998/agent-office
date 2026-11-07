package embed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// The HTTP error body is cut by rune count, not byte count: a multi-byte
// rune sitting at the cut boundary must not be split in half.
func TestEmbedErrorMessageUTF8Boundary(t *testing.T) {
	body := strings.Repeat("a", 299) + "ệ" + "bbbb"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newClient(srv.URL, "", "m")
	_, err := c.Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !utf8.ValidString(err.Error()) {
		t.Fatalf("embed error not valid UTF-8: %q", err.Error())
	}
}
