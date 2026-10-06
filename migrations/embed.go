// Package migrations embeds the versioned SQL migrations for every driver.
// Each dialect keeps the same version numbers (see docs/DECISIONS.md ADR-003).
package migrations

import "embed"

// SQLite holds goose migrations for the sqlite driver.
//
//go:embed sqlite/*.sql
var SQLite embed.FS
