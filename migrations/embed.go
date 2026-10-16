package migrations

import "embed"

// FS contains the immutable SQL migration files shipped with the application.
//
//go:embed *.sql
var FS embed.FS
