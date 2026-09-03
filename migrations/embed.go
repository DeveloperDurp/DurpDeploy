package migrations

import (
	"embed"
	"io/fs"
)

//go:embed *.sql
var FS embed.FS

//go:embed mssql/*.sql
var MSSQLFS embed.FS

//go:embed postgres/028_direct_agent_dispatch.sql postgres/029_remove_agent_enrollment.sql postgres/030_pairing_committing_state.sql postgres/031_persist_pool_to_assignment.sql
var postgresFS embed.FS

var PostgresFS fs.FS = postgresMigrationFS{}

type postgresMigrationFS struct{}

func (postgresMigrationFS) Open(name string) (fs.File, error) {
	if name == "028_direct_agent_dispatch.sql" ||
		name == "029_remove_agent_enrollment.sql" ||
		name == "030_pairing_committing_state.sql" ||
		name == "031_persist_pool_to_assignment.sql" {
		return postgresFS.Open("postgres/" + name)
	}
	return FS.Open(name)
}
