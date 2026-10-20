// Package migrations embeds the SQL migration files.
// Never edit an applied migration; add a new numbered pair instead.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
