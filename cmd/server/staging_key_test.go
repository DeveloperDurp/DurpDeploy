package main

import (
	"database/sql"
	"encoding/base64"
	"testing"

	"durpdeploy/internal/db"
)

func TestRunSecretKeyRejectsStagingUntilCleanup(t *testing.T) {
	for _, mode := range []string{"encrypted", "plaintext"} {
		t.Run(mode, func(t *testing.T) {
			f := newServerStateKeyFixture(t)
			ctx := t.Context()
			ciphertext, err := f.rotation.oldBox.Encrypt("staged-content")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.repo.Queries.CreateStagingSnapshot(
				ctx, db.CreateStagingSnapshotParams{
					DeploymentID: 1, StepIndex: 0, Producer: "server",
					Sha256: "fixture", Size: 14,
				},
			); err != nil {
				t.Fatal(err)
			}
			if err := f.repo.Queries.CreateStagingChunk(
				ctx, db.CreateStagingChunkParams{
					DeploymentID: 1, StepIndex: 0, Producer: "server",
					Ciphertext: ciphertext,
				},
			); err != nil {
				t.Fatal(err)
			}
			value := "unchanged-variable"
			args := []string{"rotate"}
			if mode == "plaintext" {
				args = append(args, "--plaintext")
			} else {
				value, err = f.rotation.oldBox.Encrypt(value)
				if err != nil {
					t.Fatal(err)
				}
			}
			variable, err := f.repo.Queries.CreateVariable(
				ctx, db.CreateVariableParams{
					ProjectID: 1, Name: "preserved",
					Value: sql.NullString{String: value, Valid: true},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			var seq int
			var database, dsn string
			if err := f.repo.DB.QueryRowContext(ctx, "PRAGMA database_list").
				Scan(&seq, &database, &dsn); err != nil {
				t.Fatal(err)
			}
			t.Setenv("DURPDEPLOY_DB", dsn)
			t.Setenv("DURPDEPLOY_SECRET_KEY",
				base64.StdEncoding.EncodeToString(make([]byte, 32)))
			if mode == "plaintext" {
				t.Setenv("DURPDEPLOY_SECRET_KEY", "invalid-key")
			}
			var code int
			output := captureStdout(t, func() { code = runSecretKey(args) })
			if code != 1 || output != "" {
				t.Fatalf("rotation with staging: exit %d, stdout bytes %d",
					code, len(output))
			}
			after, err := f.repo.Queries.GetVariable(ctx, variable.ID)
			if err != nil || after.Value.String != value {
				t.Fatalf("rejected rotation changed the variable: %v", err)
			}
			staged, err := f.repo.Queries.GetStagingChunk(
				ctx, db.GetStagingChunkParams{
					DeploymentID: 1, StepIndex: 0, Producer: "server",
				},
			)
			if err != nil || staged != ciphertext {
				t.Fatalf("rejected rotation changed staged content: %v", err)
			}
			if err := f.repo.Queries.DeleteStepStaging(
				ctx, db.DeleteStepStagingParams{DeploymentID: 1, StepIndex: 0},
			); err != nil {
				t.Fatal(err)
			}
			box := runServerStateRotationCLI(t, f, mode == "plaintext")
			after, err = f.repo.Queries.GetVariable(ctx, variable.ID)
			if err != nil {
				t.Fatal(err)
			}
			plain, err := box.Decrypt(after.Value.String)
			if err != nil || plain != "unchanged-variable" {
				t.Fatalf("rotation after cleanup lost the variable: %v", err)
			}
		})
	}
}
