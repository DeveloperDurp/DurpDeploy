package repository

import (
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
)

func TestDeploymentEnvironmentIdentityAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		first, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending_approval",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Queries.UpdateEnvironment(t.Context(), db.UpdateEnvironmentParams{
			ID: 1, Name: `prd $(echo literal); 'quoted'`,
		}); err != nil {
			t.Fatal(err)
		}
		captured, err := repo.Queries.GetDeployment(
			t.Context(),
			first.Deployment.ID,
		)
		if err != nil || !captured.EnvironmentNameSnapshot.Valid ||
			captured.EnvironmentNameSnapshot.String != "creation-race" {
			t.Fatalf("accepted context changed: %+v: %v", captured, err)
		}
		remote, err := repo.remotePayloadSnapshot(
			t.Context(),
			repo.Queries,
			"race-agent",
			captured.ID,
		)
		if err != nil ||
			remote.Deployment.EnvironmentNameSnapshot != captured.EnvironmentNameSnapshot {
			t.Fatalf("remote context differs: %+v: %v", remote.Deployment, err)
		}
		rerun, err := repo.CreateDeploymentFromDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending_approval",
			},
			first.Deployment.ID,
		)
		if err != nil ||
			rerun.Deployment.EnvironmentNameSnapshot.String != `prd $(echo literal); 'quoted'` {
			t.Fatalf(
				"new acceptance did not capture rename: %+v: %v",
				rerun,
				err,
			)
		}
	})
}

func TestEnvironmentIdentityRejectsAllDefinitionWrites(t *testing.T) {
	repo, _ := openDeploymentCreationEngine(
		t,
		newDeploymentCreationEngine(t, "SQLite"),
	)
	lifecycle, err := repo.Queries.CreateLifecycle(
		t.Context(),
		db.CreateLifecycleParams{Name: "identity"},
	)
	if err != nil {
		t.Fatal(err)
	}
	variable, err := repo.CreateVariable(
		t.Context(),
		db.CreateVariableParams{ProjectID: 1, Name: "ORIGINAL"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{containerenv.EnvironmentNameVariable, containerenv.EnvironmentIDVariable} {
		_, err := repo.CreateVariable(
			t.Context(),
			db.CreateVariableParams{ProjectID: 1, Name: name},
		)
		if !errors.Is(err, containerenv.ErrReserved) {
			t.Fatal("project create", err)
		}
		_, err = repo.UpdateVariable(
			t.Context(),
			db.UpdateVariableParams{ID: variable.ID, Name: name},
		)
		if !errors.Is(err, containerenv.ErrReserved) {
			t.Fatal("project update", err)
		}
		_, err = repo.UpdateVariableKeepValue(
			t.Context(),
			db.UpdateVariableKeepValueParams{ID: variable.ID, Name: name},
		)
		if !errors.Is(err, containerenv.ErrReserved) {
			t.Fatal("project keep secret", err)
		}
		_, err = repo.SaveLifecycleVariable(t.Context(), LifecycleVariableInput{
			CreateLifecycleVariableParams: db.CreateLifecycleVariableParams{
				LifecycleID: lifecycle.ID,
				Name:        name,
				Value:       sql.NullString{String: "ignored", Valid: true},
			},
		})
		if !errors.Is(err, containerenv.ErrReserved) {
			t.Fatal("lifecycle save", err)
		}
		_, err = repo.CreateCredential(
			t.Context(),
			1,
			false,
			CredentialCreateInput{Name: name, Value: "ignored"},
		)
		if !errors.Is(err, ErrCredentialInput) {
			t.Fatal("credential create", err)
		}
	}
}
