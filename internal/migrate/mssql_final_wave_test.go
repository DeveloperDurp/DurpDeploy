package migrate

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestSQLServer_FinalWaveRuntimeParity(t *testing.T) {
	ctx := context.Background()
	dbConn := newSQLServerTestDB(t)
	queries := db.New(dbConn)

	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "final-wave@example.com", PasswordHash: "hash", Name: "Final", Role: "admin",
	})
	requireNoError(t, err, "create user")
	session, err := queries.CreateSession(ctx, db.CreateSessionParams{
		ID: "final-wave-session", UserID: user.ID, CsrfToken: "csrf", ExpiresAt: 2_200_000_000,
	})
	requireNoError(t, err, "create session")
	if session.CreatedAt <= 0 {
		t.Fatalf(
			"session created at = %d, want UTC Unix seconds",
			session.CreatedAt,
		)
	}

	_, err = dbConn.ExecContext(
		ctx,
		"DBCC CHECKIDENT ('projects', RESEED, 2147483648)",
	)
	requireNoError(t, err, "reseed project identity")
	project, err := queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "final-wave-project"},
	)
	requireNoError(t, err, "create large-id project")
	environment, err := queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "final-wave-environment"},
	)
	requireNoError(t, err, "create environment")
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "final-wave", StepsJson: "[]",
	})
	requireNoError(t, err, "create release")
	deployment, err := queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	requireNoError(t, err, "create deployment")

	_, err = queries.ListDeploymentsWithRefsFiltered(
		ctx,
		db.ListDeploymentsWithRefsFilteredParams{
			FProjectID: sql.NullInt64{Int64: project.ID, Valid: true},
			FEnvID:     sql.NullInt64{Int64: environment.ID, Valid: true},
			FFromUnix:  sql.NullInt64{Int64: 2_200_000_000, Valid: true},
			FToUnix:    sql.NullInt64{Int64: 2_200_000_001, Valid: true},
			PageLimit:  1,
		},
	)
	requireNoError(t, err, "run large integer filter")
	latest, err := queries.ListLatestDeploymentPerReleaseEnv(ctx)
	requireNoError(t, err, "run homepage latest deployment query")
	if len(latest) != 1 || latest[0].ID != deployment.ID {
		t.Fatalf("homepage latest deployments = %#v", latest)
	}

	_, err = queries.CreateNotificationEvent(
		ctx,
		db.CreateNotificationEventParams{
			EventType: "final-wave", DeploymentID: sql.NullInt64{Int64: deployment.ID, Valid: true},
			ProjectID: sql.NullInt64{
				Int64: project.ID,
				Valid: true,
			}, Message: "cascade", Results: "{}",
		},
	)
	requireNoError(t, err, "create notification event")
	_, err = queries.CreateScheduledDeployment(
		ctx,
		db.CreateScheduledDeploymentParams{
			ProjectID: project.ID, ReleaseID: release.ID, EnvironmentID: environment.ID,
			Cron: "0 * * * *", NextRunAt: 2_200_000_000,
		},
	)
	requireNoError(t, err, "create scheduled deployment")
	requireNoError(t, queries.DeleteProject(ctx, project.ID), "delete project")

	for _, table := range []string{"notification_events", "scheduled_deployments"} {
		var count int
		err = dbConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).
			Scan(&count)
		requireNoError(t, err, "count deleted "+table+" rows")
		if count != 0 {
			t.Fatalf("%s rows after project delete = %d, want 0", table, count)
		}
	}
}
