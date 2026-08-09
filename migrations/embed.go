package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

//go:embed mssql/*.sql
var MSSQLFS embed.FS
