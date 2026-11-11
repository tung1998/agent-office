package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
		"Content-Security-Policy": dashboardCSP(testDist()),
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

// ADR-271: the dashboard's inline scripts run by hash, nothing else inline;
// data blocks and external files add no hash.
func TestDashboardCSP(t *testing.T) {
	dist := fstest.MapFS{"index.html": {Data: []byte(`<html><head>` +
		`<script type="importmap">{"imports":{}}</script>` +
		`<script type="module" src="/_nuxt/a.js"></script>` +
		`<script>boot()</script>` +
		`<script type="application/json" id="__NUXT_DATA__">[1]</script>` +
		`</head></html>`)}}
	csp := dashboardCSP(dist)
	script := ""
	for _, d := range strings.Split(csp, "; ") {
		if strings.HasPrefix(d, "script-src ") {
			script = d
		}
	}
	if n := strings.Count(script, "'sha256-"); n != 2 {
		t.Fatalf("want 2 hashes (importmap, boot), got %d: %s", n, script)
	}
	if !strings.Contains(script, "'sha256-") || strings.Contains(script, "unsafe-inline") {
		t.Fatalf("script-src = %s", script)
	}
	for _, want := range []string{"frame-ancestors 'none'", "object-src 'none'", "base-uri 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("missing %q in %s", want, csp)
		}
	}
	rec := httptest.NewRecorder()
	embeddedUIHandler(http.NotFoundHandler(), dist).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("Content-Security-Policy") != csp {
		t.Fatalf("served CSP = %q", rec.Header().Get("Content-Security-Policy"))
	}
}
