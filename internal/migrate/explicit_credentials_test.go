package migrate

import (
	"fmt"
	"testing"

	"durpdeploy/internal/testutil"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

func TestCredentialEnrollmentMigration(t *testing.T) {
	forEachRemoteClaimDatabase(
		t,
		func(t *testing.T, fixture remoteClaimTestDB) {
			fixture.baselineVersion = 72
			if fixture.gooseDialect == "mssql" {
				fixture.baselineVersion = 57
			}
			conn := fixture.openBaseline(t)
			t.Cleanup(func() { testutil.Close(t, conn) })
			_, err := conn.Exec(
				"INSERT INTO projects (name) VALUES ('Credential migration')",
			)
			require.NoError(t, err)
			_, err = conn.Exec(
				"INSERT INTO lifecycles (name) VALUES ('Credential migration')",
			)
			require.NoError(t, err)
			for _, table := range []struct{ name, owner string }{{"variables", "project_id"}, {"lifecycle_variables", "lifecycle_id"}} {
				_, err = conn.Exec(
					fmt.Sprintf(
						"INSERT INTO %s (%s,name,value,secret,expires_at,rotation_due_at,rotated_at,rotation_revision) VALUES (1,'EXISTING_SECRET','retained-ciphertext',1,1,2,3,4)",
						table.name,
						table.owner,
					),
				)
				require.NoError(t, err)
			}
			require.NoError(t, goose.UpTo(conn, ".", fixture.baselineVersion+1))
			for _, table := range []string{"variables", "lifecycle_variables"} {
				var credential, expiry, due, rotated, revision int64
				var value string
				require.NoError(
					t,
					conn.QueryRow("SELECT is_credential,expires_at,rotation_due_at,rotated_at,rotation_revision,value FROM "+table+" WHERE id=1").
						Scan(&credential, &expiry, &due, &rotated, &revision, &value),
				)
				require.Zero(t, credential)
				require.Equal(
					t,
					[]int64{1, 2, 3, 4},
					[]int64{expiry, due, rotated, revision},
				)
				require.Equal(t, "retained-ciphertext", value)
				_, err = conn.Exec(
					"UPDATE " + table + " SET is_credential=1 WHERE id=1",
				)
				require.NoError(t, err)
				_, err = conn.Exec(
					"UPDATE " + table + " SET secret=0 WHERE id=1",
				)
				require.Error(
					t,
					err,
					"credentials cannot become plaintext variables",
				)
			}
			require.NoError(t, goose.DownTo(conn, ".", fixture.baselineVersion))
		},
	)
}
