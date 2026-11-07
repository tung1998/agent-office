package main

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/tung1998/agent-office/internal/webui"
)

// apiPrefixes are the paths internal/api/server.go registers outside
// /api/**; a request under one of these never falls through to the UI.
var apiPrefixes = []string{"/api/", "/hooks/", "/mcp", "/healthz"}

// withEmbeddedUI serves the built dashboard for requests that aren't one of
// the API's own routes, so a single `office serve` can answer both without
// the separate Nuxt process `office run` otherwise starts. It is a no-op
// (returns api unchanged) when no dashboard build was embedded.
func withEmbeddedUI(api http.Handler) http.Handler {
	dist, ok := webui.FS()
	return withEmbeddedUIFS(api, dist, ok)
}

// withEmbeddedUIFS is withEmbeddedUI with the embed lookup passed in, so a
// test can exercise the "no build embedded" no-op path with an fstest.MapFS
// instead of depending on whatever dist/ happens to hold on the machine
// running `go test` (e.g. after `make ui-build`).
func withEmbeddedUIFS(api http.Handler, dist fs.FS, ok bool) http.Handler {
	if !ok {
		return api
	}
	return embeddedUIHandler(api, dist)
}

// embeddedUIHandler does the actual routing, taking dist directly so tests
// can pass an fstest.MapFS instead of the real embedded build.
func embeddedUIHandler(api http.Handler, dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			api.ServeHTTP(w, r)
			return
		}
		for _, p := range apiPrefixes {
			if strings.HasPrefix(r.URL.Path, p) {
				api.ServeHTTP(w, r)
				return
			}
		}
		// same policy as dashboard/server/middleware/security-headers.ts, for
		// when office serve answers these pages itself (no Nitro in front)
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "."
		}
		if _, err := fs.Stat(dist, name); err != nil {
			if strings.HasPrefix(r.URL.Path, "/_nuxt/") {
				// a missing build asset is a 404, not the app shell; outside
				// /_nuxt/ a dot is just as likely part of a client route
				// (e.g. /u/a.b), so only this prefix gets the stricter check
				http.NotFound(w, r)
				return
			}
			// unknown route: let the SPA's router handle it client-side
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
