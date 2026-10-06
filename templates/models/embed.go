// Package models embeds the built-in org model templates (solo, team, council).
package models

import "embed"

// FS holds one JSON file per built-in template.
//
//go:embed *.json
var FS embed.FS
