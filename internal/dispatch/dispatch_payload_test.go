package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestDispatchPayload_GlobalVariables(t *testing.T) {
	// Given
	repo, box := newPayloadRepository(t)
	deployment := createPayloadDeployment(t, repo)
	createPayloadVariable(t, repo, box, deployment.ReleaseID, payloadVariable{
		name: "REGION", value: "global",
	})

	// When
	payload := buildDispatchPayload(t, repo, box, deployment)

	// Then
	if got := payload.Variables; len(got) != 1 || got[0].Name != "REGION" ||
		got[0].Value != "global" {
		t.Fatalf("variables = %#v, want one global REGION", got)
	}
}

func TestDispatchPayload_EnvWinsOverGlobal(t *testing.T) {
	// Given
	repo, box := newPayloadRepository(t)
	deployment := createPayloadDeployment(t, repo)
	createPayloadVariable(t, repo, box, deployment.ReleaseID, payloadVariable{
		name: "REGION", value: "global",
	})
	createPayloadVariable(t, repo, box, deployment.ReleaseID, payloadVariable{
		name: "REGION", value: "environment",
		environmentID: sql.NullInt64{
			Int64: deployment.EnvironmentID,
			Valid: true,
		},
	})

	// When
	payload := buildDispatchPayload(t, repo, box, deployment)

	// Then
	if got := payload.Variables; len(got) != 1 || got[0].Name != "REGION" ||
		got[0].Value != "environment" {
		t.Fatalf("variables = %#v, want one environment REGION", got)
	}
}

func TestDispatchPayload_DeterministicLastWriteWins(t *testing.T) {
	// Given
	repo, box := newPayloadRepository(t)
	deployment := createPayloadDeployment(t, repo)
	for _, value := range []string{"first", "last"} {
		createPayloadVariable(t, repo, box, deployment.ReleaseID, payloadVariable{
			name: "REGION", value: value,
			environmentID: sql.NullInt64{
				Int64: deployment.EnvironmentID,
				Valid: true,
			},
		})
	}

	// When
	payload := buildDispatchPayload(t, repo, box, deployment)

	// Then
	if got := payload.Variables; len(got) != 1 || got[0].Name != "REGION" ||
		got[0].Value != "last" {
		t.Fatalf("variables = %#v, want last environment REGION", got)
	}
}

func newPayloadRepository(t *testing.T) (*repository.Repository, *secret.Box) {
	t.Helper()
	connection, err := migrate.Run(
		"file:" + filepath.Join(t.TempDir(), "dispatch.db") +
			"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("create secret box: %v", err)
	}
	return repository.New(connection), box
}

func createPayloadDeployment(t *testing.T, repo *repository.Repository) db.Deployment {
	t.Helper()
	ctx := context.Background()
	project, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{Name: "project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := repo.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	return deployment
}

type payloadVariable struct {
	name          string
	value         string
	environmentID sql.NullInt64
}

func createPayloadVariable(
	t *testing.T,
	repo *repository.Repository,
	box *secret.Box,
	releaseID int64,
	variable payloadVariable,
) {
	t.Helper()
	ciphertext, err := box.Encrypt(variable.value)
	if err != nil {
		t.Fatalf("encrypt test variable: %v", err)
	}
	_, err = repo.Queries.CreateReleaseVariable(
		context.Background(),
		db.CreateReleaseVariableParams{
			ReleaseID:     releaseID,
			Name:          variable.name,
			Value:         sql.NullString{String: ciphertext, Valid: true},
			EnvironmentID: variable.environmentID,
		},
	)
	if err != nil {
		t.Fatalf("create release variable: %v", err)
	}
}

func buildDispatchPayload(
	t *testing.T,
	repo *repository.Repository,
	box *secret.Box,
	deployment db.Deployment,
) payload {
	t.Helper()
	dispatcher := New(repo, box, nil)
	ciphertext, err := dispatcher.buildPayload(
		context.Background(),
		repo.Queries,
		deployment,
	)
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	plaintext, err := box.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("decrypt payload: %v", err)
	}
	var decoded payload
	if err := json.Unmarshal([]byte(plaintext), &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	return decoded
}
