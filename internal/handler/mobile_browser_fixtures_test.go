//go:build mobilebrowser

package handler_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

const mobileSecretSentinel = "MOBILE_SECRET_SENTINEL_7c6d6ce4"

func mustCreateProject(
	t *testing.T,
	ctx context.Context,
	repo *repository.Repository,
) db.Project {
	t.Helper()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name: strings.Repeat("project-", 30),
			Description: sql.NullString{
				String: "hostile mobile project",
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return project
}

func mustCreateEnvironment(
	t *testing.T,
	ctx context.Context,
	repo *repository.Repository,
) db.Environment {
	t.Helper()
	environment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{
			Name: strings.Repeat("environment-", 20),
			Description: sql.NullString{
				String: "hostile mobile environment",
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	return environment
}

func mustCreateLifecycle(
	t *testing.T,
	ctx context.Context,
	repo *repository.Repository,
	environment db.Environment,
) db.Lifecycle {
	t.Helper()
	lifecycle, err := repo.Queries.CreateLifecycle(
		ctx,
		db.CreateLifecycleParams{
			Name:        "hostile lifecycle",
			Description: sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create lifecycle: %v", err)
	}
	if _, err := repo.Queries.CreateLifecycleStage(
		ctx,
		db.CreateLifecycleStageParams{
			LifecycleID:      lifecycle.ID,
			EnvironmentID:    environment.ID,
			SortOrder:        1,
			RequiresApproval: 1,
		},
	); err != nil {
		t.Fatalf("create lifecycle stage: %v", err)
	}
	nextEnvironment, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{
			Name: "later-" + strings.Repeat("environment-", 16),
		},
	)
	if err != nil {
		t.Fatalf("create second lifecycle environment: %v", err)
	}
	if _, err := repo.Queries.CreateLifecycleStage(
		ctx,
		db.CreateLifecycleStageParams{
			LifecycleID:      lifecycle.ID,
			EnvironmentID:    nextEnvironment.ID,
			SortOrder:        2,
			RequiresApproval: 0,
		},
	); err != nil {
		t.Fatalf("create second lifecycle stage: %v", err)
	}
	return lifecycle
}

func mustCreateMobileSteps(
	t *testing.T,
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
) []db.Step {
	t.Helper()
	steps := make([]db.Step, 2)
	for i := range steps {
		step, err := repo.Queries.CreateStep(
			ctx,
			db.CreateStepParams{
				ProjectID: project.ID,
				Name:      strings.Repeat("step-name-", 24),
				ScriptBody: "printf '%s\\n' '" +
					strings.Repeat("script-token-", 40) + "'",
				SortOrder: int64(i + 1),
			},
		)
		if err != nil {
			t.Fatalf("create step: %v", err)
		}
		steps[i] = step
	}
	return steps
}

func mustCreateRelease(
	t *testing.T,
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
) db.Release {
	t.Helper()
	release, err := repo.Queries.CreateRelease(
		ctx,
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   strings.Repeat("release-", 20),
			StepsJson: `[{"name":"hostile","script_body":"echo hostile","sort_order":1}]`,
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	return release
}

func mustCreateSchedule(
	t *testing.T,
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	release db.Release,
	environment db.Environment,
) db.ScheduledDeployment {
	t.Helper()
	schedule, err := repo.Queries.CreateScheduledDeployment(
		ctx,
		db.CreateScheduledDeploymentParams{
			ProjectID:     project.ID,
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Cron:          "0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31,32,33,34,35,36,37,38,39,40,41,42,43,44,45,46,47,48,49,50,51,52,53,54,55,56,57,58,59 0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23 1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23,24,25,26,27,28,29,30,31 1,2,3,4,5,6,7,8,9,10,11,12 0,1,2,3,4,5,6",
			NextRunAt:     time.Now().Add(time.Hour).Unix(),
			Enabled:       1,
			Note: sql.NullString{
				String: strings.Repeat("schedule-note-", 20),
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	return schedule
}

func mustCreateVariable(
	t *testing.T,
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
	environment db.Environment,
) db.Variable {
	t.Helper()
	variable, err := repo.CreateVariable(
		ctx,
		db.CreateVariableParams{
			ProjectID: project.ID,
			Name:      strings.Repeat("NON_SECRET_", 16),
			Value: sql.NullString{
				String: strings.Repeat("non-secret-value-", 32),
				Valid:  true,
			},
			EnvironmentID: sql.NullInt64{Int64: environment.ID, Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("create variable: %v", err)
	}
	return variable
}

func mustCreateSecretVariable(
	t *testing.T,
	ctx context.Context,
	repo *repository.Repository,
	project db.Project,
) db.Variable {
	t.Helper()
	variable, err := repo.CreateVariable(
		ctx,
		db.CreateVariableParams{
			ProjectID: project.ID,
			Name:      "MOBILE_SECRET_VARIABLE",
			Value: sql.NullString{
				String: mobileSecretSentinel,
				Valid:  true,
			},
			Secret: 1,
		},
	)
	if err != nil {
		t.Fatalf("create secret variable: %v", err)
	}
	return variable
}
