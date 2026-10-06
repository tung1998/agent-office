// Package schema embeds the JSON Schema of office.config.json so the binary
// validates configs without needing the repo checkout.
package schema

import _ "embed"

// OfficeConfig is the raw JSON Schema (draft 2020-12).
//
//go:embed office.config.schema.json
var OfficeConfig []byte
