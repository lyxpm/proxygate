package web

import "embed"

// FS contains the built frontend. The placeholder is replaced by `pnpm build`.
//
//go:embed all:dist
var FS embed.FS
