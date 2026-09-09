package migrate

import (
	"database/sql"
	"strings"
	"testing"

	"durpdeploy/internal/pgdriver"
	"durpdeploy/migrations"
)

func TestRemoteAgentSQLParityFixtures(t *testing.T) {
	for _, pair := range [][2]string{
		{"025_remote_agents.sql", "mssql/010_remote_agents.sql"},
		{"026_deployment_step_execution.sql", "mssql/011_deployment_step_execution.sql"},
		{"027_deployment_log_scopes.sql", "mssql/012_deployment_log_scopes.sql"},
		{"028_remote_deployment_claims.sql", "mssql/013_remote_deployment_claims.sql"},
	} {
		t.Run(pair[0], func(t *testing.T) {
			shared, err := migrations.FS.ReadFile(pair[0])
			requireNoError(t, err, "read shared SQL")
			native, err := migrations.MSSQLFS.ReadFile(pair[1])
			requireNoError(t, err, "read MSSQL SQL")
			for _, sql := range []string{string(shared), string(native)} {
				if strings.Count(sql, "-- +goose Up") != 1 ||
					strings.Count(sql, "-- +goose Down") != 1 {
					t.Fatal("missing or duplicate goose direction marker")
				}
			}
			rewritten := pgdriver.RewriteSQL(string(shared))
			for _, forbidden := range []string{
				"INTEGER", "BLOB", "unixepoch()", "sqlite_sequence",
				"PRAGMA", "RAISE(", "json_each", "AUTOINCREMENT",
			} {
				if strings.Contains(rewritten, forbidden) {
					t.Fatalf("unsupported PostgreSQL token %q", forbidden)
				}
			}
			if !strings.Contains(rewritten, "BIGINT") {
				t.Fatal("PostgreSQL integer rewrite missing")
			}
			t.Logf(
				"PostgreSQL rewritten fixture (static, not engine execution):\n%s",
				rewritten,
			)
		})
	}
}

func TestRemoteAgentMSSQLSchema(t *testing.T) {
	for _, engine := range []string{"sqlite", "mssql"} {
		t.Run(engine, func(t *testing.T) {
			var conn *sql.DB
			if engine == "mssql" {
				conn = newSQLServerTestDB(t)
			} else {
				var err error
				conn, err = Run(":memory:?_pragma=foreign_keys(1)")
				requireNoError(t, err, "migrate SQLite")
				t.Cleanup(func() {
					requireNoError(t, conn.Close(), "close SQLite")
				})
			}
			assertRemoteTables(t, conn)
			for _, size := range []int{1, 255, 0, 256} {
				value := strings.Repeat("a", size)
				_, err := conn.Exec(`INSERT INTO agents(id,name,endpoint)
					VALUES(?, 'agent', 'https://agent.test')`, value)
				valid := size == 1 || size == 255
				if (err == nil) != valid {
					t.Fatalf("ID length %d: valid=%v err=%v", size, valid, err)
				}
				_, err = conn.Exec(
					`UPDATE agents SET name=? WHERE id='a'`,
					value,
				)
				if (err == nil) != valid {
					t.Fatalf(
						"name length %d: valid=%v err=%v",
						size,
						valid,
						err,
					)
				}
				t.Logf("ID and name length %d: accepted=%v", size, valid)
			}
			for _, query := range []string{
				`INSERT INTO agents(id,name,endpoint)
				 VALUES('a','duplicate','https://agent.test')`,
				`UPDATE agents SET status='active' WHERE id='a'`,
				`UPDATE agents SET certificate_fingerprint='short' WHERE id='a'`,
			} {
				if _, err := conn.Exec(query); err == nil {
					t.Fatalf("unsafe agent accepted: %s", query)
				}
			}
			t.Log("duplicate identity, unpaired activation, short pin rejected")
		})
	}
}
