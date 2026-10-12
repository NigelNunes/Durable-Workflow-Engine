// Package migrations embeds the SQL files in this directory.
// The embed directive has to live next to the .sql files: Go cannot embed
// files from a parent directory.
package migrations

import "embed"

// FS is the set of *.sql migration files, addressed by filename.
//
//go:embed *.sql
var FS embed.FS
