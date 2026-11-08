// Package migrations embeds SQL schema migrations so the binary can migrate a
// fresh database without external tooling.
package migrations

import "embed"

// FS contains the ordered *.sql migration files.
//
//go:embed *.sql
var FS embed.FS
