// Package migrations embeds the goose SQL migrations (SPEC §4.3): one file per
// change, named NNNN_<description>.sql, each with an Up and a Down section.
package migrations

import "embed"

// FS contains every *.sql migration at its root.
//
//go:embed *.sql
var FS embed.FS
