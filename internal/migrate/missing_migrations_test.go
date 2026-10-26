package migrate

import (
	"database/sql"
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"

	"durpdeploy/internal/testutil"
	"durpdeploy/migrations"
)

func TestRun_AppliesMissingLocalAdministrationMigration(t *testing.T) {
	forEachRemoteClaimDatabase(
		t,
		func(t *testing.T, fixture remoteClaimTestDB) {
			fixture.baselineVersion = 66
			missingVersion, deployedVersion := int64(67), int64(69)
			var migrationFS fs.FS = migrations.FS
			if fixture.gooseDialect == "mssql" {
				fixture.baselineVersion = 51
				missingVersion, deployedVersion = 52, 54
				config, err := migrationConfig(fixture.dsn)
				requireNoError(t, err, "SQL Server migration config")
				migrationFS = config.migrationFS
			}
			conn := fixture.openBaseline(t)
			t.Cleanup(func() { testutil.Close(t, conn) })

			// Reproduce a deployed build that did not contain the reserved version.
			files := fstest.MapFS{}
			names, err := fs.Glob(migrationFS, "*.sql")
			requireNoError(t, err, "list embedded migrations")
			missingName := fmt.Sprintf(
				"%03d_local_administration.sql", missingVersion,
			)
			for _, name := range names {
				if name == missingName {
					continue
				}
				data, err := fs.ReadFile(migrationFS, name)
				requireNoError(t, err, "read embedded migration")
				files[name] = &fstest.MapFile{Data: data}
			}
			goose.SetBaseFS(files)
			requireNoError(
				t,
				goose.UpTo(conn, ".", deployedVersion),
				"seed deployed schema",
			)
			_, err = conn.Exec("INSERT INTO projects(name) VALUES('retained')")
			requireNoError(t, err, "seed retained project")
			var historyCount int
			requireNoError(t, conn.QueryRow(
				"SELECT COUNT(*) FROM goose_db_version",
			).Scan(&historyCount), "read deployed migration history")

			for range 2 {
				upgraded, err := Run(fixture.dsn)
				requireNoError(t, err, "upgrade and repeat startup")
				t.Cleanup(func() { testutil.Close(t, upgraded) })
				checkMissingMigrationUpgrade(
					t, upgraded, missingVersion, deployedVersion, historyCount,
				)
			}
		},
	)
}

func checkMissingMigrationUpgrade(
	t *testing.T, conn *sql.DB,
	missingVersion, deployedVersion int64, historyCount int,
) {
	t.Helper()
	var seed, count int
	err := conn.QueryRow(
		"SELECT version FROM local_administration_lock WHERE id=1",
	).Scan(&seed)
	requireNoError(t, err, "read local administration lock")
	if seed != 0 {
		t.Fatalf("lock seed = %d, want 0", seed)
	}
	for _, version := range []int64{
		missingVersion, deployedVersion - 1, deployedVersion,
	} {
		err := conn.QueryRow(
			"SELECT COUNT(*) FROM goose_db_version"+
				" WHERE version_id=? AND is_applied=?", version, true,
		).Scan(&count)
		requireNoError(t, err, "read applied migration")
		if count != 1 {
			t.Fatalf("migration %d applied %d times, want 1", version, count)
		}
	}
	latest, err := goose.GetDBVersion(conn)
	requireNoError(t, err, "read upgraded version")
	err = conn.QueryRow("SELECT COUNT(*) FROM goose_db_version").Scan(&count)
	requireNoError(t, err, "read upgraded migration history")
	wantCount := historyCount + 1 + int(latest-deployedVersion)
	if latest <= deployedVersion || count != wantCount {
		t.Fatalf(
			"version=%d history=%d, want %d rows",
			latest,
			count,
			wantCount,
		)
	}
	err = conn.QueryRow(
		"SELECT COUNT(*) FROM projects WHERE name='retained'",
	).Scan(&count)
	requireNoError(t, err, "read retained project")
	if count != 1 {
		t.Fatalf("retained projects = %d, want 1", count)
	}
}
