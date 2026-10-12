package main

import (
	"database/sql"
	"io"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestRunSecretKeyPlaintextRejectsEncryptedServerState(t *testing.T) {
	for _, store := range serverStateStores {
		t.Run(store.name, func(t *testing.T) {
			f := newServerStateKeyFixture(t)
			value, err := f.rotation.oldBox.Encrypt("encrypted-only")
			if err != nil {
				t.Fatal(err)
			}
			store.seed(t, f, value)
			variable, err := f.repo.Queries.CreateVariable(
				t.Context(),
				db.CreateVariableParams{
					ProjectID: 1,
					Name:      "legacy",
					Value: sql.NullString{
						String: "unchanged-legacy",
						Valid:  true,
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			var seq int
			var database, dsn string
			if err := f.repo.DB.QueryRowContext(t.Context(), "PRAGMA database_list").
				Scan(&seq, &database, &dsn); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DURPDEPLOY_DB", dsn)
			// Plaintext mode must reach the encrypted-only store without loading a key.
			t.Setenv("DURPDEPLOY_SECRET_KEY", "invalid-key")
			original := os.Stderr
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			os.Stderr = writer
			code := runSecretKey([]string{"rotate", "--plaintext"})
			os.Stderr = original
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			output, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			if code != 1 ||
				!strings.Contains(
					string(output),
					"--plaintext cannot rotate encrypted server state",
				) {
				t.Fatalf(
					"CLI did not reject encrypted-only state: code %d, error %s",
					code,
					output,
				)
			}
			var after string
			if err := f.repo.DB.QueryRowContext(t.Context(), "SELECT value FROM variables WHERE id = ?", variable.ID).
				Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != "unchanged-legacy" {
				t.Fatal("rejected CLI migration changed earlier variable")
			}
		})
	}
}
