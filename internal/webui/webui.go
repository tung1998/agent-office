// Package webui embeds the built dashboard (dashboard/.output/public) into
// the office binary, so `office serve` can serve it without a separate Nuxt
// process. `make ui-build` copies the build into dist/ before `go build`;
// dist/.gitkeep keeps the embed directive valid when no build has run yet.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the embedded dashboard's static files and whether a real build
// is present. Until `make ui-build` runs, dist holds only the placeholder
// and ok is false.
func FS() (files fs.FS, ok bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
