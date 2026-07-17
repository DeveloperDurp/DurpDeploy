package db_test

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

func TestSmoke(t *testing.T) {
	ctx := context.Background()

	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer dbConn.Close()

	queries := db.New(dbConn)

	// projects
	proj, err := queries.CreateProject(ctx, db.CreateProjectParams{
		Name:        "test-project",
		Description: sql.NullString{String: "desc", Valid: true},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if proj.ID == 0 {
		t.Fatal("project id should not be zero")
	}
	proj2, err := queries.GetProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	if proj2.Name != proj.Name {
		t.Fatalf("project name mismatch")
	}

	// environments
	env, err := queries.CreateEnvironment(ctx, db.CreateEnvironmentParams{
		Name:        "test-env",
		Description: sql.NullString{String: "env desc", Valid: true},
		Tags:        sql.NullString{String: "tag1", Valid: true},
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	if env.ID == 0 {
		t.Fatal("environment id should not be zero")
	}
	env2, err := queries.GetEnvironment(ctx, env.ID)
	if err != nil {
		t.Fatalf("get environment: %v", err)
	}
	if env2.Name != env.Name {
		t.Fatalf("environment name mismatch")
	}

	// steps
	step, err := queries.CreateStep(ctx, db.CreateStepParams{
		ProjectID:  proj.ID,
		Name:       "test-step",
		ScriptBody: "echo hello",
		SortOrder:  1,
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	if step.ID == 0 {
		t.Fatal("step id should not be zero")
	}
	step2, err := queries.GetStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("get step: %v", err)
	}
	if step2.Name != step.Name {
		t.Fatalf("step name mismatch")
	}

	// variables
	variable, err := queries.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID:     proj.ID,
		Name:          "test-var",
		Value:         sql.NullString{String: "val", Valid: true},
		EnvironmentID: sql.NullInt64{Int64: env.ID, Valid: true},
	})
	if err != nil {
		t.Fatalf("create variable: %v", err)
	}
	if variable.ID == 0 {
		t.Fatal("variable id should not be zero")
	}
	variable2, err := queries.GetVariable(ctx, variable.ID)
	if err != nil {
		t.Fatalf("get variable: %v", err)
	}
	if variable2.Name != variable.Name {
		t.Fatalf("variable name mismatch")
	}

	// releases
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: proj.ID,
		Version:   "v1.0.0",
		StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	if release.ID == 0 {
		t.Fatal("release id should not be zero")
	}
	release2, err := queries.GetRelease(ctx, release.ID)
	if err != nil {
		t.Fatalf("get release: %v", err)
	}
	if release2.Version != release.Version {
		t.Fatalf("release version mismatch")
	}

	// release_variables
	rv, err := queries.CreateReleaseVariable(
		ctx,
		db.CreateReleaseVariableParams{
			ReleaseID:     release.ID,
			Name:          "test-rv",
			Value:         sql.NullString{String: "rv-val", Valid: true},
			EnvironmentID: sql.NullInt64{Int64: env.ID, Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("create release variable: %v", err)
	}
	if rv.ID == 0 {
		t.Fatal("release variable id should not be zero")
	}
	rv2, err := queries.GetReleaseVariable(ctx, rv.ID)
	if err != nil {
		t.Fatalf("get release variable: %v", err)
	}
	if rv2.Name != rv.Name {
		t.Fatalf("release variable name mismatch")
	}

	// deployments
	deployment, err := queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: env.ID,
		Status:        "pending",
		StartedAt:     sql.NullInt64{Int64: 0, Valid: false},
		FinishedAt:    sql.NullInt64{Int64: 0, Valid: false},
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if deployment.ID == 0 {
		t.Fatal("deployment id should not be zero")
	}
	deployment2, err := queries.GetDeployment(ctx, deployment.ID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if deployment2.Status != deployment.Status {
		t.Fatalf("deployment status mismatch")
	}

	// deployment_logs
	log, err := queries.CreateDeploymentLog(ctx, db.CreateDeploymentLogParams{
		DeploymentID: deployment.ID,
		StepName:     sql.NullString{String: "step1", Valid: true},
		Line:         "log line",
	})
	if err != nil {
		t.Fatalf("create deployment log: %v", err)
	}
	if log.ID == 0 {
		t.Fatal("deployment log id should not be zero")
	}
	log2, err := queries.GetDeploymentLog(ctx, log.ID)
	if err != nil {
		t.Fatalf("get deployment log: %v", err)
	}
	if log2.Line != log.Line {
		t.Fatalf("deployment log line mismatch")
	}

	// scheduled_deployments
	sched, err := queries.CreateScheduledDeployment(
		ctx,
		db.CreateScheduledDeploymentParams{
			ProjectID:     proj.ID,
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Cron:          "0 9 * * *",
			NextRunAt:     1234567890,
			Enabled:       1,
			LastFiredAt:   sql.NullInt64{Valid: false},
			Note: sql.NullString{
				String: "morning deploy",
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatalf("create scheduled deployment: %v", err)
	}
	if sched.ID == 0 {
		t.Fatal("scheduled deployment id should not be zero")
	}
	sched2, err := queries.GetScheduledDeployment(ctx, sched.ID)
	if err != nil {
		t.Fatalf("get scheduled deployment: %v", err)
	}
	if sched2.Cron != sched.Cron {
		t.Fatalf("scheduled deployment cron mismatch: got %q", sched2.Cron)
	}
	if sched2.NextRunAt != sched.NextRunAt {
		t.Fatalf(
			"scheduled deployment next_run_at mismatch: got %d",
			sched2.NextRunAt,
		)
	}
	if sched2.Enabled != sched.Enabled {
		t.Fatalf(
			"scheduled deployment enabled mismatch: got %d",
			sched2.Enabled,
		)
	}
}
