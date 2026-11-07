package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func testDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":     {Data: []byte("<html>app shell</html>")},
		"assets/app.css": {Data: []byte("body{}")},
		"_nuxt/chunk.js": {Data: []byte("console.log(1)")},
	}
}

func apiStub(t *testing.T, hit *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hit = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestEmbeddedUIAPIPrefixes(t *testing.T) {
	for _, p := range []string{"/api/auth/status", "/hooks/abc", "/mcp", "/mcp/s/x", "/healthz"} {
		var hitAPI bool
		h := embeddedUIHandler(apiStub(t, &hitAPI), testDist())
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !hitAPI {
			t.Errorf("path %s: expected to reach the API handler, it did not", p)
		}
	}
}

func TestEmbeddedUINonGetGoesToAPI(t *testing.T) {
	var hitAPI bool
	h := embeddedUIHandler(apiStub(t, &hitAPI), testDist())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !hitAPI {
		t.Error("POST / should reach the API handler, not the static file server")
	}
}

func TestEmbeddedUIServesStaticFile(t *testing.T) {
	var hitAPI bool
	h := embeddedUIHandler(apiStub(t, &hitAPI), testDist())
	req := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if hitAPI {
		t.Fatal("static asset request should not reach the API handler")
	}
	if rec.Body.String() != "body{}" {
		t.Errorf("got body %q, want the asset's content", rec.Body.String())
	}
}

func TestEmbeddedUISPAFallback(t *testing.T) {
	var hitAPI bool
	h := embeddedUIHandler(apiStub(t, &hitAPI), testDist())
	req := httptest.NewRequest(http.MethodGet, "/some/client/route", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if hitAPI {
		t.Fatal("unknown client route should not reach the API handler")
	}
	if rec.Body.String() != "<html>app shell</html>" {
		t.Errorf("got body %q, want index.html (SPA fallback)", rec.Body.String())
	}
}

func TestEmbeddedUIMissingAssetIs404(t *testing.T) {
	var hitAPI bool
	h := embeddedUIHandler(apiStub(t, &hitAPI), testDist())
	req := httptest.NewRequest(http.MethodGet, "/_nuxt/missing-chunk.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if hitAPI {
		t.Fatal("missing asset should not reach the API handler")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404 for a missing asset instead of the app shell", rec.Code)
	}
}

func TestEmbeddedUISecurityHeaders(t *testing.T) {
	var hitAPI bool
	h := embeddedUIHandler(apiStub(t, &hitAPI), testDist())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	want := map[string]string{
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "same-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s: got %q, want %q", k, got, v)
		}
	}
}

func TestEmbeddedUIMissingAssetOutsideNuxtFallsBackToShell(t *testing.T) {
	// a dotted path outside /_nuxt/ (e.g. a client route like /u/a.b) must not
	// be mistaken for a missing build asset and 404 — only /_nuxt/ gets that
	// stricter check.
	var hitAPI bool
	h := embeddedUIHandler(apiStub(t, &hitAPI), testDist())
	req := httptest.NewRequest(http.MethodGet, "/u/a.b", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if hitAPI {
		t.Fatal("unknown client route should not reach the API handler")
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>app shell</html>" {
		t.Errorf("got status %d body %q, want 200 with index.html (SPA fallback)", rec.Code, rec.Body.String())
	}
}

func TestWithEmbeddedUIFSNoOpWithoutBuild(t *testing.T) {
	// ok=false (webui.FS() reports no real build embedded, e.g. a placeholder
	// dist/.gitkeep-only checkout) must hand back the API handler untouched,
	// regardless of what dist/ happens to hold on the machine running the
	// test (it is not webui.FS() itself being exercised here).
	var hitAPI bool
	api := apiStub(t, &hitAPI)
	h := withEmbeddedUIFS(api, testDist(), false)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !hitAPI {
		t.Error("with ok=false, withEmbeddedUIFS should pass every request straight to the API handler")
	}
}

func TestWithEmbeddedUIFSServesWhenOK(t *testing.T) {
	var hitAPI bool
	api := apiStub(t, &hitAPI)
	h := withEmbeddedUIFS(api, testDist(), true)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if hitAPI {
		t.Fatal("with ok=true, withEmbeddedUIFS should serve the dashboard, not the API handler")
	}
	if rec.Body.String() != "<html>app shell</html>" {
		t.Errorf("got body %q, want index.html", rec.Body.String())
	}
}
