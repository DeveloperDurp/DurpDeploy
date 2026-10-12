package repository

import (
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

func TestDeploymentLiveVariablesAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		box, err := secret.NewBox(make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		repo.SetSecretBox(box)
		lc, err := repo.Queries.CreateLifecycle(
			t.Context(),
			db.CreateLifecycleParams{Name: "live"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Queries.SetProjectLifecycle(
			t.Context(),
			db.SetProjectLifecycleParams{
				ID: 1, LifecycleID: sql.NullInt64{Int64: lc.ID, Valid: true},
			},
		); err != nil {
			t.Fatal(err)
		}
		stage, err := repo.Queries.CreateLifecycleStage(
			t.Context(),
			db.CreateLifecycleStageParams{
				LifecycleID: lc.ID, EnvironmentID: 1, SortOrder: 1,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		input := LifecycleVariableInput{
			CreateLifecycleVariableParams: db.CreateLifecycleVariableParams{
				LifecycleID: lc.ID, Name: "REGION",
				Value:         sql.NullString{String: "current", Valid: true},
				EnvironmentID: sql.NullInt64{Int64: 1, Valid: true}, Secret: 1,
			},
		}
		variable, err := repo.SaveLifecycleVariable(t.Context(), input)
		if err != nil {
			t.Fatal(err)
		}
		local, err := repo.EncryptValue(
			sql.NullString{String: "project-frozen", Valid: true},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Queries.CreateReleaseVariable(
			t.Context(),
			db.CreateReleaseVariableParams{
				ReleaseID: 1, Name: "REGION", Value: local,
			},
		); err != nil {
			t.Fatal(err)
		}
		first, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending_approval",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		input.ID = variable.ID
		input.Value.String = "changed"
		if _, err := repo.SaveLifecycleVariable(
			t.Context(),
			input,
		); err != nil {
			t.Fatal(err)
		}
		assertDeploymentVariableValues(
			t,
			repo,
			first.Deployment.ID,
			"current",
			"project-frozen",
		)
		remote, err := repo.remotePayloadSnapshot(
			t.Context(),
			repo.Queries,
			"race-agent",
			first.Deployment.ID,
		)
		if err != nil || len(remote.Variables) != 2 ||
			remote.Variables[0].Value.String != "current" {
			t.Fatal("remote payload did not retain captured secret", err)
		}
		rerun, err := repo.CreateDeploymentFromDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending_approval",
			},
			first.Deployment.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		assertDeploymentVariableValues(
			t,
			repo,
			rerun.Deployment.ID,
			"changed",
			"project-frozen",
		)
		if err := repo.Queries.DeleteLifecycleStage(
			t.Context(),
			stage.ID,
		); err != nil {
			t.Fatal(err)
		}
		withoutStage, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending_approval",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		assertDeploymentVariableValues(
			t,
			repo,
			withoutStage.Deployment.ID,
			"project-frozen",
		)
		// Same-scope project snapshots suppress current lifecycle defaults.
		input.EnvironmentID = sql.NullInt64{}
		if _, err := repo.SaveLifecycleVariable(
			t.Context(),
			input,
		); err != nil {
			t.Fatal(err)
		}
		override, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending_approval",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		assertDeploymentVariableValues(
			t,
			repo,
			override.Deployment.ID,
			"project-frozen",
		)
		if _, err := repo.Queries.CreateLifecycleVariable(
			t.Context(),
			db.CreateLifecycleVariableParams{
				LifecycleID: lc.ID,
				Name:        "CORRUPT",
				Value: sql.NullString{
					String: "invalid-ciphertext",
					Valid:  true,
				},
			},
		); err != nil {
			t.Fatal(err)
		}
		before, err := repo.Queries.ListDeploymentsByRelease(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending_approval",
			},
		); err == nil {
			t.Fatal("corrupt shared ciphertext accepted")
		}
		after, err := repo.Queries.ListDeploymentsByRelease(t.Context(), 1)
		if err != nil || len(before) != len(after) {
			t.Fatal("failed capture left a deployment", err)
		}
		if err := deleteDeploymentHistory(
			t.Context(),
			repo.Queries,
			first.Deployment.ID,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Queries.GetDeploymentVariableSnapshot(
			t.Context(),
			first.Deployment.ID,
		); !errors.Is(
			err,
			sql.ErrNoRows,
		) {
			t.Fatal("deployment deletion retained variables", err)
		}
	})
}

func assertDeploymentVariableValues(
	t *testing.T,
	repo *Repository,
	id int64,
	want ...string,
) {
	t.Helper()
	variables, err := repo.ListDeploymentVariables(t.Context(), id)
	if err != nil || len(variables) != len(want) {
		t.Fatal("deployment variable count mismatch", err)
	}
	for i, value := range want {
		if variables[i].Value.String != value {
			t.Fatalf(
				"variable %d = %q, want %q",
				i,
				variables[i].Value.String,
				value,
			)
		}
	}
}
