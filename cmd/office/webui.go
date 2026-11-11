package main

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"regexp"
	"slices"
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
	csp := dashboardCSP(dist)
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
		h.Set("Content-Security-Policy", csp)
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

// inlineScript: a <script> element and its body (Nuxt's importmap and its
// colour-mode/boot scripts are inline in the built index.html).
var inlineScript = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)

// dashboardCSP (ADR-271): only the build's own scripts run, the inline ones
// by their hash (computed from the embedded build, so a new build needs no
// change here), never a script a message or a file could slip into the page.
// Styles stay inline (Vue/Nuxt UI set style attributes); images, fonts and
// the preview frames (a watched page, a PDF) may come from anywhere.
func dashboardCSP(dist fs.FS) string {
	var hashes []string
	files, _ := fs.Glob(dist, "*.html")
	for _, f := range files {
		b, err := fs.ReadFile(dist, f)
		if err != nil {
			continue
		}
		for _, m := range inlineScript.FindAllStringSubmatch(string(b), -1) {
			attrs := strings.ToLower(m[1])
			if strings.Contains(attrs, "src=") || strings.Contains(attrs, "application/json") || strings.TrimSpace(m[2]) == "" {
				continue // an external file (covered by 'self') or data, never run
			}
			sum := sha256.Sum256([]byte(m[2]))
			if h := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"; !slices.Contains(hashes, h) {
				hashes = append(hashes, h)
			}
		}
	}
	script := strings.Join(append([]string{"'self'"}, hashes...), " ")
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + script,
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob: https: http:",
		"font-src 'self' data:",
		"connect-src 'self' https://api.iconify.design https://api.simplesvg.com https://api.unisvg.com",
		"frame-src 'self' blob: https: http:",
		"worker-src 'self' blob:",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}
