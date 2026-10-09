// Package migrations embeds the SQL schema migrations so the service can migrate the database on start-up.
package migrations

import "embed"

// Files holds every *.sql migration in golang-migrate naming format (NNNNNN_name.up.sql / .down.sql).
//
//go:embed *.sql
var Files embed.FS
